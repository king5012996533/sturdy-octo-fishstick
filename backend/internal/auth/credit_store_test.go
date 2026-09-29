package auth

import (
	"net/http"
	"sync"
	"testing"
)

// newCreditTestEnv 复用计费用例的 SQLite 装配，再补建积分表。
//
// 建表在这里显式调用 EnsureCreditSchema，而不是指望 EnsureDevSchema 已经带上积分模型：
// 注册由合并方负责，本域的用例必须能在"还没接线"的状态下独立跑绿。
func newCreditTestEnv(t *testing.T) *billingTestEnv {
	t.Helper()
	env := newBillingTestService(t)
	if err := EnsureCreditSchema(env.store.db); err != nil {
		t.Fatalf("初始化积分表失败: %v", err)
	}
	return env
}

// TestEnsureCreditSchemaIsIdempotent 覆盖重复建表与幂等唯一索引。
//
// 索引缺失时"重复扣费"不会报错，只会静默地把钱扣两遍——所以这里既验证重复建表不炸，
// 也验证索引真的在拦重复写入。
func TestEnsureCreditSchemaIsIdempotent(t *testing.T) {
	env := newCreditTestEnv(t)
	if err := EnsureCreditSchema(env.store.db); err != nil {
		t.Fatalf("重复建表应幂等，实际报错: %v", err)
	}

	first := CreditLedgerEntry{ID: "ledger-1", UserID: "user-1", Kind: CreditKindTopUp, RefType: CreditRefOrder, RefID: "order-1", Amount: 1000}
	if _, _, err := env.store.AppendCreditEntries([]CreditLedgerEntry{first}); err != nil {
		t.Fatalf("写入流水失败: %v", err)
	}
	// 重放走幂等分支：返回原流水、不再新建，余额也不动。
	duplicate := CreditLedgerEntry{ID: "ledger-2", UserID: "user-1", Kind: CreditKindTopUp, RefType: CreditRefOrder, RefID: "order-1", Amount: 1000}
	stored, created, err := env.store.AppendCreditEntries([]CreditLedgerEntry{duplicate})
	if err != nil {
		t.Fatalf("重放不应报错: %v", err)
	}
	if created[0] || stored[0].ID != first.ID {
		t.Fatalf("重放应命中幂等并返回原流水，实际 created=%v id=%s", created[0], stored[0].ID)
	}

	// 索引本身要拦得住绕过幂等检查的写入，否则并发下仍可能落下两条同键流水。
	bypass := CreditLedgerEntry{ID: "ledger-3", UserID: "user-1", Kind: CreditKindTopUp, RefType: CreditRefOrder, RefID: "order-1", Amount: 1000}
	if err := env.store.db.Create(&bypass).Error; err == nil {
		t.Fatal("同一 (user, kind, refType, refId) 应被唯一索引拒绝")
	}
}

// TestChargeCreditsIsIdempotentPerTask 覆盖任务重试不会重复扣费。
//
// 这是整个积分域最容易出事的一条：任务重试与网关重放都会用同一个任务 ID 再次提交，
// 第二次必须被识别成"已扣过"而不是再扣一笔。
func TestChargeCreditsIsIdempotentPerTask(t *testing.T) {
	env := newCreditTestEnv(t)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 5000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}

	first, created, err := env.service.ChargeTaskCredits("user-1", "task-1", 300, "Seedance 30 秒")
	if err != nil {
		t.Fatalf("首次扣费失败: %v", err)
	}
	if !created {
		t.Fatal("首次扣费应新建流水")
	}

	replayed, created, err := env.service.ChargeTaskCredits("user-1", "task-1", 300, "Seedance 30 秒")
	if err != nil {
		t.Fatalf("重放扣费不应报错: %v", err)
	}
	if created {
		t.Fatal("重放扣费不应新建流水")
	}
	if replayed.ID != first.ID {
		t.Fatalf("重放应返回同一条流水，实际 %s / %s", first.ID, replayed.ID)
	}

	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 4700 {
		t.Fatalf("重放不应重复扣费，期望余额 4700，实际 %d", wallet.Balance)
	}
	if wallet.LifetimeOut != 300 {
		t.Fatalf("累计消耗应只记一次，实际 %d", wallet.LifetimeOut)
	}
}

// TestChargeCreditsRejectsInsufficientBalance 覆盖余额不足：既不扣钱也不留流水。
//
// 错误必须是 402 + insufficient_credits：前端据此弹充值引导，而不是把"钱不够"
// 混进通用失败提示里。
func TestChargeCreditsRejectsInsufficientBalance(t *testing.T) {
	env := newCreditTestEnv(t)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 200, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}

	_, _, err := env.service.ChargeTaskCredits("user-1", "task-1", 500, "超出余额")
	if err == nil {
		t.Fatal("余额不足应报错")
	}
	var authErr *Error
	if !errorsAs(err, &authErr) {
		t.Fatalf("期望模块错误，实际 %T: %v", err, err)
	}
	if authErr.Status != http.StatusPaymentRequired {
		t.Fatalf("期望 402，实际 %d（%s）", authErr.Status, authErr.Message)
	}
	if authErr.Reason != "insufficient_credits" {
		t.Fatalf("期望 insufficient_credits，实际 %q", authErr.Reason)
	}

	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 200 {
		t.Fatalf("余额不足时不应改动余额，实际 %d", wallet.Balance)
	}
	entries, total, err := env.service.CreditLedger(CreditLedgerFilter{UserID: "user-1"})
	if err != nil {
		t.Fatalf("读流水失败: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("失败的扣费不应留流水，期望 1 条，实际 %d", total)
	}
}

// TestRefundTaskCreditsIsIdempotent 覆盖失败退回只可能发生一次。
func TestRefundTaskCreditsIsIdempotent(t *testing.T) {
	env := newCreditTestEnv(t)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 1000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if _, _, err := env.service.ChargeTaskCredits("user-1", "task-1", 400, "预扣"); err != nil {
		t.Fatalf("扣费失败: %v", err)
	}

	if _, created, err := env.service.RefundTaskCredits("user-1", "task-1", 400, "上游超时"); err != nil || !created {
		t.Fatalf("首次退回应成功且新建流水，实际 created=%v err=%v", created, err)
	}
	if _, created, err := env.service.RefundTaskCredits("user-1", "task-1", 400, "上游超时"); err != nil || created {
		t.Fatalf("重复退回应被幂等拦住，实际 created=%v err=%v", created, err)
	}

	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 1000 {
		t.Fatalf("扣后退回应回到原值，实际 %d", wallet.Balance)
	}
}

// TestGrantTopUpCreditsWritesPrincipalAndGiftTogether 覆盖本金与赠送同一事务。
func TestGrantTopUpCreditsWritesPrincipalAndGiftTogether(t *testing.T) {
	env := newCreditTestEnv(t)
	views, created, err := env.service.GrantTopUpCredits("user-1", "order-9", 1000, 200, "首充赠送")
	if err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if len(views) != 2 || len(created) != 2 || !created[0] || !created[1] {
		t.Fatalf("本金与赠送应各记一条流水，实际 %#v", views)
	}
	if views[0].Kind != CreditKindTopUp || views[0].Amount != 1000 {
		t.Fatalf("第一条应是本金，实际 %#v", views[0])
	}
	if views[1].Kind != CreditKindGift || views[1].Amount != 200 {
		t.Fatalf("第二条应是赠送，实际 %#v", views[1])
	}
	if views[1].BalanceAfter != 1200 {
		t.Fatalf("赠送后的余额应是 1200，实际 %d", views[1].BalanceAfter)
	}

	// 回调重放：两条都命中幂等，余额不变。
	if _, created, err := env.service.GrantTopUpCredits("user-1", "order-9", 1000, 200, "首充赠送"); err != nil {
		t.Fatalf("重放充值不应报错: %v", err)
	} else if created[0] || created[1] {
		t.Fatal("重放充值不应新建流水")
	}
	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 1200 {
		t.Fatalf("重放不应重复到账，实际 %d", wallet.Balance)
	}
}

// TestCreditLedgerFiltersAndPaginates 覆盖分页、种类筛选与非法种类。
func TestCreditLedgerFiltersAndPaginates(t *testing.T) {
	env := newCreditTestEnv(t)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 1000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	for index := 0; index < 3; index++ {
		taskID := "task-" + string(rune('a'+index))
		if _, _, err := env.service.ChargeTaskCredits("user-1", taskID, 100, "批量扣费"); err != nil {
			t.Fatalf("扣费失败: %v", err)
		}
	}

	page, total, err := env.service.CreditLedger(CreditLedgerFilter{UserID: "user-1", Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("读流水失败: %v", err)
	}
	if total != 4 || len(page) != 2 {
		t.Fatalf("期望 4 条共 2 页每页 2 条，实际 total=%d len=%d", total, len(page))
	}

	charges, total, err := env.service.CreditLedger(CreditLedgerFilter{UserID: "user-1", Kind: "task_charge"})
	if err != nil {
		t.Fatalf("按种类筛选失败: %v", err)
	}
	if total != 3 || len(charges) != 3 {
		t.Fatalf("期望 3 条扣费流水，实际 total=%d len=%d", total, len(charges))
	}

	if _, _, err := env.service.CreditLedger(CreditLedgerFilter{UserID: "user-1", Kind: "NOT_A_KIND"}); err == nil {
		t.Fatal("非法种类应报错，而不是静默返回空列表")
	}
}

// TestConcurrentChargesCannotOverdraw 覆盖并发扣费不会把余额扣成负数。
//
// 这条针对的是"先读余额再决定扣不扣"的写法：两个请求各自读到够扣，一起写下去就是负数。
// 断言不依赖谁的请求先到，只看最终账实相符。
func TestConcurrentChargesCannotOverdraw(t *testing.T) {
	env := newCreditTestEnv(t)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 500, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}

	const attempts = 10
	var wait sync.WaitGroup
	var lock sync.Mutex
	succeeded := int64(0)
	for index := 0; index < attempts; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, created, err := env.service.ChargeTaskCredits("user-1", "task-"+string(rune('a'+index)), 100, "并发扣费")
			if err != nil || !created {
				return
			}
			lock.Lock()
			succeeded++
			lock.Unlock()
		}(index)
	}
	wait.Wait()

	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance < 0 {
		t.Fatalf("余额被扣成负数: %d", wallet.Balance)
	}
	if wallet.Balance != 500-succeeded*100 {
		t.Fatalf("账实不符：余额 %d，成功扣费 %d 次", wallet.Balance, succeeded)
	}
}

// errorsAs 是本文件用的 errors.As 简写，避免为一个断言导入 errors 包。
func errorsAs(err error, target **Error) bool {
	typed, ok := err.(*Error)
	if !ok {
		return false
	}
	*target = typed
	return true
}
