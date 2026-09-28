package hosted

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestHostedManualCallbackCannotSettleOrders 盯住公网回调的准入边界。
//
// /api/payments/callback/:channel 是整组计费接口里唯一的匿名入口，它的准入只有验签。
// 手工渠道没有外部网关、没有可验签的报文，一旦被放行，任何注册用户拿自己的订单号
// POST 一次就能零元开通套餐——这条用例从 HTTP 层把住这道门。
func TestHostedManualCallbackCannotSettleOrders(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "billing-guard-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	userCookie, _ := registerAccount(t, router, authDB, "billing-guard-user@example.com")

	if recorder := perform(router, http.MethodPost, "/api/admin/plans",
		`{"code":"guard-month","name":"守门套餐","priceFen":9900,"periodDays":30,"enabled":true,"quotaCalls":10,"quotaStorageMb":10,"quotaMembers":1}`,
		adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("建套餐失败：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder := perform(router, http.MethodPost, "/api/admin/coupons",
		`{"code":"GUARD1000","name":"守门券","kind":"AMOUNT","value":1000,"minAmountFen":0,"totalQuota":10,"perUserLimit":1,"startsAt":"2026-01-01T00:00:00Z","expiresAt":"2099-01-01T00:00:00Z","enabled":true}`,
		adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("建券失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var couponCreated struct {
		Data struct {
			Coupon struct {
				ID string `json:"id"`
			} `json:"coupon"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &couponCreated); err != nil || couponCreated.Data.Coupon.ID == "" {
		t.Fatalf("解析建券响应失败: %v %s", err, recorder.Body.String())
	}

	recorder = perform(router, http.MethodPost, "/api/payments/orders", `{"planCode":"guard-month","couponCode":"GUARD1000"}`, userCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("下单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Data struct {
			Order struct {
				ID       string `json:"id"`
				OrderNo  string `json:"orderNo"`
				Status   string `json:"status"`
				Provider string `json:"provider"`
			} `json:"order"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析下单响应失败: %v %s", err, recorder.Body.String())
	}
	order := created.Data.Order
	if order.Provider != "MANUAL" {
		t.Fatalf("未配置在线渠道时应付费渠道为 MANUAL，实际 %q", order.Provider)
	}

	// 匿名伪造回调：不接受、不生效。
	recorder = perform(router, http.MethodPost, "/api/payments/callback/MANUAL",
		fmt.Sprintf(`{"orderNo":%q,"status":"SUCCESS","tradeNo":"FAKE-1"}`, order.OrderNo), nil)
	if recorder.Code == http.StatusOK {
		t.Fatalf("匿名 MANUAL 回调必须被拒，却返回 200：%s", recorder.Body.String())
	}

	recorder = perform(router, http.MethodGet, "/api/payments/orders/"+order.ID, "", userCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取订单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var detail struct {
		Data struct {
			Order struct {
				Status string `json:"status"`
				PaidAt string `json:"paidAt"`
			} `json:"order"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("解析订单详情失败: %v %s", err, recorder.Body.String())
	}
	if detail.Data.Order.Status != "PENDING" || detail.Data.Order.PaidAt != "" {
		t.Fatalf("伪造回调不得改变订单状态：%s", recorder.Body.String())
	}
	if rows := countRows(t, authDB, "billing_subscriptions"); rows != 0 {
		t.Fatalf("伪造回调不得开通订阅，实际 %d 条", rows)
	}

	// 核销记录接口的字段名就是前端契约：漏 json tag 会退化成 Go 字段名，
	// 客服页面会静默显示空的账号/订单与 ¥0.00 抵扣额。
	recorder = perform(router, http.MethodGet, "/api/admin/coupons/"+couponCreated.Data.Coupon.ID+"/redemptions", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取核销记录失败：%d %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, key := range []string{`"userId"`, `"orderId"`, `"discountFen"`, `"redeemedAt"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("核销记录缺少字段 %s：%s", key, body)
		}
	}
	if strings.Contains(body, `"DiscountFen"`) || strings.Contains(body, `"UserID"`) {
		t.Fatalf("核销记录不得回传 Go 字段名：%s", body)
	}
}

// TestHostedBillingFlow 覆盖计费域的真实链路：后台建套餐/发券 → 用户试算下单 →
// 手工补单 → 权益生效 → 退款回滚券。
//
// 走 HTTP 而不是只测服务层：路由挂载、管理员守卫、响应信封的字段名都是这里才暴露的
// 契约，前端读的是这一层。回调那条匿名路径也只在中间件串联之后才看得出来。
func TestHostedBillingFlow(t *testing.T) {
	extension, router, authDB, canvasDB := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "billing-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	userCookie, _ := registerAccount(t, router, authDB, "billing-user@example.com")

	// 未登录不能看货架：套餐价格是商业信息，也不该让匿名请求打到账号库。
	if recorder := perform(router, http.MethodGet, "/api/finance/plans", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("匿名读取套餐应 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 普通账号不能建套餐。
	if recorder := perform(router, http.MethodPost, "/api/admin/plans", `{}`, userCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号建套餐应 403，实际 %d", recorder.Code)
	}

	recorder := perform(router, http.MethodPost, "/api/admin/plans",
		`{"code":"pro-month","name":"专业版","description":"演示套餐","sortOrder":1,"enabled":true,"priceFen":9900,"periodDays":30,"quotaCalls":1000,"quotaStorageMb":20480,"quotaMembers":3}`,
		adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("建套餐失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 8 折券：9900 分应收 7920 分。
	recorder = perform(router, http.MethodPost, "/api/admin/coupons",
		`{"code":"KINO80","name":"八折券","kind":"PERCENT","value":8000,"minAmountFen":0,"totalQuota":10,"perUserLimit":1,"startsAt":"2026-01-01T00:00:00Z","expiresAt":"2099-01-01T00:00:00Z","enabled":true}`,
		adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("建优惠券失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 试算：金额、抵扣、实付三处都要对。
	recorder = perform(router, http.MethodPost, "/api/finance/coupons/quote",
		`{"planCode":"pro-month","couponCode":"kino80"}`, userCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("试算失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var quote struct {
		Data struct {
			Quote struct {
				AmountFen   int64  `json:"amountFen"`
				DiscountFen int64  `json:"discountFen"`
				PayableFen  int64  `json:"payableFen"`
				CouponCode  string `json:"couponCode"`
				CouponError string `json:"couponError"`
			} `json:"quote"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &quote); err != nil {
		t.Fatalf("解析试算响应失败: %v %s", err, recorder.Body.String())
	}
	if quote.Data.Quote.PayableFen != 7920 || quote.Data.Quote.DiscountFen != 1980 || quote.Data.Quote.CouponError != "" {
		t.Fatalf("试算结果不符：%s", recorder.Body.String())
	}
	// 券码大小写不敏感是给用户手输留的容错，响应里应回大写规范码。
	if quote.Data.Quote.CouponCode != "KINO80" {
		t.Fatalf("试算应回显规范化券码，实际 %q", quote.Data.Quote.CouponCode)
	}

	// 下单：实付金额落库、状态待支付。
	recorder = perform(router, http.MethodPost, "/api/payments/orders", `{"planCode":"pro-month","couponCode":"KINO80"}`, userCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("下单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Data struct {
			Order struct {
				ID         string `json:"id"`
				OrderNo    string `json:"orderNo"`
				Status     string `json:"status"`
				PayableFen int64  `json:"payableFen"`
				Provider   string `json:"provider"`
			} `json:"order"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析下单响应失败: %v %s", err, recorder.Body.String())
	}
	order := created.Data.Order
	if order.ID == "" || order.OrderNo == "" || order.Status != "PENDING" || order.PayableFen != 7920 {
		t.Fatalf("下单结果不符：%s", recorder.Body.String())
	}
	// 未配置任何在线渠道时应回落到手工渠道，而不是留下空 Provider（回调将无处验签）。
	if order.Provider != "MANUAL" {
		t.Fatalf("默认渠道应为 MANUAL，实际 %q", order.Provider)
	}

	// 券已核销一次。
	var usedCount int
	if err := authDB.Table("billing_coupons").Where("code = ?", "KINO80").Pluck("used_count", &usedCount).Error; err != nil {
		t.Fatalf("读取券使用数失败: %v", err)
	}
	if usedCount != 1 {
		t.Fatalf("下单后券应核销 1 次，实际 %d", usedCount)
	}

	// 越权：管理员自己的会话读用户订单，必须拿不到（同文案的 403）。
	if recorder := perform(router, http.MethodGet, "/api/payments/orders/"+order.ID, "", adminCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("越权读订单应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 回调是匿名接口：未配置渠道时应是 4xx（验签/配置错误），绝不能是 401 ——
	// 401 说明请求根本没进到验签，渠道会永远收不到"已支付"。
	if recorder := perform(router, http.MethodPost, "/api/payments/callback/AGGREGATE", `{"orderNo":"x","status":"SUCCESS"}`, nil); recorder.Code == http.StatusUnauthorized {
		t.Fatalf("支付回调不应被会话中间件拦截：%s", recorder.Body.String())
	}

	// 手工补单：置为已支付并激活订阅。
	recorder = perform(router, http.MethodPost, "/api/admin/orders/"+order.ID+"/mark-paid", `{"remark":"线下已到账"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("补单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var marked struct {
		Data struct {
			Order struct {
				Status string `json:"status"`
				Remark string `json:"remark"`
			} `json:"order"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &marked); err != nil {
		t.Fatalf("解析补单响应失败: %v %s", err, recorder.Body.String())
	}
	if marked.Data.Order.Status != "PAID" {
		t.Fatalf("补单后状态应为 PAID：%s", recorder.Body.String())
	}

	// 重复补单不得再顺延一期：这是真金白银的幂等要求。
	var subscriptionCount int64
	if err := authDB.Table("billing_subscriptions").Count(&subscriptionCount).Error; err != nil {
		t.Fatalf("读取订阅失败: %v", err)
	}
	firstExpiry := currentSubscriptionExpiry(t, authDB)
	if recorder := perform(router, http.MethodPost, "/api/admin/orders/"+order.ID+"/mark-paid", `{}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("重复补单应幂等成功，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if again := currentSubscriptionExpiry(t, authDB); again != firstExpiry {
		t.Fatalf("重复补单不得续期：%s → %s", firstExpiry, again)
	}
	if count := countRows(t, authDB, "billing_subscriptions"); count != subscriptionCount {
		t.Fatalf("重复补单不得新建订阅：%d → %d", subscriptionCount, count)
	}

	// 权益已生效，且带着套餐配额。
	recorder = perform(router, http.MethodGet, "/api/finance/entitlements", "", userCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取权益失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var entitlements struct {
		Data struct {
			Entitlements struct {
				Active       bool   `json:"active"`
				PlanCode     string `json:"planCode"`
				PlanName     string `json:"planName"`
				ExpiresAt    string `json:"expiresAt"`
				QuotaCalls   int64  `json:"quotaCalls"`
				QuotaMembers int    `json:"quotaMembers"`
			} `json:"entitlements"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &entitlements); err != nil {
		t.Fatalf("解析权益响应失败: %v %s", err, recorder.Body.String())
	}
	if !entitlements.Data.Entitlements.Active || entitlements.Data.Entitlements.PlanName != "专业版" ||
		entitlements.Data.Entitlements.ExpiresAt == "" || entitlements.Data.Entitlements.QuotaMembers != 3 {
		t.Fatalf("权益内容不符：%s", recorder.Body.String())
	}

	// 后台营收读数必须反映这笔 7920（曾经因为汇总实现缺陷长期为零）。
	recorder = perform(router, http.MethodGet, "/api/admin/orders", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("订单列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Data struct {
			Total   int64 `json:"total"`
			Revenue struct {
				PaidOrders    int64 `json:"paidOrders"`
				PaidAmountFen int64 `json:"paidAmountFen"`
				PendingOrders int64 `json:"pendingOrders"`
			} `json:"revenue"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析订单列表失败: %v %s", err, recorder.Body.String())
	}
	if page.Data.Total != 1 || page.Data.Revenue.PaidOrders != 1 || page.Data.Revenue.PaidAmountFen != 7920 {
		t.Fatalf("营收读数不符：%s", recorder.Body.String())
	}

	// 退款：券要按订单回滚，核销留痕同时清掉。
	if recorder := perform(router, http.MethodPost, "/api/admin/orders/"+order.ID+"/refund", `{"reason":"用户申请退款"}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("退款失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if err := authDB.Table("billing_coupons").Where("code = ?", "KINO80").Pluck("used_count", &usedCount).Error; err != nil {
		t.Fatalf("读取券使用数失败: %v", err)
	}
	if usedCount != 0 {
		t.Fatalf("退款后券应回滚为 0，实际 %d", usedCount)
	}
	if rows := countRows(t, authDB, "billing_coupon_redemptions"); rows != 0 {
		t.Fatalf("退款后核销留痕应清空，实际 %d", rows)
	}

	// 关键写操作必须留痕。
	for _, action := range []string{"plan.create", "coupon.create", "order.mark-paid", "order.refund"} {
		var count int64
		// 审计落在画布库：账号库只存身份，运营留痕与画布数据同库同析。
		if err := canvasDB.Table("admin_audit_events").Where("action = ?", action).Count(&count).Error; err != nil {
			t.Fatalf("读取审计失败: %v", err)
		}
		if count == 0 {
			t.Fatalf("缺少审计记录：%s", action)
		}
	}
}

func currentSubscriptionExpiry(t *testing.T, authDB *gorm.DB) time.Time {
	t.Helper()
	var rows []struct {
		ExpiresAt time.Time `gorm:"column:expires_at"`
	}
	if err := authDB.Table("billing_subscriptions").Select("expires_at").Scan(&rows).Error; err != nil {
		t.Fatalf("读取订阅到期时间失败: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("没有任何订阅记录")
	}
	return rows[0].ExpiresAt
}

func countRows(t *testing.T, db *gorm.DB, table string) int64 {
	t.Helper()
	var count int64
	if err := db.Table(table).Count(&count).Error; err != nil {
		t.Fatalf("统计 %s 失败: %v", table, err)
	}
	return count
}
