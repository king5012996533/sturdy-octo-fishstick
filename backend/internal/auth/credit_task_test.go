package auth

import (
	"errors"
	"net/http"
	"strings"
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

// TestQuoteTaskChargePricesImageByQualityTier 覆盖图片按上游质量档取价。
//
// gpt-image-2 的三档上游成本是 $0.012 / $0.047 / $0.128（差 10.7 倍）。三行价必须各自
// 独立命中：任何"取不到就退回某个档"的实现都会在 high 上按 low 的成本出货。
func TestQuoteTaskChargePricesImageByQualityTier(t *testing.T) {
	env := newCreditTaskEnv(t)
	rows := []struct {
		tier     PriceTier
		upstream int64
		want     int64
	}{
		{PriceTierLow, 9, 18},
		{PriceTierMedium, 34, 68},
		{PriceTierHigh, 93, 186},
		// 空档是"面板没指定质量"时的价（上游按 auto 计费），它不是任何一档的别名。
		{PriceTierNone, 93, 186},
	}
	for _, row := range rows {
		upstream := row.upstream
		multiplierBp := 2 * markupBaseBp
		if err := env.store.SaveModelPrice(&ModelPrice{
			ModelKey:          "CHANNEL_000003::openai/gpt-image-2",
			Capability:        string(CapabilityImage),
			PriceTier:         string(row.tier),
			Unit:              string(UnitPerImage),
			UpstreamUnitPrice: &upstream,
			// 价目只存上游成本 + 倍率，售价由解析引擎算出来——上游调价时改一个数就够了。
			MultiplierBp: &multiplierBp,
			Enabled:      true,
		}); err != nil {
			t.Fatalf("写入 %s 档单价失败: %v", row.tier, err)
		}
	}

	for _, row := range rows {
		quote, err := env.service.QuoteTaskCharge(TaskChargeInput{
			ModelKey:   "CHANNEL_000003::openai/gpt-image-2",
			Capability: "IMAGE",
			Tier:       string(row.tier),
			Quantity:   3,
		})
		if err != nil {
			t.Fatalf("%s 档试算失败: %v", row.tier, err)
		}
		if !quote.Priced || quote.SellUnitPrice == nil || *quote.SellUnitPrice != row.want {
			t.Fatalf("%s 档单价应为 %d，实际 %#v", row.tier, row.want, quote)
		}
		if quote.Credits != row.want*3 {
			t.Fatalf("%s 档 3 张应扣 %d，实际 %d", row.tier, row.want*3, quote.Credits)
		}
	}
}

// TestChargeTaskRejectsUnpricedImageTier 覆盖"漏配一档"必须被拒绝，而不是按别档成交。
func TestChargeTaskRejectsUnpricedImageTier(t *testing.T) {
	env := newCreditTaskEnv(t)
	upstream := int64(9)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:          "CHANNEL_000003::openai/gpt-image-2",
		Capability:        string(CapabilityImage),
		PriceTier:         string(PriceTierLow),
		Unit:              string(UnitPerImage),
		UpstreamUnitPrice: &upstream,
		Enabled:           true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}

	_, _, _, err := env.service.ChargeTask(TaskChargeInput{
		UserID:     "user-1",
		TaskID:     "task-1",
		ModelKey:   "CHANNEL_000003::openai/gpt-image-2",
		Capability: "IMAGE",
		Tier:       string(PriceTierHigh),
		Quantity:   1,
	})
	var serviceErr *Error
	if !errors.As(err, &serviceErr) {
		t.Fatalf("应返回结构化错误，实际 %T：%v", err, err)
	}
	if serviceErr.Status != http.StatusConflict {
		t.Fatalf("漏配的高档应报 409，实际 %d", serviceErr.Status)
	}
	// 文案必须点出是哪个档位漏了：只报模型名会让管理员反复确认一个已经配好的模型。
	if !strings.Contains(serviceErr.Message, "HIGH") {
		t.Fatalf("错误文案应指明是 HIGH 档，实际 %q", serviceErr.Message)
	}
}

// TestQuoteTaskChargeRejectsTierThatDoesNotBelongToCapability 覆盖档位与能力必须匹配。
//
// 认错的档位不是"随便挑一档"，而是直接拒绝：视频带着图片的 HIGH 去取价，取到的是另一套
// 成本口径，而且不会报错。
func TestQuoteTaskChargeRejectsTierThatDoesNotBelongToCapability(t *testing.T) {
	env := newCreditTaskEnv(t)
	for _, test := range []struct{ capability, tier string }{
		{"VIDEO", "HIGH"},
		{"AUDIO", "INPUT"},
		{"IMAGE", "CACHE"},
		{"TEXT", "LOW"},
	} {
		if _, err := env.service.QuoteTaskCharge(TaskChargeInput{
			ModelKey:   "some-model",
			Capability: test.capability,
			Tier:       test.tier,
			Quantity:   5,
		}); err == nil {
			t.Fatalf("%s 带着 %s 档应被拒绝", test.capability, test.tier)
		}
	}
}

// TestChargeTaskPreChargesTextAtStartPrice 覆盖文本在提交阶段按起步价预扣。
//
// 文本按 token 结算，而 token 用量要等上游回执，所以提交时既定不了档位也定不了用量：
// 空档位不该被当成"参数填错"而报 400，那是管理员口径；这里按产品定的下限预扣 1 积分。
func TestChargeTaskPreChargesTextAtStartPrice(t *testing.T) {
	env := newCreditTaskEnv(t)
	upstream := int64(200)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:          "CHANNEL_000006::deepseek-flash",
		Capability:        string(CapabilityText),
		PriceTier:         string(PriceTierInput),
		Unit:              string(UnitPerMillionTokens),
		UpstreamUnitPrice: &upstream,
		Enabled:           true,
	}); err != nil {
		t.Fatalf("写入文本单价失败: %v", err)
	}

	quote, err := env.service.QuoteTaskCharge(TaskChargeInput{
		ModelKey:   "CHANNEL_000006::deepseek-flash",
		Capability: "TEXT",
		Quantity:   0,
	})
	if err != nil {
		t.Fatalf("文本试算不应报错: %v", err)
	}
	if !quote.Priced || quote.Credits != textStartPriceCredits {
		t.Fatalf("文本应按起步价预扣 %d 积分，实际 %#v", textStartPriceCredits, quote)
	}
	if quote.Unit != string(UnitPerRequest) || quote.Quantity != 1 {
		t.Fatalf("起步价应按次计一个单位，实际 %s × %d", quote.Unit, quote.Quantity)
	}
}

// TestChargeTaskRejectsUnpricedTextModel 覆盖起步价的那个例外不能吃掉"未定价不给生成"。
//
// 起步价只在模型确实配过文本价目时生效：否则一个拼错的模型标识会变成一条永远免费的通道。
func TestChargeTaskRejectsUnpricedTextModel(t *testing.T) {
	env := newCreditTaskEnv(t)
	_, _, _, err := env.service.ChargeTask(TaskChargeInput{
		UserID:     "user-1",
		TaskID:     "task-text",
		ModelKey:   "CHANNEL_000006::没有这个模型",
		Capability: "TEXT",
		Quantity:   0,
	})
	var serviceErr *Error
	if !errors.As(err, &serviceErr) {
		t.Fatalf("应返回结构化错误，实际 %T：%v", err, err)
	}
	if serviceErr.Status != http.StatusConflict {
		t.Fatalf("未配置过文本价目的模型应报 409，实际 %d", serviceErr.Status)
	}
}

// TestQuoteTaskChargeBillsVideoPerRequest 覆盖视频按条计费：调用方传的时长不参与相乘。
//
// 上游按"每条多少钱"结算，跟生成多少秒无关。一旦把秒数乘进去，一条 600 积分的视频
// 在 30 秒档会变成 18000 积分。这里同时覆盖 2.0（×0.6 的内测亏本价）与 2.5 两档。
func TestQuoteTaskChargeBillsVideoPerRequest(t *testing.T) {
	env := newCreditTaskEnv(t)
	rows := []struct {
		modelKey     string
		multiplierBp int
		want         int64
	}{
		{"CHANNEL_000007::seedance-2.0", 6000, 300},
		{"CHANNEL_000007::seedance-2.5", 12000, 600},
	}
	for _, row := range rows {
		upstream := int64(500) // 上游 ¥5/条
		multiplierBp := row.multiplierBp
		if err := env.store.SaveModelPrice(&ModelPrice{
			ModelKey:          row.modelKey,
			Capability:        string(CapabilityVideo),
			Unit:              string(UnitPerRequest),
			UpstreamUnitPrice: &upstream,
			MultiplierBp:      &multiplierBp,
			Enabled:           true,
		}); err != nil {
			t.Fatalf("写入 %s 单价失败: %v", row.modelKey, err)
		}
	}

	// 2.0 支持 5/10/15 秒，2.5 固定 30 秒；按条计费时传哪个都只算一条。
	for _, row := range rows {
		for _, seconds := range []int64{5, 10, 15, 30} {
			quote, err := env.service.QuoteTaskCharge(TaskChargeInput{
				ModelKey:   row.modelKey,
				Capability: "VIDEO",
				Quantity:   seconds,
			})
			if err != nil {
				t.Fatalf("%s 试算失败: %v", row.modelKey, err)
			}
			if !quote.Priced || quote.SellUnitPrice == nil || *quote.SellUnitPrice != row.want {
				t.Fatalf("%s 单价应为 %d，实际 %#v", row.modelKey, row.want, quote)
			}
			if quote.Unit != string(UnitPerRequest) || quote.Quantity != 1 || quote.Credits != row.want {
				t.Fatalf("%s 传 %d 秒应按一条 %d 积分，实际 %s × %d = %d",
					row.modelKey, seconds, row.want, quote.Unit, quote.Quantity, quote.Credits)
			}
		}
	}
}

// TestChargeFormulaRendersUserReadableUnit 覆盖流水备注里的单位标签。
//
// 备注是给用户复核账单用的：直接写 TOKEN_1M 这类枚举名，他没法看出这笔钱怎么来的。
func TestChargeFormulaRendersUserReadableUnit(t *testing.T) {
	cases := []struct {
		unit     string
		quantity int64
		want     string
	}{
		// 按条计费时用量恒为 1，算式里不再冗余地写 "× 1"。
		{string(UnitPerRequest), 1, "600分/条"},
		{string(UnitPerImage), 3, "600分/张 × 3"},
		{string(UnitPerSecond), 30, "600分/秒 × 30"},
		{string(UnitPerMillionTokens), 1200, "600分/百万token × 1200"},
		{string(UnitPerThousandTokens), 3300, "600分/千token × 3300"},
	}
	for _, testCase := range cases {
		if got := chargeFormula(600, testCase.unit, testCase.quantity); got != testCase.want {
			t.Fatalf("%s 的备注应为 %q，实际 %q", testCase.unit, testCase.want, got)
		}
	}
}
