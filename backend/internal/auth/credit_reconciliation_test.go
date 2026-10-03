package auth

import (
	"testing"
	"time"
)

// 后台对账查询的口径：净额、计费起点、最近扣费。
//
// 这三条读数直接决定后台把哪些产物标成"漏扣费"，所以既要覆盖"扣了又退"这种净额归零的
// 情况，也要覆盖"一条扣费都没有"时不能瞎报。

func TestTaskChargeTotalsNetOutRefunds(t *testing.T) {
	env := newCreditTestEnv(t)
	now := time.Now()
	entries := []CreditLedgerEntry{
		{ID: "c1", UserID: "user-1", Kind: CreditKindCharge, RefType: CreditRefTask, RefID: "task-a", Amount: -45, CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "c2", UserID: "user-1", Kind: CreditKindRefund, RefType: CreditRefTask, RefID: "task-a", Amount: 45, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "c3", UserID: "user-1", Kind: CreditKindCharge, RefType: CreditRefTask, RefID: "task-b", Amount: -30, CreatedAt: now.Add(-time.Minute)},
		// 订单充值不是任务扣费：不能被算进任何任务的净额。
		{ID: "c4", UserID: "user-1", Kind: CreditKindTopUp, RefType: CreditRefOrder, RefID: "task-a", Amount: 1000, CreatedAt: now},
	}
	// 直接落库而不是走 AppendCreditEntries：这几条用例读的是"流水本身长什么样"，
	// 走余额校验只会给读路径凭空加一层与结论无关的前置条件。
	if err := env.store.db.Create(&entries).Error; err != nil {
		t.Fatalf("写入流水失败: %v", err)
	}

	totals, err := env.service.AdminTaskChargeTotals([]string{"task-a", "task-b", "task-missing"})
	if err != nil {
		t.Fatal(err)
	}
	if totals["task-a"] != 0 {
		t.Fatalf("task-a 净扣费 = %d; want 0（扣了又退）", totals["task-a"])
	}
	if totals["task-b"] != 30 {
		t.Fatalf("task-b 净扣费 = %d; want 30", totals["task-b"])
	}
	if _, ok := totals["task-missing"]; ok {
		t.Fatalf("没有流水的任务不该出现在结果里：%#v", totals)
	}
}

func TestTaskChargeBillingStartAndRecentCharges(t *testing.T) {
	env := newCreditTestEnv(t)

	// 一条扣费都没有时：不能给出一个零值时间当作"计费起点"，否则全部历史产物都会被
	// 判成漏扣费。
	start, ok, err := env.service.AdminTaskChargeBillingStart()
	if err != nil {
		t.Fatal(err)
	}
	if ok || !start.IsZero() {
		t.Fatalf("无扣费时计费起点 = %v/%v; want 零值且 ok=false", start, ok)
	}

	oldest := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := env.store.db.Create(&[]CreditLedgerEntry{
		{ID: "c-old", UserID: "user-1", Kind: CreditKindCharge, RefType: CreditRefTask, RefID: "task-old", Amount: -45, CreatedAt: oldest},
		{ID: "c-new", UserID: "user-2", Kind: CreditKindCharge, RefType: CreditRefTask, RefID: "task-new", Amount: -30, CreatedAt: time.Now()},
		{ID: "c-adjust", UserID: "user-1", Kind: CreditKindAdmin, RefType: CreditRefSelf, RefID: "c-adjust", Amount: 500, CreatedAt: oldest.Add(-time.Hour)},
	}).Error; err != nil {
		t.Fatalf("写入流水失败: %v", err)
	}

	start, ok, err = env.service.AdminTaskChargeBillingStart()
	if err != nil {
		t.Fatal(err)
	}
	// 起点必须来自任务扣费，不能被更早的管理员调整流水带偏。
	if !ok || !start.Equal(oldest) {
		t.Fatalf("计费起点 = %v/%v; want %v", start, ok, oldest)
	}

	rows, err := env.service.AdminRecentTaskCharges(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].TaskID != "task-new" || rows[1].TaskID != "task-old" {
		t.Fatalf("最近扣费 = %#v; want 按时间倒序的两条任务扣费", rows)
	}
	if rows[0].Net != 30 || rows[1].Net != 45 || rows[0].UserID != "user-2" {
		t.Fatalf("最近扣费读数异常：%#v", rows)
	}
}
