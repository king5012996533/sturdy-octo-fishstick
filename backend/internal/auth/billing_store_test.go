package auth

import (
	"testing"
	"time"
)

// TestBillingRevenueKeepsEveryBucket 守住营收汇总的一次真实回归。
//
// 曾经用同一个 BillingRevenue 结构体连续 Scan 三次，而 GORM 的 Scan 会整体重置目标，
// 最后那次退款汇总把已付订单数与金额一起清零——后台因此长期显示"营收为零"，
// 而单看代码完全看不出来。这里一次性断言四个字段。
func TestBillingRevenueKeepsEveryBucket(t *testing.T) {
	env := newBillingTestService(t)
	now := env.clock.Now()

	paidAt := now.Add(-2 * time.Hour)
	orders := []BillingOrder{
		{UserID: "user-1", OrderNo: "K260928PAID1", PlanCode: "pro-month", AmountFen: 9900, PayableFen: 9900, Status: OrderStatusPaid, PaidAt: &paidAt},
		{UserID: "user-2", OrderNo: "K260928PEND1", PlanCode: "pro-month", AmountFen: 9900, PayableFen: 9900, Status: OrderStatusPending},
		{UserID: "user-3", OrderNo: "K260928RFND1", PlanCode: "pro-month", AmountFen: 5000, PayableFen: 5000, Status: OrderStatusRefunded, PaidAt: &paidAt},
	}
	for index := range orders {
		orders[index].ExpiresAt = now.Add(time.Hour)
		if err := env.store.CreateBillingOrder(&orders[index]); err != nil {
			t.Fatalf("写入订单失败: %v", err)
		}
	}

	revenue, err := env.store.BillingRevenue(now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("汇总营收失败: %v", err)
	}
	if revenue.PaidOrders != 1 || revenue.PaidAmountFen != 9900 {
		t.Fatalf("已付口径错误：orders=%d amount=%d", revenue.PaidOrders, revenue.PaidAmountFen)
	}
	if revenue.PendingOrders != 1 {
		t.Fatalf("待支付订单数应为 1，实际 %d", revenue.PendingOrders)
	}
	if revenue.RefundedFen != 5000 {
		t.Fatalf("退款金额应为 5000，实际 %d", revenue.RefundedFen)
	}
}

// TestExpireStaleOrdersClosesTimedOutPending 确认超时订单会被关闭。
//
// 不关闭的话"待处理"列表会被用户放弃支付的订单长期占满，前台也一直显示一笔可支付
// 的僵尸订单。
func TestExpireStaleOrdersClosesTimedOutPending(t *testing.T) {
	env := newBillingTestService(t)
	now := env.clock.Now()

	stale := BillingOrder{UserID: "user-1", OrderNo: "K260928STALE", PlanCode: "pro-month", AmountFen: 100, PayableFen: 100, Status: OrderStatusPending, ExpiresAt: now.Add(-time.Minute)}
	if err := env.store.CreateBillingOrder(&stale); err != nil {
		t.Fatalf("写入订单失败: %v", err)
	}
	fresh := BillingOrder{UserID: "user-2", OrderNo: "K260928FRESH", PlanCode: "pro-month", AmountFen: 100, PayableFen: 100, Status: OrderStatusPending, ExpiresAt: now.Add(time.Minute)}
	if err := env.store.CreateBillingOrder(&fresh); err != nil {
		t.Fatalf("写入订单失败: %v", err)
	}

	closed, err := env.store.ExpireStaleOrders(now, 10)
	if err != nil {
		t.Fatalf("关闭超时订单失败: %v", err)
	}
	if closed != 1 {
		t.Fatalf("应关闭 1 笔超时订单，实际 %d", closed)
	}
	updated, err := env.store.BillingOrderByNo("K260928STALE")
	if err != nil || updated.Status != OrderStatusCanceled {
		t.Fatalf("超时订单应被取消：%v %+v", err, updated)
	}
	untouched, err := env.store.BillingOrderByNo("K260928FRESH")
	if err != nil || untouched.Status != OrderStatusPending {
		t.Fatalf("未超时订单不应被改动：%v %+v", err, untouched)
	}
}
