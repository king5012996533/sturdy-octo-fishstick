package auth

import (
	"net/http"
	"testing"
)

// newCreditTaskEnv 建出"积分 + 定价"两张表都齐的测试环境。
//
// 任务计费同时依赖两个域，任何一边缺表都会在报价阶段报一条与计费无关的错，
// 所以这里一次性备齐，而不是让每个用例各自补建。
func newCreditTaskEnv(t *testing.T) *billingTestEnv {
	t.Helper()
	env := newCreditTestEnv(t)
	if err := EnsurePricingSchema(env.store.db); err != nil {
		t.Fatalf("初始化定价表失败: %v", err)
	}
	return env
}

// TestQuoteTaskChargeAppliesMultiplierAndQuantity 覆盖"成本 × 倍率 × 用量"这条主路径。
func TestQuoteTaskChargeAppliesMultiplierAndQuantity(t *testing.T) {
	env := newCreditTaskEnv(t)
	upstream := int64(300)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:          "seedance-2.5",
		Capability:        string(CapabilityVideo),
		Unit:              string(UnitPerSecond),
		UpstreamUnitPrice: &upstream,
		Enabled:           true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}
	if err := env.store.SaveMarkupRule(&MarkupRule{ID: "rule-1", Scope: MarkupScopeGlobal, MultiplierBp: 15000}); err != nil {
		t.Fatalf("写入倍率失败: %v", err)
	}

	quote, err := env.service.QuoteTaskCharge(TaskChargeInput{ModelKey: "seedance-2.5", Capability: "VIDEO", Quantity: 30})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if !quote.Priced || quote.SellUnitPrice == nil {
		t.Fatalf("应算出售价，实际 %#v", quote)
	}
	if *quote.SellUnitPrice != 450 {
		t.Fatalf("1.5 倍后的单价应是 450，实际 %d", *quote.SellUnitPrice)
	}
	if quote.Credits != 13500 {
		t.Fatalf("30 秒应扣 13500，实际 %d", quote.Credits)
	}
	if quote.Unit != "SECOND" || quote.Quantity != 30 {
		t.Fatalf("单位与用量应原样回传，实际 %s × %d", quote.Unit, quote.Quantity)
	}
}

// TestQuoteTaskChargeFallsBackToOneUnit 覆盖用量未知时按一个单位计费。
//
// 退回 0 会让"没传时长"变成一次免费调用，而这类参数缺失在实际调用里并不少见。
func TestQuoteTaskChargeFallsBackToOneUnit(t *testing.T) {
	env := newCreditTaskEnv(t)
	sell := int64(120)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:      "gpt-image-2",
		Capability:    string(CapabilityImage),
		Unit:          string(UnitPerImage),
		SellUnitPrice: &sell,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}

	quote, err := env.service.QuoteTaskCharge(TaskChargeInput{ModelKey: "gpt-image-2", Capability: "IMAGE"})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if quote.Quantity != 1 || quote.Credits != 120 {
		t.Fatalf("用量未知应按 1 个单位，实际 quantity=%d credits=%d", quote.Quantity, quote.Credits)
	}
}

// TestChargeTaskRefusesUnpricedModel 覆盖未定价模型不放行也不免费。
//
// 这是本域最重要的一条产品口径：静默按 0 元出货，等到对账才会发现某批模型一直在白送。
func TestChargeTaskRefusesUnpricedModel(t *testing.T) {
	env := newCreditTaskEnv(t)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 10000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}

	_, _, _, err := env.service.ChargeTask(TaskChargeInput{UserID: "user-1", TaskID: "task-1", ModelKey: "unpriced-model", Capability: "VIDEO", Quantity: 5})
	if err == nil {
		t.Fatal("未定价模型应被拒绝")
	}
	var authErr *Error
	if !errorsAs(err, &authErr) {
		t.Fatalf("期望模块错误，实际 %T: %v", err, err)
	}
	if authErr.Status != http.StatusConflict || authErr.Reason != "failed_precondition" {
		t.Fatalf("期望 409 failed_precondition，实际 %d %s", authErr.Status, authErr.Reason)
	}

	wallet, walletErr := env.service.CreditWallet("user-1")
	if walletErr != nil {
		t.Fatalf("读余额失败: %v", walletErr)
	}
	if wallet.Balance != 10000 {
		t.Fatalf("被拒绝的任务不应改动余额，实际 %d", wallet.Balance)
	}
}

// TestChargeTaskTreatsZeroPriceAsFree 覆盖显式 0 元定价：放行，但不留流水。
//
// 定价域里"未定价"与"免费"是两个状态，这里验证后者真的免费而不是被当成前者拒绝。
func TestChargeTaskTreatsZeroPriceAsFree(t *testing.T) {
	env := newCreditTaskEnv(t)
	free := int64(0)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:      "free-model",
		Capability:    string(CapabilityImage),
		Unit:          string(UnitPerImage),
		SellUnitPrice: &free,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}

	quote, entry, created, err := env.service.ChargeTask(TaskChargeInput{UserID: "user-1", TaskID: "task-1", ModelKey: "free-model", Capability: "IMAGE", Quantity: 4})
	if err != nil {
		t.Fatalf("免费模型不该报错: %v", err)
	}
	if !quote.Priced || quote.Credits != 0 {
		t.Fatalf("期望已定价且为 0，实际 %#v", quote)
	}
	if created || entry != nil {
		t.Fatal("零额变动不应留下流水")
	}
	if _, total, err := env.service.CreditLedger(CreditLedgerFilter{UserID: "user-1"}); err != nil {
		t.Fatalf("读流水失败: %v", err)
	} else if total != 0 {
		t.Fatalf("免费任务不应产生流水，实际 %d 条", total)
	}
}

// TestChargeTaskDeductsThenRefunds 覆盖扣费与退回的金额守恒。
func TestChargeTaskDeductsThenRefunds(t *testing.T) {
	env := newCreditTaskEnv(t)
	sell := int64(200)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:      "seedance-2.0",
		Capability:    string(CapabilityVideo),
		Unit:          string(UnitPerSecond),
		SellUnitPrice: &sell,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 10000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}

	quote, entry, created, err := env.service.ChargeTask(TaskChargeInput{UserID: "user-1", TaskID: "task-1", ModelKey: "seedance-2.0", Capability: "VIDEO", Quantity: 10})
	if err != nil {
		t.Fatalf("扣费失败: %v", err)
	}
	if !created || entry == nil || entry.Amount != -2000 {
		t.Fatalf("应扣 2000 分，实际 %#v", entry)
	}
	if quote.Credits != 2000 {
		t.Fatalf("试算与实扣应一致，实际 %d / %d", quote.Credits, -entry.Amount)
	}

	refunded, refundedNow, err := env.service.RefundTaskCharge("user-1", "task-1", "上游超时")
	if err != nil {
		t.Fatalf("退回失败: %v", err)
	}
	if refunded != quote.Credits || !refundedNow {
		t.Fatalf("退回金额应等于当初扣款 %d，实际 %d（新建=%v）", quote.Credits, refunded, refundedNow)
	}
	// 失败路径会被重放：第二次退必须是空操作而不是再退一笔。
	if _, again, err := env.service.RefundTaskCharge("user-1", "task-1", "上游超时"); err != nil || again {
		t.Fatalf("重复退回应为空操作，实际 新建=%v err=%v", again, err)
	}
	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 10000 {
		t.Fatalf("扣后退回应回到原值，实际 %d", wallet.Balance)
	}
}

// TestQuoteTaskChargeRejectsUnknownCapability 覆盖能力白名单，避免把规则命中到错误维度。
func TestQuoteTaskChargeRejectsUnknownCapability(t *testing.T) {
	env := newCreditTaskEnv(t)
	if _, err := env.service.QuoteTaskCharge(TaskChargeInput{ModelKey: "m", Capability: "MYSTERY"}); err == nil {
		t.Fatal("未知能力应报错")
	}
}
