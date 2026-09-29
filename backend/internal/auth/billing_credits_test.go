package auth

import (
	"net/http"
	"testing"
)

// 充值到账的用例：订单付清之后积分必须真的进钱包，而且重放只能进一次。
//
// 全部走真实 SQLite：到账的幂等靠 (user_id, kind, ref_type, ref_id) 唯一索引，
// 用假存储测出来的"幂等"证明不了任何事。

// seedTopUpPlan 建一个"纯积分包"套餐：只到账积分，不产生订阅。
func seedTopUpPlan(t *testing.T, env *billingTestEnv, code string, credits int64, gift int64) BillingPlanView {
	t.Helper()
	view, err := env.service.SaveBillingPlan(BillingPlanInput{
		Code:        code,
		Name:        "积分包 " + code,
		PriceFen:    10000,
		PeriodDays:  0,
		Credits:     credits,
		GiftCredits: gift,
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("建积分包失败: %v", err)
	}
	return *view
}

// TestBillingPaidOrderGrantsCredits 覆盖主路径：下单 → 支付成功 → 本金与赠送分别到账。
func TestBillingPaidOrderGrantsCredits(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "买家", "buyer@example.com")
	seedTopUpPlan(t, env, "topup-100", 10000, 2000)

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "topup-100"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if order.Credits != 10000 || order.GiftCredits != 2000 {
		t.Fatalf("订单应快照套餐积分 10000/2000，实际 %d/%d", order.Credits, order.GiftCredits)
	}

	if _, err := env.service.MarkBillingOrderPaid(order.ID, "测试补单"); err != nil {
		t.Fatalf("补单失败: %v", err)
	}

	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 12000 {
		t.Fatalf("到账后余额应为 12000，实际 %d", wallet.Balance)
	}
	if wallet.LifetimeIn != 12000 || wallet.LifetimeOut != 0 {
		t.Fatalf("累计入账/出账应为 12000/0，实际 %d/%d", wallet.LifetimeIn, wallet.LifetimeOut)
	}

	// 本金与赠送必须是两条流水：合并成一条之后，退款只退本金就没有依据可算。
	entries, total, err := env.service.CreditLedger(CreditLedgerFilter{UserID: "user-1"})
	if err != nil {
		t.Fatalf("读流水失败: %v", err)
	}
	if total != 2 {
		t.Fatalf("到账应留下两条流水，实际 %d（%#v）", total, entries)
	}
	kinds := map[string]int64{}
	for _, entry := range entries {
		kinds[entry.Kind] = entry.Amount
	}
	if kinds[CreditKindTopUp] != 10000 || kinds[CreditKindGift] != 2000 {
		t.Fatalf("流水金额应为 10000/2000，实际 %#v", kinds)
	}
}

// TestBillingPaidOrderGrantsCreditsOnce 盯住重放：回调与客服补单都会重复触发支付成功。
//
// 重复到账是真金白银的损失，而且只有用户主动"多拿了钱"时才会被发现。
func TestBillingPaidOrderGrantsCreditsOnce(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "买家", "buyer@example.com")
	seedTopUpPlan(t, env, "topup-100", 10000, 0)

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "topup-100"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if _, err := env.service.MarkBillingOrderPaid(order.ID, ""); err != nil {
		t.Fatalf("首次补单失败: %v", err)
	}
	if _, err := env.service.MarkBillingOrderPaid(order.ID, ""); err != nil {
		t.Fatalf("重复补单应幂等成功，实际报错: %v", err)
	}

	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 10000 {
		t.Fatalf("重复补单不应重复到账，余额应为 10000，实际 %d", wallet.Balance)
	}
}

// TestBillingPaidOrderUsesOrderSnapshot 覆盖套餐改价之后的到账口径。
//
// 用户付的是当时那一版承诺：运营后来把赠送调小，不该让已经付过款的订单少拿积分。
func TestBillingPaidOrderUsesOrderSnapshot(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "买家", "buyer@example.com")
	plan := seedTopUpPlan(t, env, "topup-100", 10000, 3000)

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "topup-100"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if _, err := env.service.SaveBillingPlan(BillingPlanInput{
		ID: plan.ID, Code: "topup-100", Name: "积分包 topup-100",
		PriceFen: 10000, PeriodDays: 0, Credits: 10000, GiftCredits: 0, Enabled: true,
	}); err != nil {
		t.Fatalf("改套餐失败: %v", err)
	}
	if _, err := env.service.MarkBillingOrderPaid(order.ID, ""); err != nil {
		t.Fatalf("补单失败: %v", err)
	}

	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 13000 {
		t.Fatalf("应按下单时的快照到账 13000，实际 %d", wallet.Balance)
	}
}

// TestBillingTopUpPlanDoesNotCreateSubscription 确认纯积分包不产生订阅。
//
// 否则用户买一包积分会顺带得到一份"永久有效"的订阅，权益页上再也说不清他买过什么。
func TestBillingTopUpPlanDoesNotCreateSubscription(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-1", "买家", "buyer@example.com")
	seedTopUpPlan(t, env, "topup-100", 10000, 0)

	order, err := env.service.CreateBillingOrder("user-1", BillingOrderInput{PlanCode: "topup-100"})
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if _, err := env.service.MarkBillingOrderPaid(order.ID, ""); err != nil {
		t.Fatalf("补单失败: %v", err)
	}

	entitlements, err := env.service.BillingEntitlements("user-1")
	if err != nil {
		t.Fatalf("读权益失败: %v", err)
	}
	if entitlements.Active {
		t.Fatalf("纯积分包不应激活订阅，实际 %#v", entitlements)
	}
}

// TestBillingPlanRejectsNonDeliverableProduct 盯住"周期 0 且没有任何积分"的空商品。
//
// 这种套餐卖了不交付任何东西，几乎总是运营漏填字段，而不是真想做一件空商品。
func TestBillingPlanRejectsNonDeliverableProduct(t *testing.T) {
	env := newBillingTestService(t)
	_, err := env.service.SaveBillingPlan(BillingPlanInput{
		Code: "empty-plan", Name: "空套餐", PriceFen: 100, PeriodDays: 0, Enabled: true,
	})
	assertBillingError(t, err, http.StatusBadRequest, "")
}

// TestBillingPlanRejectsNegativeCredits 负积分等于"买了之后余额变少"。
func TestBillingPlanRejectsNegativeCredits(t *testing.T) {
	env := newBillingTestService(t)
	_, err := env.service.SaveBillingPlan(BillingPlanInput{
		Code: "negative-topup", Name: "负积分包", PriceFen: 100, PeriodDays: 0, Credits: -1, Enabled: true,
	})
	assertBillingError(t, err, http.StatusBadRequest, "")
}

// TestAdminCreditAccountsFiltersAndSorts 覆盖后台积分列表的筛选与排序。
//
// 排序必须是稳定的余额降序：这张表的用途就是找"谁消耗最多"，而余额相同的账号
// 没有第二排序键时，翻页会重复或漏行。
func TestAdminCreditAccountsFiltersAndSorts(t *testing.T) {
	env := newBillingTestService(t)
	seedBillingUser(t, env, "user-big", "大户", "big@example.com")
	seedBillingUser(t, env, "user-small", "散户", "small@example.com")
	seedBillingUser(t, env, "user-zero", "零余额", "zero@example.com")

	if _, err := env.service.AdjustCredits("user-big", 50000, "测试充值"); err != nil {
		t.Fatalf("加分失败: %v", err)
	}
	if _, err := env.service.AdjustCredits("user-small", 100, "测试充值"); err != nil {
		t.Fatalf("加分失败: %v", err)
	}

	rows, total, err := env.service.AdminCreditAccounts(CreditAccountFilter{})
	if err != nil {
		t.Fatalf("读积分列表失败: %v", err)
	}
	if total != 2 {
		t.Fatalf("只有两个账号有积分账户，实际 total=%d", total)
	}
	if rows[0].UserID != "user-big" || rows[0].Balance != 50000 {
		t.Fatalf("应按余额降序，实际首行 %#v", rows[0])
	}
	// 账号资料与余额同处一行：后台看这张表只问"这个人还有多少钱"，
	// 只回 userId 会让列表变成一串看不懂的字符串。
	if rows[0].Name != "大户" || rows[0].Email != "big@example.com" {
		t.Fatalf("应带上账号资料，实际 %#v", rows[0])
	}

	filtered, filteredTotal, err := env.service.AdminCreditAccounts(CreditAccountFilter{Keyword: "small@example.com"})
	if err != nil {
		t.Fatalf("按关键字筛选失败: %v", err)
	}
	if filteredTotal != 1 || len(filtered) != 1 || filtered[0].UserID != "user-small" {
		t.Fatalf("关键字应只命中 user-small，实际 total=%d rows=%#v", filteredTotal, filtered)
	}
}
