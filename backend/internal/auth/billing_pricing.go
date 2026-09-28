package auth

import (
	"strings"
	"time"
)

// 优惠券与金额的纯计算。
//
// 单独放在这里而不是塞进服务层：这几个函数决定"用户到底付多少钱"，必须能被穷举测试
// （见 billing_pricing_test.go）。任何依赖数据库或时钟注入的逻辑都不进这个文件。

// BillingCouponUsable 判断优惠券此刻能否用于这笔金额，不可用时返回可直接展示的原因。
//
// 顺序刻意如此：先"是否还能领"再"够不够门槛"——同一条券同时不满足两个条件时，
// 告诉用户"已领完"比"还差 30 元"更接近事实。
func BillingCouponUsable(coupon BillingCoupon, amountFen int64, now time.Time) error {
	if !coupon.Enabled {
		return invalidArgument("优惠券已停用")
	}
	if !coupon.StartsAt.IsZero() && now.Before(coupon.StartsAt) {
		return invalidArgument("优惠券尚未到可使用时间")
	}
	if !coupon.ExpiresAt.IsZero() && !now.Before(coupon.ExpiresAt) {
		return invalidArgument("优惠券已过期")
	}
	if coupon.TotalQuota > 0 && coupon.UsedCount >= coupon.TotalQuota {
		return invalidArgument("优惠券已领完")
	}
	if amountFen < coupon.MinAmountFen {
		return invalidArgument("该优惠券需满额才能使用")
	}
	return nil
}

// BillingCouponDiscount 计算抵扣额（分）。
//
// 三点约束：抵扣不会超过订单金额（否则会出现负数应付）、折扣券按万分比向下取整
// （让利归平台承担，不出现四舍五入后用户少付一分的情况）、结果不为负。
func BillingCouponDiscount(coupon BillingCoupon, amountFen int64) int64 {
	if amountFen <= 0 {
		return 0
	}
	var discount int64
	switch coupon.Kind {
	case CouponKindPercent:
		rate := coupon.Value
		if rate < 0 {
			rate = 0
		}
		if rate > 10000 {
			rate = 10000
		}
		discount = amountFen * (10000 - rate) / 10000
	case CouponKindAmount:
		discount = coupon.Value
	default:
		return 0
	}
	if discount < 0 {
		return 0
	}
	if discount > amountFen {
		return amountFen
	}
	return discount
}

// NormalizeBillingCouponCode 统一券码形态：去空格并转大写。
//
// 券码是给用户手输的，大小写与空格不该成为"券无效"的原因；这里与
// BillingCouponByCode 的查询条件保持同一套规则。
func NormalizeBillingCouponCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
