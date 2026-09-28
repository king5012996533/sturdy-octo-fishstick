package auth

import (
	"testing"
	"time"
)

func TestBillingCouponDiscount(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		coupon   BillingCoupon
		amount   int64
		expected int64
	}{
		{"满减券按面额抵扣", BillingCoupon{Kind: CouponKindAmount, Value: 1000, Enabled: true}, 5000, 1000},
		{"满减券不会把应付打到负数", BillingCoupon{Kind: CouponKindAmount, Value: 9900, Enabled: true}, 5000, 5000},
		{"八折券抵扣两成", BillingCoupon{Kind: CouponKindPercent, Value: 8000, Enabled: true}, 10000, 2000},
		{"折扣券向下取整", BillingCoupon{Kind: CouponKindPercent, Value: 6666, Enabled: true}, 999, 333},
		{"折扣率越大抵扣越多但不超过金额", BillingCoupon{Kind: CouponKindPercent, Value: 0, Enabled: true}, 100, 100},
		{"未知券型不抵扣", BillingCoupon{Kind: "MYSTERY", Value: 100}, 1000, 0},
		{"金额为零不抵扣", BillingCoupon{Kind: CouponKindAmount, Value: 100, Enabled: true}, 0, 0},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := BillingCouponDiscount(item.coupon, item.amount); got != item.expected {
				t.Fatalf("抵扣额应为 %d，实际 %d", item.expected, got)
			}
		})
	}

	// 可用性：先判"还能不能用"，再判门槛，顺序决定用户看到哪条原因。
	expired := BillingCoupon{Enabled: true, ExpiresAt: now.Add(-time.Hour)}
	if err := BillingCouponUsable(expired, 10000, now); err == nil {
		t.Fatal("过期券不应可用")
	}
	exhausted := BillingCoupon{Enabled: true, TotalQuota: 2, UsedCount: 2, MinAmountFen: 99999}
	if err := BillingCouponUsable(exhausted, 100, now); err == nil {
		t.Fatal("已领完的券不应可用")
	}
	belowThreshold := BillingCoupon{Enabled: true, MinAmountFen: 5000}
	if err := BillingCouponUsable(belowThreshold, 4999, now); err == nil {
		t.Fatal("未达门槛的券不应可用")
	}
	if err := BillingCouponUsable(belowThreshold, 5000, now); err != nil {
		t.Fatalf("达标券应可用：%v", err)
	}
	disabled := BillingCoupon{Enabled: false}
	if err := BillingCouponUsable(disabled, 10000, now); err == nil {
		t.Fatal("停用的券不应可用")
	}
}
