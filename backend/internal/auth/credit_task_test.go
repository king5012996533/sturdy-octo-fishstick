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

// TestQuoteTaskRejectsUnpricedModelLikeCharge 覆盖"试算与提交对未定价给同一个结论"。
//
// 试算是用户在按下生成之前看到的唯一价格来源。它要是把未定价回成 0，用户会以为这次免费，
// 直到提交被 409 拒掉才明白过来——两个入口对同一件事给出不同的说法，比都不给还糟。
func TestQuoteTaskRejectsUnpricedModelLikeCharge(t *testing.T) {
	env := newCreditTaskEnv(t)
	input := TaskChargeInput{UserID: "user-1", TaskID: "task-1", ModelKey: "not-priced", Capability: "IMAGE"}

	_, quoteErr := env.service.QuoteTask(input)
	_, _, _, chargeErr := env.service.ChargeTask(input)
	if quoteErr == nil || chargeErr == nil {
		t.Fatalf("两个入口都应拒绝未定价的模型，实际 quote=%v charge=%v", quoteErr, chargeErr)
	}
	var quoteAppErr *Error
	var chargeAppErr *Error
	if !errors.As(quoteErr, &quoteAppErr) || !errors.As(chargeErr, &chargeAppErr) {
		t.Fatalf("应回结构化错误，实际 quote=%v charge=%v", quoteErr, chargeErr)
	}
	if quoteAppErr.Status != http.StatusConflict || chargeAppErr.Status != http.StatusConflict {
		t.Fatalf("未定价应是 409，实际 quote=%d charge=%d", quoteAppErr.Status, chargeAppErr.Status)
	}
	if quoteAppErr.Message != chargeAppErr.Message {
		t.Fatalf("两个入口的文案应一致，实际 quote=%q charge=%q", quoteAppErr.Message, chargeAppErr.Message)
	}
}

// TestQuoteTaskDoesNotTouchBalance 覆盖试算不写账：报价是只读的。
//
// 试算会被前端在每次改参数时调用，一旦它落了流水，用户的账单会被一串 0 元变动淹没。
func TestQuoteTaskDoesNotTouchBalance(t *testing.T) {
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
	if _, err := env.service.AdjustCredits("user-1", 5000, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	before, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读取余额失败: %v", err)
	}

	if _, err := env.service.QuoteTask(TaskChargeInput{UserID: "user-1", TaskID: "task-2", ModelKey: "seedance-2.5", Capability: "VIDEO", Quantity: 30}); err != nil {
		t.Fatalf("试算失败: %v", err)
	}

	after, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读取余额失败: %v", err)
	}
	if before.Balance != after.Balance || after.LifetimeOut != 0 {
		t.Fatalf("试算不该改变余额或累计消耗，实际 %d -> %d，lifetimeOut=%d", before.Balance, after.Balance, after.LifetimeOut)
	}
	entries, total, err := env.service.CreditLedger(CreditLedgerFilter{UserID: "user-1"})
	if err != nil {
		t.Fatalf("读取流水失败: %v", err)
	}
	if total != 1 || len(entries) != 1 || entries[0].Kind != CreditKindAdmin {
		t.Fatalf("试算不该留下流水，实际 total=%d entries=%#v", total, entries)
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

// TestQuoteTaskChargeBillsVideoPerSecond 覆盖视频按秒计费：金额随提交时的时长走。
//
// 售价定 ¥0.3/秒，所以 15 秒的视频扣 450 分。时长由提交时的 videoSeconds 决定——
// 用户的选择，或模型声明的默认时长（见 app/task_creation.go 的 applyChannelCapabilityDefaults），
// 所以这里只需守住"传进来的秒数真的参与相乘"。
func TestQuoteTaskChargeBillsVideoPerSecond(t *testing.T) {
	env := newCreditTaskEnv(t)
	sell := int64(30) // 30 分/秒
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:      "CHANNEL_000007::seedance-2.0",
		Capability:    string(CapabilityVideo),
		Unit:          string(UnitPerSecond),
		SellUnitPrice: &sell,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}

	// 2.0 支持 5/10/15 秒。
	for _, testCase := range []struct{ seconds, want int64 }{{5, 150}, {10, 300}, {15, 450}} {
		quote, err := env.service.QuoteTaskCharge(TaskChargeInput{
			ModelKey:   "CHANNEL_000007::seedance-2.0",
			Capability: "VIDEO",
			Quantity:   testCase.seconds,
		})
		if err != nil {
			t.Fatalf("%d 秒试算失败: %v", testCase.seconds, err)
		}
		if !quote.Priced || quote.Unit != string(UnitPerSecond) || quote.Credits != testCase.want {
			t.Fatalf("%d 秒应扣 %d 分，实际 %#v", testCase.seconds, testCase.want, quote)
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

// TestQuoteTaskChargeBillsAudioPerRequest 覆盖音频按次计费：一次调用一个价。
//
// 与视频那条用例成对读。视频的金额随秒数变（提交时用户选的），音频不随任何量变——时长在
// 提交那一刻还不存在（配音取决于文本、配乐取决于上游）。这里的"用量"含义是**次数**：
// 任务层固定传 1（见 app.TestTaskChargeQuantityForAudioIsPerRequest），于是每次调用扣一个
// 单价。单元若是 SECOND，传 60 就会被读成"60 秒"，这正是这道口径要挡住的事。
func TestQuoteTaskChargeBillsAudioPerRequest(t *testing.T) {
	env := newCreditTaskEnv(t)
	sell := int64(200) // 200 分 = ¥2.00/次
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:      "CHANNEL_000003::minimax/music-2.5",
		Capability:    string(CapabilityAudio),
		Unit:          string(UnitPerRequest),
		SellUnitPrice: &sell,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}

	for _, testCase := range []struct {
		quantity int64
		credits  int64
	}{
		{1, 200}, // 任务层的真实取值：一次调用
		{0, 200}, // 用量缺失时计费域兜底成一个单位，而不是 0 元
		{3, 600}, // 传 3 就是"3 次"，单位是次而不是秒
	} {
		quote, err := env.service.QuoteTaskCharge(TaskChargeInput{
			ModelKey:   "CHANNEL_000003::minimax/music-2.5",
			Capability: "AUDIO",
			Quantity:   testCase.quantity,
		})
		if err != nil {
			t.Fatalf("试算失败: %v", err)
		}
		if !quote.Priced || quote.Unit != string(UnitPerRequest) || quote.Credits != testCase.credits {
			t.Fatalf("用量 %d 次应扣 %d 分，实际 %#v", testCase.quantity, testCase.credits, quote)
		}
	}
}
