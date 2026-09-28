package auth

import (
	"errors"
	"log"
	"strings"
	"time"
)

// 订单服务层：下单、试算、查询、取消、补单、退款。
//
// 这一层只做编排（定价、状态机、幂等），真正的读写与事务边界在 billing_store.go，
// 支付渠道的适配与验签在 billing_payment.go。分成三处是因为订单状态机的改动频率
// 远低于接一个新支付渠道，混在一起会让每次接渠道都要重新理解一遍订单流转。

// billingOrderTTL 是待支付订单的存活时长。
//
// 30 分钟是一个折中：太短会让用户在收银台慢慢填资料时订单先失效；太长则后台的
// "待处理"列表会被大量实际已放弃的订单淹没，也让掉单难以识别。
const billingOrderTTL = 30 * time.Minute

// billingExpireBatch 限制单次清理的订单数：定时任务会周期执行，一次抓太多只会让
// 这一轮变慢，下一轮接着处理剩下的。
const billingExpireBatch = 200

// billingQuote 是"这一单到底收多少钱"的一次性结算结果。
type billingQuote struct {
	plan        BillingPlan
	amountFen   int64
	discountFen int64
	payableFen  int64
	coupon      *BillingCoupon
	couponError string
}

// quoteBillingOrder 是试算与下单共用的定价逻辑。
//
// 必须共用：试算走一套、下单走另一套，就会出现"页面显示 8 折、实际按原价扣款"
// 这类只能靠客诉发现的偏差。券不可用时不返回错误，而是把原因留在 couponError——
// 前台要的是"就地提示这张券不能用"，而不是整单失败。
func (s *Service) quoteBillingOrder(userID string, input BillingOrderInput) (*billingQuote, error) {
	code := strings.TrimSpace(input.PlanCode)
	if code == "" {
		return nil, invalidArgument("请选择套餐")
	}
	plan, err := s.store.BillingPlanByCode(code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, invalidArgument("套餐不存在或已停用")
		}
		return nil, internalFailure(err)
	}
	if plan == nil || !plan.Enabled {
		return nil, invalidArgument("套餐不存在或已停用")
	}
	quote := &billingQuote{
		plan:       *plan,
		amountFen:  plan.PriceFen,
		payableFen: plan.PriceFen,
	}
	couponCode := NormalizeBillingCouponCode(input.CouponCode)
	if couponCode == "" {
		return quote, nil
	}
	coupon, discount, err := s.store.QuoteBillingCoupon(userID, couponCode, quote.amountFen)
	if err != nil {
		// 业务性失败（不存在/过期/领完/未达门槛）不阻断整单，原因交给前台展示；
		// 只有真正的存储故障才升级为 500。
		var business *Error
		if errors.As(err, &business) {
			quote.couponError = business.Message
			return quote, nil
		}
		return nil, internalFailure(err)
	}
	if coupon != nil {
		quote.coupon = coupon
		quote.discountFen = discount
		quote.payableFen = quote.amountFen - discount
		if quote.payableFen < 0 {
			quote.payableFen = 0
		}
	}
	return quote, nil
}

// QuoteBillingOrder 做结算试算：算金额、算抵扣，并把券不可用的原因带回来。
func (s *Service) QuoteBillingOrder(userID string, input BillingOrderInput) (*BillingCouponQuoteView, error) {
	quote, err := s.quoteBillingOrder(strings.TrimSpace(userID), input)
	if err != nil {
		return nil, err
	}
	view := &BillingCouponQuoteView{
		AmountFen:   quote.amountFen,
		DiscountFen: quote.discountFen,
		PayableFen:  quote.payableFen,
		CouponError: quote.couponError,
	}
	if quote.coupon != nil {
		view.CouponCode = quote.coupon.Code
	}
	return view, nil
}

// CreateBillingOrder 建一笔待支付订单，并在券可用时核销。
//
// 先核销、后落单：核销失败时订单根本不会出现，杜绝"券没扣掉但订单已生成"。
// 两者本应在同一个事务里，但写层的两个方法各自带事务；先核销 + 落单失败时补偿
// 释放，即可得到等价的可见性保证。核销成功但落单失败属于极罕见情况，补偿一次即可。
func (s *Service) CreateBillingOrder(userID string, input BillingOrderInput) (*BillingOrderView, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	quote, err := s.quoteBillingOrder(uid, input)
	if err != nil {
		return nil, err
	}

	now := s.now()
	orderNo, err := NewBillingOrderNo(now)
	if err != nil {
		return nil, internalFailure(err)
	}
	orderID, err := newBillingID()
	if err != nil {
		return nil, internalFailure(err)
	}

	order := &BillingOrder{
		ID:          orderID,
		OrderNo:     orderNo,
		UserID:      uid,
		PlanID:      quote.plan.ID,
		PlanCode:    quote.plan.Code,
		PlanName:    quote.plan.Name,
		AmountFen:   quote.amountFen,
		DiscountFen: quote.discountFen,
		PayableFen:  quote.payableFen,
		Status:      OrderStatusPending,
		// Provider 是下单时"当前启用的渠道"的快照：下单之后运营换了渠道，
		// 这笔单子仍然应该走当时选定的渠道，否则会付错地方。
		Provider:  s.defaultPaymentChannel(),
		ExpiresAt: now.Add(billingOrderTTL),
	}

	if quote.coupon != nil {
		if err := s.store.RedeemCoupon(*quote.coupon, uid, orderID, quote.discountFen); err != nil {
			return nil, billingBusinessError(err)
		}
		order.CouponID = quote.coupon.ID
		order.CouponCode = quote.coupon.Code
	}
	if err := s.store.CreateBillingOrder(order); err != nil {
		if quote.coupon != nil {
			// 补偿：订单没落库，把券还回去。补偿本身失败只记日志——下单已经失败，
			// 在这里再抛错只会掩盖真正的原因，券的余量可由运营人工修正。
			if releaseErr := s.store.ReleaseCouponByOrder(orderID); releaseErr != nil {
				log.Printf("auth: 下单失败后回滚优惠券（订单 %s）失败: %v", orderID, releaseErr)
			}
		}
		return nil, internalFailure(err)
	}
	return s.billingOrderView(*order), nil
}

// UserBillingOrders 返回某个账号自己的订单，按时间倒序。
func (s *Service) UserBillingOrders(userID string, page int, pageSize int) (*BillingOrderPageView, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	page, pageSize = normalizeBillingPage(page, pageSize)
	orders, total, err := s.store.BillingOrders(BillingOrderFilter{UserID: uid, Page: page, PageSize: pageSize})
	if err != nil {
		return nil, internalFailure(err)
	}
	return &BillingOrderPageView{
		Orders:   s.billingOrderViews(orders),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// AdminBillingOrders 返回后台的全量订单，并附带 30 天经营读数。
func (s *Service) AdminBillingOrders(filter BillingOrderFilter) (*BillingOrderPageView, error) {
	page, pageSize := normalizeBillingPage(filter.Page, filter.PageSize)
	filter.Page, filter.PageSize = page, pageSize
	orders, total, err := s.store.BillingOrders(filter)
	if err != nil {
		return nil, internalFailure(err)
	}
	// 30 天而非自然月：自然月会让月初的读数骤降到接近零，运营看着像出了故障。
	revenue, err := s.store.BillingRevenue(s.now().AddDate(0, 0, -30))
	if err != nil {
		return nil, internalFailure(err)
	}
	return &BillingOrderPageView{
		Orders:   s.billingOrderViews(orders),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Revenue:  &revenue,
	}, nil
}

// BillingOrderForUser 读取某个账号名下的订单。
//
// 不属于该账号时与"订单不存在"返回同样的 forbidden：区分二者等于给攻击者一个
// 订单号枚举器，能逐个确认哪些订单真实存在。
func (s *Service) BillingOrderForUser(userID string, orderID string) (*BillingOrderView, error) {
	order, err := s.loadOwnedOrder(userID, orderID)
	if err != nil {
		return nil, err
	}
	return s.billingOrderView(*order), nil
}

// CancelBillingOrder 取消一笔待支付订单并回滚其占用的优惠券。
func (s *Service) CancelBillingOrder(userID string, orderID string, reason string) (*BillingOrderView, error) {
	order, err := s.loadOwnedOrder(userID, orderID)
	if err != nil {
		return nil, err
	}
	switch order.Status {
	case OrderStatusPending:
		// 可取消。
	case OrderStatusPaid:
		return nil, invalidArgument("订单已支付，请通过退款流程处理")
	case OrderStatusCanceled:
		return nil, conflict("订单已取消")
	case OrderStatusRefunded:
		return nil, conflict("订单已退款")
	default:
		return nil, conflict("订单当前状态不可取消")
	}
	// 先回滚再改状态：回滚按订单精确释放且幂等（没有核销记录时是空操作），
	// 即使随后写状态失败，重试也不会把券重复还回去。
	if err := s.store.ReleaseCouponByOrder(order.ID); err != nil {
		return nil, internalFailure(err)
	}
	order.Status = OrderStatusCanceled
	order.Remark = billingRemarkOr(reason, "订单已取消")
	if err := s.store.SaveBillingOrder(order); err != nil {
		return nil, internalFailure(err)
	}
	return s.billingOrderView(*order), nil
}

// ExpireStaleBillingOrders 关闭超时未支付的订单，并归还它们占用的优惠券。
//
// 唯一的调用方是托管侧的定时任务：用户放弃支付后没有人会手动取消，不关闭的话后台的
// "待支付"读数会一直虚高，真正的掉单也看不出来；而订单占用的优惠券只在取消/退款时按
// 订单释放，少做这一步，券的额度就被永远吃掉。
func (s *Service) ExpireStaleBillingOrders() (int, error) {
	orders, err := s.store.CloseStaleOrders(s.now(), billingExpireBatch)
	for _, order := range orders {
		if strings.TrimSpace(order.CouponID) == "" {
			continue
		}
		// 归还失败只记日志不中断：订单已经关闭，下一轮定时任务会再试，
		// 而 ReleaseCouponByOrder 按订单释放本身是幂等的。
		if releaseErr := s.store.ReleaseCouponByOrder(order.ID); releaseErr != nil {
			log.Printf("auth: 超时关闭订单 %s 后归还优惠券失败: %v", order.OrderNo, releaseErr)
		}
	}
	if err != nil {
		return len(orders), internalFailure(err)
	}
	return len(orders), nil
}

// MarkBillingOrderPaid 是后台手工补单入口。
//
// 幂等是硬要求：客服重复点一次按钮，续期（顺延）会多送一期时长，是真金白银的损失。
func (s *Service) MarkBillingOrderPaid(orderID string, remark string) (*BillingOrderView, error) {
	order, err := s.store.BillingOrderByID(orderID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notFound("订单不存在")
		}
		return nil, internalFailure(err)
	}
	return s.markBillingOrderPaid(order, remark)
}

// markBillingOrderPaid 是"把订单置为已支付"的唯一实现，手工补单与支付回调共用。
//
// 已 PAID 直接返回原订单；已 REFUNDED 拒绝补单；已取消（支付超时）允许补单——
// 这正是"用户其实付了、订单却超时关闭"的掉单场景，手工补单的价值就在这里。
// 置为已支付必须连带激活订阅，两者逻辑只此一份，否则回调与补单会算出两个到期时间。
func (s *Service) markBillingOrderPaid(order *BillingOrder, remark string) (*BillingOrderView, error) {
	if order.Status == OrderStatusPaid {
		return s.billingOrderView(*order), nil
	}
	if order.Status == OrderStatusRefunded {
		return nil, invalidArgument("订单已退款，不能补单")
	}

	plan, planErr := s.store.BillingPlanByID(order.PlanID)
	if planErr != nil || plan == nil {
		// PlanID 查不到时按 PlanCode 兜底：套餐被删后重建会换 ID，历史订单仍应能补单。
		plan, planErr = s.store.BillingPlanByCode(order.PlanCode)
	}
	if planErr != nil || plan == nil {
		if planErr == nil {
			planErr = errors.New("auth: 订单对应的套餐不存在")
		}
		return nil, internalFailure(planErr)
	}

	// 先续期、后落状态：只要订单变成 PAID，就必然已经激活订阅。反过来（先落状态、
	// 续期失败）会留下"已付款但没生效"、且因幂等再也补不上的死局；先续期最坏是落
	// 状态失败留下 PENDING 订单，重试可能多续一期，属于可人工对账的偏差，
	// 比"用户付了钱拿不到服务"轻得多。
	if _, err := s.store.ActivateSubscription(order.UserID, *plan, order.ID); err != nil {
		return nil, internalFailure(err)
	}

	now := s.now()
	order.Status = OrderStatusPaid
	order.PaidAt = &now
	order.Remark = billingRemarkOr(remark, "手工补单")
	if err := s.store.SaveBillingOrder(order); err != nil {
		return nil, internalFailure(err)
	}
	return s.billingOrderView(*order), nil
}

// RefundBillingOrder 对已支付订单退款并回滚其优惠券。
//
// 订阅不回滚：时长一旦消费就无法精确撤销（用户可能已经用掉配额），强行截断会把
// "已付费用户"变成"欠费用户"，引发更多客诉。退款只记在订单上，是否停用账号由运营
// 按实际情况决定。这是刻意的取舍，不是遗漏。
func (s *Service) RefundBillingOrder(orderID string, reason string) (*BillingOrderView, error) {
	order, err := s.store.BillingOrderByID(orderID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notFound("订单不存在")
		}
		return nil, internalFailure(err)
	}
	if order.Status != OrderStatusPaid {
		return nil, invalidArgument("只有已支付订单可以退款")
	}
	if err := s.store.ReleaseCouponByOrder(order.ID); err != nil {
		return nil, internalFailure(err)
	}
	order.Status = OrderStatusRefunded
	order.Remark = billingRemarkOr(reason, "订单已退款")
	if err := s.store.SaveBillingOrder(order); err != nil {
		return nil, internalFailure(err)
	}
	return s.billingOrderView(*order), nil
}

// ---------- 订单视图的内部工具 ----------

// loadOwnedOrder 读取订单并校验归属；越权与不存在返回同一文案。
func (s *Service) loadOwnedOrder(userID string, orderID string) (*BillingOrder, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	order, err := s.store.BillingOrderByID(orderID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, forbidden("订单不存在")
		}
		return nil, internalFailure(err)
	}
	if order.UserID != uid {
		return nil, forbidden("订单不存在")
	}
	return order, nil
}

// billingOrderView 拼装单条订单视图。账号查不到时留空，不报错。
func (s *Service) billingOrderView(order BillingOrder) *BillingOrderView {
	view := BillingOrderViewOfUser(order, s.billingOrderUser(order.UserID, nil))
	return &view
}

// billingOrderViews 批量拼装，并用缓存避免同一账号在一次列表里被查多次。
func (s *Service) billingOrderViews(orders []BillingOrder) []BillingOrderView {
	views := make([]BillingOrderView, 0, len(orders))
	cache := map[string]*User{}
	for _, order := range orders {
		views = append(views, BillingOrderViewOfUser(order, s.billingOrderUser(order.UserID, cache)))
	}
	return views
}

// billingOrderUser 查账号并写入缓存；账号缺失或查询失败都返回 nil，由视图留空。
func (s *Service) billingOrderUser(userID string, cache map[string]*User) *User {
	if cache != nil {
		if user, ok := cache[userID]; ok {
			return user
		}
	}
	user, err := s.store.UserByID(userID)
	if err != nil {
		user = nil
	}
	if cache != nil {
		cache[userID] = user
	}
	return user
}

// normalizeBillingPage 与 Store 的分页口径保持一致，让回显的 page/pageSize 就是实际生效的值。
func normalizeBillingPage(page int, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	return page, pageSize
}

// billingRemarkOr 返回去空白后的备注，为空时用兜底文案。
func billingRemarkOr(remark string, fallback string) string {
	if trimmed := strings.TrimSpace(remark); trimmed != "" {
		return trimmed
	}
	return fallback
}

// billingBusinessError 把写层返回的模块错误原样透出，其余（存储故障）升级为 500。
func billingBusinessError(err error) error {
	var business *Error
	if errors.As(err, &business) {
		return business
	}
	return internalFailure(err)
}

// defaultPaymentChannel 选择下单时默认使用的渠道。
//
// 取"当前启用的第一个真实渠道"，没有则回落到手工渠道。手工渠道永远存在，
// 因此这里绝不会返回空字符串——订单的 Provider 不允许为空，否则回调无处验签。
func (s *Service) defaultPaymentChannel() string {
	for _, channel := range []string{PaymentChannelAggregate, PaymentChannelWechat, PaymentChannelAlipay} {
		record, err := s.store.PaymentConfig(channel)
		if err != nil || record == nil || !record.Enabled {
			continue
		}
		return channel
	}
	return PaymentChannelManual
}
