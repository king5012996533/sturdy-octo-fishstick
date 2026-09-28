package auth

import (
	"errors"
	"regexp"
	"strings"
)

// 优惠券服务层。
//
// 这里只做「后台能改什么、前台能看到什么」的编排。试算与核销的底座早已沉淀在
// billing_pricing.go 与 billing_store.go 的 QuoteBillingCoupon/RedeemCoupon 里：
// 折扣怎么算、额度怎么占用必须与下单共用同一段代码，所以本文件刻意不再复制一份，
// 否则「试算 8 折、下单原价」这类客诉会重新出现。

// billingCouponCodePattern 是券码的合法形态。
//
// 以字母或数字开头、长度 3-32、只允许大写字母/数字/中划线：券码要能被用户照着输入，
// 出现下划线、空格或中文都会让「明明发过券却兑不了」变成客服工单。校验前统一走
// NormalizeBillingCouponCode，所以小写与首尾空格在这里已经被抹平，正则只需认大写形态。
var billingCouponCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,31}$`)

// AdminBillingCoupons 返回后台的优惠券列表。
//
// 含停用与过期：运营需要看见历史券的全貌，「还能不能用」由 Enabled 与时间字段表达，
// 而不是从列表里直接藏起来。排序沿用 store 的 created_at 倒序，最新建的排在最前。
func (s *Service) AdminBillingCoupons() ([]BillingCouponView, error) {
	coupons, err := s.store.BillingCoupons(false)
	if err != nil {
		return nil, internalFailure(err)
	}
	views := make([]BillingCouponView, 0, len(coupons))
	for _, coupon := range coupons {
		views = append(views, BillingCouponViewOf(coupon))
	}
	return views, nil
}

// SaveBillingCoupon 新建或更新一张优惠券，ID 为空表示新建。
//
// 校验失败一律返回中文可读原因：这些文案会直接出现在后台表单下方，让运营知道改哪一格，
// 而不是收到一句「参数错误」。
func (s *Service) SaveBillingCoupon(input BillingCouponInput) (*BillingCouponView, error) {
	code := NormalizeBillingCouponCode(input.Code)
	if !billingCouponCodePattern.MatchString(code) {
		return nil, invalidArgument("券码需为 3-32 位大写字母、数字或中划线，且以字母或数字开头")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, invalidArgument("请填写优惠券名称")
	}
	if len([]rune(name)) > 40 {
		return nil, invalidArgument("优惠券名称最多 40 个字符")
	}
	kind := strings.ToUpper(strings.TrimSpace(input.Kind))
	switch kind {
	case CouponKindAmount, CouponKindPercent:
	default:
		return nil, invalidArgument("优惠券类型只能是 AMOUNT 或 PERCENT")
	}
	if input.Value < 0 {
		return nil, invalidArgument("优惠券面额不能为负数")
	}
	switch kind {
	case CouponKindAmount:
		// 面额为 0 的满减券等于「立减 0 元」，只会让用户白高兴一场。
		if input.Value < 1 {
			return nil, invalidArgument("满减券面额至少为 1 分")
		}
	case CouponKindPercent:
		// 10000 表示不打折，收进来就是一张「看起来能省、实际一分不减」的坏券；
		// 值为 0 则是全额抵扣，对平台是资损，都必须挡在保存之外。
		if input.Value <= 0 || input.Value >= 10000 {
			return nil, invalidArgument("折扣券折扣率需在 0 到 10000 之间，且不能是 10000（零折扣）")
		}
	}
	if input.MinAmountFen < 0 {
		return nil, invalidArgument("使用门槛金额不能为负数")
	}
	if input.TotalQuota < 0 {
		return nil, invalidArgument("发放总量不能为负数")
	}
	if input.PerUserLimit < 0 {
		return nil, invalidArgument("每人限领数量不能为负数")
	}
	if !input.ExpiresAt.After(input.StartsAt) {
		return nil, invalidArgument("结束时间必须晚于开始时间")
	}

	// 券码查重：BillingCouponByCode 内部已统一大小写，命中且不是「自己改自己」才算冲突。
	existing, err := s.store.BillingCouponByCode(code)
	switch {
	case err == nil:
		if existing.ID != strings.TrimSpace(input.ID) {
			return nil, conflict("券码已被占用")
		}
	case errors.Is(err, ErrNotFound):
	default:
		return nil, internalFailure(err)
	}

	coupon := BillingCoupon{
		ID:           strings.TrimSpace(input.ID),
		Code:         code,
		Name:         name,
		Kind:         kind,
		Value:        input.Value,
		MinAmountFen: input.MinAmountFen,
		TotalQuota:   input.TotalQuota,
		PerUserLimit: input.PerUserLimit,
		StartsAt:     input.StartsAt,
		ExpiresAt:    input.ExpiresAt,
		Enabled:      input.Enabled,
	}
	if coupon.ID != "" {
		current, err := s.store.BillingCouponByID(coupon.ID)
		if errors.Is(err, ErrNotFound) {
			return nil, notFound("优惠券不存在")
		}
		if err != nil {
			return nil, internalFailure(err)
		}
		// UsedCount 只读回显、绝不接受外部赋值：它是「实际用了多少张」的对账依据，
		// 只能由 RedeemCoupon 在核销事务里自增，后台接口覆盖写入会让发放数与核销数脱节。
		// 输入结构体里没有这个字段，这里的取值也让返回视图与库中真实计数保持一致。
		coupon.UsedCount = current.UsedCount
		coupon.CreatedAt = current.CreatedAt
	}
	if err := s.store.SaveBillingCoupon(&coupon); err != nil {
		return nil, internalFailure(err)
	}
	view := BillingCouponViewOf(coupon)
	return &view, nil
}

// DeleteBillingCoupon 物理删除一张没有任何核销记录的券。
func (s *Service) DeleteBillingCoupon(id string) error {
	target := strings.TrimSpace(id)
	if target == "" {
		return invalidArgument("请选择要删除的优惠券")
	}
	if _, err := s.store.BillingCouponByID(target); err != nil {
		if errors.Is(err, ErrNotFound) {
			return notFound("优惠券不存在")
		}
		return internalFailure(err)
	}
	// 有核销记录就不许删：流水按 coupon_id 关联，券被物理删除后这些记录会变成指向
	// 不存在券的孤儿——客服既查不到券码，也还原不了当时到底抵扣了多少。停用既能立刻
	// 止损（新订单不再能用），又保留了历史订单与流水的可追溯性，所以这里要求改成停用。
	_, total, err := s.store.CouponRedemptions(CouponRedemptionFilter{CouponID: target, PageSize: 1})
	if err != nil {
		return internalFailure(err)
	}
	if total > 0 {
		return conflict("该券已有使用记录，请改为停用")
	}
	if err := s.store.DeleteBillingCoupon(target); err != nil {
		return internalFailure(err)
	}
	return nil
}

// CouponRedemptions 返回核销流水与总数，供后台对账与客服排查。
//
// 分页兜底放在服务层而不是 store 里：store 对「超过上限」的处理是整体回落到默认 20 条，
// 而接口语义是「最多给 200 条」。先在这里夹到 200，避免运营传 pageSize=500 时反而只拿到
// 20 条、以为数据丢了。
func (s *Service) CouponRedemptions(filter CouponRedemptionFilter) ([]BillingCouponRedemption, int64, error) {
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	filter.Page = page
	filter.PageSize = pageSize
	records, total, err := s.store.CouponRedemptions(filter)
	if err != nil {
		return nil, 0, internalFailure(err)
	}
	return records, total, nil
}

// AvailableBillingCoupons 返回指定账号此刻还能领用的券，供前台「我的优惠券」。
//
// 判定复用 BillingCouponUsable：把金额门槛设成券自身的最低门槛，让「够不够钱」恒成立，
// 只借用底座里「已停用 / 未开始 / 已过期 / 已领完」这四类判断。领券页与收银台的可用性
// 同源，规则只维护一处，不会各说各话。每人限领次数需要查核销记录，单独补充。
func (s *Service) AvailableBillingCoupons(userID string) ([]BillingCouponView, error) {
	target := strings.TrimSpace(userID)
	if target == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	coupons, err := s.store.BillingCoupons(true)
	if err != nil {
		return nil, internalFailure(err)
	}
	now := s.now()
	views := make([]BillingCouponView, 0, len(coupons))
	for _, coupon := range coupons {
		if err := BillingCouponUsable(coupon, coupon.MinAmountFen, now); err != nil {
			continue
		}
		if coupon.PerUserLimit > 0 {
			used, err := s.store.CouponUsedByUser(coupon.ID, target)
			if err != nil {
				return nil, internalFailure(err)
			}
			if used >= int64(coupon.PerUserLimit) {
				continue
			}
		}
		views = append(views, BillingCouponViewOf(coupon))
	}
	return views, nil
}
