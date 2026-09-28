package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// 订单与支付的用例。数据源一律走真实 SQLite（见 billing_plans_test.go 的
// newBillingTestService）：订单状态机、券核销与订阅顺延都依赖 SQL 语义，
// 用假存储测出来的"通过"证明不了这些行为。

// seedBillingUser 建一个真实账号：订单视图要靠 UserByID 拼用户名/邮箱。
func seedBillingUser(t *testing.T, env *billingTestEnv, id string, name string, email string) {
	t.Helper()
	nameValue := name
	emailValue := email
	if err := env.store.CreateUser(&User{ID: id, Name: &nameValue, Email: &emailValue}); err != nil {
		t.Fatalf("写入账号失败: %v", err)
	}
}

// seedBillingCoupon 直接落一张满减券，绕过后台表单校验以精确控制额度与门槛。
func seedBillingCoupon(t *testing.T, env *billingTestEnv, code string, value int64, minAmountFen int64, quota int) BillingCoupon {
	t.Helper()
	coupon := &BillingCoupon{
		Code:         code,
		Name:         "测试券",
		Kind:         CouponKindAmount,
		Value:        value,
		MinAmountFen: minAmountFen,
		TotalQuota:   quota,
		Enabled:      true,
	}
	if err := env.store.SaveBillingCoupon(coupon); err != nil {
		t.Fatalf("写入优惠券失败: %v", err)
	}
	return *coupon
}

// testAggregateSign 按文档规则独立实现一遍签名，避免"用被测函数给自己签名"。
func testAggregateSign(fields map[string]string, secret string) string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if key == "sign" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+fields[key])
	}
	payload := strings.Join(parts, "&") + "&key=" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func signedBillingCallback(t *testing.T, fields map[string]string, secret string) []byte {
	t.Helper()
	signed := map[string]string{}
	for key, value := range fields {
		signed[key] = value
	}
	signed["sign"] = testAggregateSign(signed, secret)
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("序列化回调失败: %v", err)
	}
	return raw
}

func findPaymentChannel(t *testing.T, view *PaymentChannelsView, channel string) PaymentChannelView {
	t.Helper()
	for _, item := range view.Channels {
		if item.Channel == channel {
			return item
		}
	}
	t.Fatalf("渠道视图里没有 %s", channel)
	return PaymentChannelView{}
}

func mustMarkPaidErr(t *testing.T, env *billingTestEnv, orderID string) error {
	t.Helper()
	_, err := env.service.MarkBillingOrderPaid(orderID, "")
	return err
}

// TestBillingOrderQuote 覆盖试算：无券、有效券、无效券码。
func TestBillingOrderQuote(t *testing.T) {
	env := newBillingTestService(t)
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 5000, PeriodDays: 30, Enabled: true})
	seedBillingCoupon(t, env, "NEW1000", 1000, 0, 10)

	// 无券：应付等于原价。
	quote, err := env.service.QuoteBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month"})
	if err != nil {
		t.Fatalf("无券试算失败: %v", err)
	}
	if quote.AmountFen != 5000 || quote.DiscountFen != 0 || quote.PayableFen != 5000 || quote.CouponError != "" {
		t.Fatalf("无券试算结果不对: %+v", quote)
	}

	// 有效券：小写券码应被归一化后命中。
	quote, err = env.service.QuoteBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month", CouponCode: " new1000 "})
	if err != nil {
		t.Fatalf("有效券试算失败: %v", err)
	}
	if quote.AmountFen != 5000 || quote.DiscountFen != 1000 || quote.PayableFen != 4000 {
		t.Fatalf("有效券试算金额不对: %+v", quote)
	}
	if quote.CouponCode != "NEW1000" || quote.CouponError != "" {
		t.Fatalf("有效券应回填券码且无错误: %+v", quote)
	}

	// 无效券码：不报错，只把原因放进 CouponError。
	quote, err = env.service.QuoteBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month", CouponCode: "NOPE"})
	if err != nil {
		t.Fatalf("无效券不应让整单失败: %v", err)
	}
	if quote.DiscountFen != 0 || quote.PayableFen != 5000 || quote.CouponError == "" {
		t.Fatalf("无效券应原价试算并给出原因: %+v", quote)
	}

	// 停用套餐不可下单。
	seedPlan(t, env, BillingPlanInput{Code: "paused-plan", Name: "下架套餐", PriceFen: 1000, PeriodDays: 30, Enabled: false})
	_, err = env.service.QuoteBillingOrder("user-1", BillingOrderInput{PlanCode: "paused-plan"})
	assertBillingError(t, err, http.StatusBadRequest, "套餐不存在或已停用")
}

// TestBillingOrderCreateRedeemsCoupon 覆盖下单：订单号、金额、券核销。
func TestBillingOrderCreateRedeemsCoupon(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 5000, PeriodDays: 30, Enabled: true})
	seedBillingCoupon(t, env, "NEW1000", 1000, 0, 10)

	view, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month", CouponCode: "NEW1000"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if view.OrderNo == "" {
		t.Fatal("订单号不应为空")
	}
	if view.Status != OrderStatusPending {
		t.Fatalf("新订单应为 PENDING，实际 %s", view.Status)
	}
	// 没有启用任何真实渠道时默认走手工渠道。
	if view.Provider != PaymentChannelManual {
		t.Fatalf("默认渠道应为 MANUAL，实际 %s", view.Provider)
	}
	if view.AmountFen != 5000 || view.DiscountFen != 1000 || view.PayableFen != 4000 {
		t.Fatalf("下单金额不对: %+v", view)
	}
	if view.CouponCode != "NEW1000" {
		t.Fatalf("订单应记录券码快照，实际 %q", view.CouponCode)
	}
	if view.UserName != "小明" || view.UserEmail != "ming@example.com" {
		t.Fatalf("订单视图应补齐账号信息: %+v", view)
	}
	expectedExpiry := env.clock.Now().Add(billingOrderTTL)
	if !view.ExpiresAt.Equal(expectedExpiry) {
		t.Fatalf("支付超时应为 %v，实际 %v", expectedExpiry, view.ExpiresAt)
	}

	// 券被核销：used_count +1，且留下按订单可回滚的核销记录。
	coupon, err := env.store.BillingCouponByCode("NEW1000")
	if err != nil {
		t.Fatalf("读取券失败: %v", err)
	}
	if coupon.UsedCount != 1 {
		t.Fatalf("券应被核销一次，实际 used_count=%d", coupon.UsedCount)
	}
	order, err := env.store.BillingOrderByNo(view.OrderNo)
	if err != nil {
		t.Fatalf("按订单号回查失败: %v", err)
	}
	if order.ID != view.ID || order.CouponID != coupon.ID {
		t.Fatalf("订单未正确落库: %+v", order)
	}
}

// TestBillingOrderForUserForbidden 覆盖越权：别人的订单看起来就是"不存在"。
func TestBillingOrderForUserForbidden(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 5000, PeriodDays: 30, Enabled: true})

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if _, err := env.service.BillingOrderForUser("user-2", order.ID); err != nil {
		assertBillingError(t, err, http.StatusForbidden, "订单不存在")
	} else {
		t.Fatal("越权读取他人工单应被拒绝")
	}
	if _, err := env.service.BillingOrderForUser("user-1", order.ID); err != nil {
		t.Fatalf("本人读取自己的订单应成功: %v", err)
	}
}

// TestBillingCallbackIdempotent 覆盖回调：验签、置为已支付、重复回调只激活一次订阅。
func TestBillingCallbackIdempotent(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 5000, PeriodDays: 30, Enabled: true})

	if _, err := env.service.UpdatePaymentChannel(PaymentChannelAggregate, PaymentChannelInput{
		Enabled: true,
		Config: map[string]string{
			"appId":     "app-1",
			"createUrl": "https://gw.example.com/pay",
			"appSecret": "s3cr3t",
			"notifyUrl": "https://kino.example.com/api/billing/callback",
		},
	}, "admin-1"); err != nil {
		t.Fatalf("保存聚合渠道失败: %v", err)
	}

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if order.Provider != PaymentChannelAggregate {
		t.Fatalf("存在启用渠道时应默认使用它，实际 %s", order.Provider)
	}

	raw := signedBillingCallback(t, map[string]string{
		"orderNo":   order.OrderNo,
		"tradeNo":   "trade-1",
		"status":    "SUCCESS",
		"amountFen": "5000",
	}, "s3cr3t")

	if err := env.service.HandleBillingCallback(PaymentChannelAggregate, raw, nil); err != nil {
		t.Fatalf("首次回调失败: %v", err)
	}
	// 渠道会重复回调：第二次必须原样成功且不再续期。
	if err := env.service.HandleBillingCallback(PaymentChannelAggregate, raw, nil); err != nil {
		t.Fatalf("重复回调应幂等: %v", err)
	}

	history, err := env.store.SubscriptionHistory("user-1", 10)
	if err != nil {
		t.Fatalf("读取订阅历史失败: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("两次回调只应激活一条订阅，实际 %d 条", len(history))
	}
	if expected := env.clock.Now().AddDate(0, 0, 30); !history[0].ExpiresAt.Equal(expected) {
		t.Fatalf("订阅到期时间应为 %v，实际 %v（重复激活会顺延成两期）", expected, history[0].ExpiresAt)
	}

	paid, err := env.store.BillingOrderByNo(order.OrderNo)
	if err != nil {
		t.Fatalf("回查订单失败: %v", err)
	}
	if paid.Status != OrderStatusPaid || paid.ProviderOrderNo != "trade-1" || paid.PaidAt == nil {
		t.Fatalf("订单未正确置为已支付: %+v", paid)
	}

	// 验签错误必须被拒，且不改变任何状态。
	bad := signedBillingCallback(t, map[string]string{"orderNo": order.OrderNo, "status": "SUCCESS"}, "wrong-secret")
	assertBillingError(t, env.service.HandleBillingCallback(PaymentChannelAggregate, bad, nil), http.StatusBadRequest, "支付回调验签失败")
}

// TestBillingMarkPaidIdempotent 覆盖手工补单的幂等。
func TestBillingMarkPaidIdempotent(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 5000, PeriodDays: 30, Enabled: true})

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	first, err := env.service.MarkBillingOrderPaid(order.ID, "")
	if err != nil {
		t.Fatalf("补单失败: %v", err)
	}
	if first.Status != OrderStatusPaid || first.Remark != "手工补单" || first.PaidAt == nil {
		t.Fatalf("补单结果不对: %+v", first)
	}
	second, err := env.service.MarkBillingOrderPaid(order.ID, "")
	if err != nil {
		t.Fatalf("重复补单应幂等: %v", err)
	}
	if second.PaidAt == nil || !second.PaidAt.Equal(*first.PaidAt) {
		t.Fatalf("重复补单不应刷新支付时间: %v -> %v", first.PaidAt, second.PaidAt)
	}
	history, err := env.store.SubscriptionHistory("user-1", 10)
	if err != nil {
		t.Fatalf("读取订阅历史失败: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("重复补单只应激活一条订阅，实际 %d 条", len(history))
	}
}

// TestBillingRefundReleasesCoupon 覆盖退款：状态、券回滚、已退款禁止补单。
func TestBillingRefundReleasesCoupon(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 5000, PeriodDays: 30, Enabled: true})
	seedBillingCoupon(t, env, "NEW1000", 1000, 0, 10)

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month", CouponCode: "NEW1000"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if _, err := env.service.MarkBillingOrderPaid(order.ID, ""); err != nil {
		t.Fatalf("补单失败: %v", err)
	}
	coupon, err := env.store.BillingCouponByCode("NEW1000")
	if err != nil {
		t.Fatalf("读取券失败: %v", err)
	}
	if coupon.UsedCount != 1 {
		t.Fatalf("补单后券应已核销，实际 used_count=%d", coupon.UsedCount)
	}

	refunded, err := env.service.RefundBillingOrder(order.ID, "客户申请")
	if err != nil {
		t.Fatalf("退款失败: %v", err)
	}
	if refunded.Status != OrderStatusRefunded || refunded.Remark != "客户申请" {
		t.Fatalf("退款结果不对: %+v", refunded)
	}
	coupon, err = env.store.BillingCouponByCode("NEW1000")
	if err != nil {
		t.Fatalf("读取券失败: %v", err)
	}
	if coupon.UsedCount != 0 {
		t.Fatalf("退款应回滚券，实际 used_count=%d", coupon.UsedCount)
	}

	// 已退款订单不允许再补单。
	assertBillingError(t, mustMarkPaidErr(t, env, order.ID), http.StatusBadRequest, "订单已退款，不能补单")
}

// TestBillingUpdatePaymentChannelKeepsSecret 覆盖密钥"只写不读"与不回显。
func TestBillingUpdatePaymentChannelKeepsSecret(t *testing.T) {
	env := newBillingTestService(t)

	view, err := env.service.UpdatePaymentChannel(PaymentChannelAggregate, PaymentChannelInput{
		Enabled: true,
		Config: map[string]string{
			"appId":     "app-1",
			"createUrl": "https://gw.example.com/pay",
			"appSecret": "s3cr3t",
		},
	}, "admin-1")
	if err != nil {
		t.Fatalf("首次保存渠道失败: %v", err)
	}
	aggregate := findPaymentChannel(t, view, PaymentChannelAggregate)
	if !aggregate.Enabled || !aggregate.Ready || !aggregate.HasSecret {
		t.Fatalf("渠道应已启用且就绪: %+v", aggregate)
	}
	if _, leaked := aggregate.Config[paymentConfigAppSecret]; leaked {
		t.Fatal("渠道视图不应回传 appSecret")
	}
	if aggregate.Config["createUrl"] != "https://gw.example.com/pay" {
		t.Fatalf("非密钥字段应回传: %+v", aggregate.Config)
	}

	// 第二次提交 appSecret 为空：应保持原值，同时更新其它字段。
	view, err = env.service.UpdatePaymentChannel(PaymentChannelAggregate, PaymentChannelInput{
		Enabled: true,
		Config: map[string]string{
			"appSecret": "",
			"createUrl": "https://gw2.example.com/pay",
		},
	}, "admin-2")
	if err != nil {
		t.Fatalf("二次保存渠道失败: %v", err)
	}
	aggregate = findPaymentChannel(t, view, PaymentChannelAggregate)
	if !aggregate.HasSecret {
		t.Fatal("密钥留空时应保持原值（hasSecret 仍为 true）")
	}
	if _, leaked := aggregate.Config[paymentConfigAppSecret]; leaked {
		t.Fatal("渠道视图不应回传 appSecret")
	}
	if aggregate.Config["createUrl"] != "https://gw2.example.com/pay" {
		t.Fatalf("本次提交的字段应被更新: %+v", aggregate.Config)
	}
	if aggregate.Config["appId"] != "app-1" {
		t.Fatalf("本次未提交的字段应保持原值: %+v", aggregate.Config)
	}

	// 直接从库里解密，确认密钥没有被空值覆盖。
	config, enabled, err := env.service.billingChannelConfig(PaymentChannelAggregate)
	if err != nil {
		t.Fatalf("读取渠道配置失败: %v", err)
	}
	if !enabled {
		t.Fatal("渠道应处于启用状态")
	}
	if config[paymentConfigAppSecret] != "s3cr3t" {
		t.Fatalf("库里的 appSecret 应保持原值，实际 %q", config[paymentConfigAppSecret])
	}

	// 手工渠道是兜底，不允许被停用。
	_, err = env.service.UpdatePaymentChannel(PaymentChannelManual, PaymentChannelInput{Enabled: false}, "admin-1")
	assertBillingError(t, err, http.StatusBadRequest, "手工渠道不能停用")

	// 渠道列表固定返回四个渠道，手工渠道始终可用。
	channels, err := env.service.AdminPaymentChannels()
	if err != nil {
		t.Fatalf("读取渠道列表失败: %v", err)
	}
	if len(channels.Channels) != 4 {
		t.Fatalf("应返回四个渠道，实际 %d", len(channels.Channels))
	}
	manual := findPaymentChannel(t, channels, PaymentChannelManual)
	if !manual.Enabled || !manual.Ready {
		t.Fatalf("手工渠道应始终可用: %+v", manual)
	}
}

// TestBillingManualCallbackRejected 是那条"匿名回调白拿套餐"的回归测试。
//
// 手工渠道没有外部网关，也就没有可验签的报文；它的确认到账只能由后台补单完成。
// 一旦放行到 /api/payments/callback/:channel（匿名路由），任何人只要拿到订单号就能
// 零元开通套餐——这里从服务层直接打同一条代码路径。
func TestBillingManualCallbackRejected(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 9900, PeriodDays: 30, Enabled: true})

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if order.Provider != PaymentChannelManual {
		t.Fatalf("未配置在线渠道时应落在手工渠道，实际 %s", order.Provider)
	}

	// 不带任何签名，只报订单号与成功状态——这曾经足以把订单改成已支付。
	forged := []byte(`{"orderNo":"` + order.OrderNo + `","status":"SUCCESS","tradeNo":"FAKE-1"}`)
	assertBillingError(t, env.service.HandleBillingCallback(PaymentChannelManual, forged, nil), http.StatusBadRequest, "")

	stored, err := env.store.BillingOrderByID(order.ID)
	if err != nil {
		t.Fatalf("回查订单失败: %v", err)
	}
	if stored.Status != OrderStatusPending || stored.PaidAt != nil {
		t.Fatalf("伪造回调不得改变订单状态: %+v", stored)
	}
	if count := countSubscriptionRows(t, env); count != 0 {
		t.Fatalf("伪造回调不得开通订阅，实际 %d 条", count)
	}

	// 后台补单这条受鉴权保护的正路仍然可用。
	if _, err := env.service.MarkBillingOrderPaid(order.ID, "线下到账"); err != nil {
		t.Fatalf("后台补单应仍然可用: %v", err)
	}
}

// TestBillingCallbackAmountMustMatch 覆盖回调金额校验与渠道一致性。
//
// 金额不是"大于 0 才比对"：报文里少一个 amountFen（字段名不匹配、网关省略零元位）
// 就会让比对短路，小额订单能被任意金额核销。
func TestBillingCallbackAmountMustMatch(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 9900, PeriodDays: 30, Enabled: true})
	channels := map[string]struct {
		secret string
		config map[string]string
	}{
		PaymentChannelAggregate: {secret: "s3cr3t", config: map[string]string{"appId": "app-1", "createUrl": "https://gw.example.com/pay"}},
		// 微信渠道的启用校验要商户号，密钥只用于这里的验签测试。
		PaymentChannelWechat: {secret: "wechat-secret", config: map[string]string{"mchId": "mch-1"}},
	}
	for channel, setup := range channels {
		config := map[string]string{"appSecret": setup.secret}
		for key, value := range setup.config {
			config[key] = value
		}
		if _, err := env.service.UpdatePaymentChannel(channel, PaymentChannelInput{Enabled: true, Config: config}, "admin-1"); err != nil {
			t.Fatalf("保存 %s 渠道失败: %v", channel, err)
		}
	}

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if order.Provider != PaymentChannelAggregate {
		t.Fatalf("默认渠道应为聚合网关，实际 %s", order.Provider)
	}

	// 缺金额：验签通过也不能核销。
	missingAmount := signedBillingCallback(t, map[string]string{"orderNo": order.OrderNo, "status": "SUCCESS"}, "s3cr3t")
	assertBillingError(t, env.service.HandleBillingCallback(PaymentChannelAggregate, missingAmount, nil), http.StatusBadRequest, "支付回调金额与订单不一致")

	// 金额不符：同样拒绝。
	wrongAmount := signedBillingCallback(t, map[string]string{"orderNo": order.OrderNo, "status": "SUCCESS", "amountFen": "1"}, "s3cr3t")
	assertBillingError(t, env.service.HandleBillingCallback(PaymentChannelAggregate, wrongAmount, nil), http.StatusBadRequest, "支付回调金额与订单不一致")

	// 渠道不符：用微信密钥签的回调不能核销聚合渠道的订单。
	crossChannel := signedBillingCallback(t, map[string]string{"orderNo": order.OrderNo, "status": "SUCCESS", "amountFen": "9900"}, "wechat-secret")
	assertBillingError(t, env.service.HandleBillingCallback(PaymentChannelWechat, crossChannel, nil), http.StatusBadRequest, "支付回调渠道与订单不一致")

	if stored, err := env.store.BillingOrderByID(order.ID); err != nil || stored.Status != OrderStatusPending {
		t.Fatalf("被拒的回调不得改变订单状态: %+v (%v)", stored, err)
	}

	// 金额与渠道都对时正常核销。
	ok := signedBillingCallback(t, map[string]string{"orderNo": order.OrderNo, "status": "SUCCESS", "amountFen": "9900", "tradeNo": "trade-9"}, "s3cr3t")
	if err := env.service.HandleBillingCallback(PaymentChannelAggregate, ok, nil); err != nil {
		t.Fatalf("合法回调应成功: %v", err)
	}
	stored, err := env.store.BillingOrderByID(order.ID)
	if err != nil {
		t.Fatalf("回查订单失败: %v", err)
	}
	if stored.Status != OrderStatusPaid || stored.ProviderOrderNo != "trade-9" {
		t.Fatalf("合法回调应置为已支付: %+v", stored)
	}
}

// TestBillingActivateSubscriptionPerOrderOnce 覆盖"同一订单只续期一次"。
//
// 渠道重试与客服连点会并发走到续期，读订阅 → 加一期没有排他性，两次都读到同一份旧
// 到期时间就会多送一期时长。同一订单重复调用必须只生效一次，不同订单仍应正常顺延。
func TestBillingActivateSubscriptionPerOrderOnce(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 9900, PeriodDays: 30, Enabled: true})
	storedPlan, err := env.store.BillingPlanByCode("pro-month")
	if err != nil {
		t.Fatalf("读取套餐失败: %v", err)
	}

	if _, err := env.store.ActivateSubscription("user-1", *storedPlan, "order-1"); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}
	first, err := env.store.ActivateSubscription("user-1", *storedPlan, "order-1")
	if err != nil {
		t.Fatalf("重复激活应幂等: %v", err)
	}
	if expected := env.clock.Now().AddDate(0, 0, 30); !first.ExpiresAt.Equal(expected) {
		t.Fatalf("同一订单重复激活不得顺延，期望 %v，实际 %v", expected, first.ExpiresAt)
	}
	if count := countSubscriptionRows(t, env); count != 1 {
		t.Fatalf("重复激活不得新建订阅，实际 %d 条", count)
	}

	// 换一笔订单：正常顺延一期。
	second, err := env.store.ActivateSubscription("user-1", *storedPlan, "order-2")
	if err != nil {
		t.Fatalf("第二笔订单续期失败: %v", err)
	}
	if expected := env.clock.Now().AddDate(0, 0, 60); !second.ExpiresAt.Equal(expected) {
		t.Fatalf("新订单应顺延一期，期望 %v，实际 %v", expected, second.ExpiresAt)
	}
}

// TestBillingRedeemCouponEnforcesPerUserLimit 覆盖"每人限领"在核销事务内的兜底。
//
// 试算阶段的限领判断与核销之间没有排他性，并发提交同一张限领券时两笔都能通过试算；
// 额度判断必须与 used_count 自增落在同一个事务里。
func TestBillingRedeemCouponEnforcesPerUserLimit(t *testing.T) {
	env := newBillingTestService(t)
	coupon := seedBillingCoupon(t, env, "ONEPER", 1000, 0, 10)
	coupon.PerUserLimit = 1

	if err := env.store.RedeemCoupon(coupon, "user-1", "order-1", 1000); err != nil {
		t.Fatalf("首次核销失败: %v", err)
	}
	assertBillingError(t, env.store.RedeemCoupon(coupon, "user-1", "order-2", 1000), http.StatusConflict, "")

	stored, err := env.store.BillingCouponByCode("ONEPER")
	if err != nil {
		t.Fatalf("读取券失败: %v", err)
	}
	if stored.UsedCount != 1 {
		t.Fatalf("超限核销不得消耗额度，实际 used_count=%d", stored.UsedCount)
	}
	// 别的账号不受影响。
	if err := env.store.RedeemCoupon(coupon, "user-2", "order-3", 1000); err != nil {
		t.Fatalf("其他账号核销失败: %v", err)
	}
}

func countSubscriptionRows(t *testing.T, env *billingTestEnv) int64 {
	t.Helper()
	var count int64
	if err := env.store.db.Table("billing_subscriptions").Count(&count).Error; err != nil {
		t.Fatalf("统计订阅失败: %v", err)
	}
	return count
}

// TestBillingExpireStaleOrdersReleasesCoupon 覆盖定时清理这条唯一会"自动"发生的路径。
//
// 用户放弃支付后没人会手动取消：订单必须自己关闭，且它占用的优惠券必须跟着归还，
// 否则券的额度会被一笔从未付款的订单永久吃掉。
func TestBillingExpireStaleOrdersReleasesCoupon(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "小明", "ming@example.com")
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 5000, PeriodDays: 30, Enabled: true})
	seedBillingCoupon(t, env, "TMOUT1000", 1000, 0, 10)

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "pro-month", CouponCode: "TMOUT1000"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	coupon, err := env.store.BillingCouponByCode("TMOUT1000")
	if err != nil {
		t.Fatalf("读取券失败: %v", err)
	}
	if coupon.UsedCount != 1 {
		t.Fatalf("下单后券应已核销，实际 %d", coupon.UsedCount)
	}

	// 时限内不该被清理。
	if closed, err := env.service.ExpireStaleBillingOrders(); err != nil || closed != 0 {
		t.Fatalf("时限内不应关闭任何订单: %d %v", closed, err)
	}

	env.clock.Advance(billingOrderTTL + time.Minute)
	closed, err := env.service.ExpireStaleBillingOrders()
	if err != nil {
		t.Fatalf("清理超时订单失败: %v", err)
	}
	if closed != 1 {
		t.Fatalf("应关闭 1 笔超时订单，实际 %d", closed)
	}
	stored, err := env.store.BillingOrderByID(order.ID)
	if err != nil {
		t.Fatalf("回查订单失败: %v", err)
	}
	if stored.Status != OrderStatusCanceled || stored.Remark != "支付超时自动关闭" {
		t.Fatalf("超时订单应被取消并写明原因: %+v", stored)
	}
	coupon, err = env.store.BillingCouponByCode("TMOUT1000")
	if err != nil {
		t.Fatalf("读取券失败: %v", err)
	}
	if coupon.UsedCount != 0 {
		t.Fatalf("超时关闭应归还券，实际 used_count=%d", coupon.UsedCount)
	}
	if rows := countRowsInTable(t, env, "billing_coupon_redemptions"); rows != 0 {
		t.Fatalf("超时关闭应清掉核销留痕，实际 %d", rows)
	}

	// 再跑一轮：已关闭的订单不会被重复处理。
	if closed, err := env.service.ExpireStaleBillingOrders(); err != nil || closed != 0 {
		t.Fatalf("重复清理不应再关闭订单: %d %v", closed, err)
	}
	// 券归还之后可以被另一个账号再次使用。
	if err := env.store.RedeemCoupon(*coupon, "user-2", "order-9", 1000); err != nil {
		t.Fatalf("归还后的券应可再次使用: %v", err)
	}
}

func countRowsInTable(t *testing.T, env *billingTestEnv, table string) int64 {
	t.Helper()
	var count int64
	if err := env.store.db.Table(table).Count(&count).Error; err != nil {
		t.Fatalf("统计 %s 失败: %v", table, err)
	}
	return count
}
