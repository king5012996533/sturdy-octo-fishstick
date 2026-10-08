package auth

import (
	"strings"
	"testing"
)

// seedTextPrices 给一个模型配齐三档文本价（分 / 百万 token）。
func seedTextPrices(t *testing.T, env *billingTestEnv, modelKey string, cache int64, input int64, output int64) {
	t.Helper()
	tiers := map[string]int64{string(PriceTierCache): cache, string(PriceTierInput): input, string(PriceTierOutput): output}
	for tier, price := range tiers {
		value := price
		if err := env.store.SaveModelPrice(&ModelPrice{
			ModelKey:      modelKey,
			Capability:    string(CapabilityText),
			PriceTier:     tier,
			Unit:          string(UnitPerMillionTokens),
			SellUnitPrice: &value,
			Enabled:       true,
		}); err != nil {
			t.Fatalf("写入 %s 档单价失败: %v", tier, err)
		}
	}
}

// settleTestTask 造一个"已充值、已按起步价预扣"的任务，返回环境。
func settleTestTask(t *testing.T, modelKey string) *billingTestEnv {
	t.Helper()
	env := newCreditTaskEnv(t)
	seedTextPrices(t, env, modelKey, 49, 490, 2450)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 1_000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if _, _, err := env.service.ChargeTaskCredits("user-1", "task-1", textStartPriceCredits, "文本起步价"); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
	return env
}

// TestSettleTextTaskChargeBillsThreeTiers 覆盖按 token 结算的主路径。
//
// 这条路径存在的唯一理由就是：一次带着上下文的长对话不能按起步价成交。所以断言的重点是
// 金额本身——三档各自向上取整后求和，再减掉提交时已经预扣的那 1 积分。
func TestSettleTextTaskChargeBillsThreeTiers(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := settleTestTask(t, modelKey)

	quote, entry, created, err := env.service.SettleTextTaskCharge(TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 20_000, Cached: 12_000, Output: 1_000},
	})
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	// 缓存 12000×49/1e6=0.588→1；未命中 8000×490/1e6=3.92→4；输出 1000×2450/1e6=2.45→3。
	if quote.Credits != 8 {
		t.Fatalf("三档合计应为 8，实际 %d", quote.Credits)
	}
	if quote.Charged != textStartPriceCredits || quote.Delta != 7 {
		t.Fatalf("应从起步价 1 补扣 7，实际 charged=%d delta=%d", quote.Charged, quote.Delta)
	}
	if !created || entry == nil || entry.Kind != CreditKindSettle || entry.Amount != -7 {
		t.Fatalf("应落一条 TASK_SETTLE 的 -7 流水，实际 created=%v entry=%#v", created, entry)
	}
	if entry.RefType != CreditRefTask || entry.RefID != "task-1" {
		t.Fatalf("流水应挂回任务，实际 %s/%s", entry.RefType, entry.RefID)
	}

	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 1_000-textStartPriceCredits-7 {
		t.Fatalf("余额应为 992，实际 %d", wallet.Balance)
	}
}

// TestSettleTextTaskChargeIsIdempotent 覆盖任务收尾被重放时的幂等。
//
// 收尾重放比"正常情况下跑两次"更容易发生：worker 崩溃恢复、人工重跑都会用同一个任务 ID
// 再走一次结算。重放时补扣第二遍，用户余额会在没有任何可见原因的情况下少一块。
func TestSettleTextTaskChargeIsIdempotent(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := settleTestTask(t, modelKey)
	input := TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 20_000, Cached: 12_000, Output: 1_000},
	}
	if _, _, _, err := env.service.SettleTextTaskCharge(input); err != nil {
		t.Fatalf("首次结算失败: %v", err)
	}
	replay, _, created, err := env.service.SettleTextTaskCharge(input)
	if err != nil {
		t.Fatalf("重放不应报错: %v", err)
	}
	if created {
		t.Fatal("重放不应新建流水")
	}
	if replay.Delta != 7 {
		t.Fatalf("重放应读出同样的差额，实际 %d", replay.Delta)
	}
	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 992 {
		t.Fatalf("重放不应重复补扣，期望余额 992，实际 %d", wallet.Balance)
	}
}

// TestSettleTextTaskChargeClampsCachedAboveInput 覆盖上游把 cached 报成大于 input 的情形。
//
// 缓存统计偶尔含上一轮的提示词。不夹回来时"未命中 = input - cached"变成负数，
// 那一档被静默按 0 处理，而缓存档却按虚高的 token 数计价——两头一起错。
func TestSettleTextTaskChargeClampsCachedAboveInput(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := settleTestTask(t, modelKey)

	quote, _, _, err := env.service.SettleTextTaskCharge(TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 20_000, Cached: 30_000, Output: 1_000},
	})
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	// 夹回后：缓存 20000×49/1e6=0.98→1，未命中 0，输出 2.45→3，合计 4。
	if quote.Credits != 4 {
		t.Fatalf("cached 大于 input 时应按 input 收敛，期望 4，实际 %d", quote.Credits)
	}
}

// TestSettleTextTaskChargeReportsMissingTiers 覆盖缺档：能算的照算，缺的那部分必须被报出来。
//
// 整笔不算会让"缺一档"看起来像"这个模型没定价"；静默按 0 算那一档则会让平台一直少收钱而
// 不报错。所以这里既断言算出来的金额，也断言缺失的档位名被回传。
func TestSettleTextTaskChargeReportsMissingTiers(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := newCreditTaskEnv(t)
	outputPrice := int64(2450)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey: modelKey, Capability: string(CapabilityText), PriceTier: string(PriceTierOutput),
		Unit: string(UnitPerMillionTokens), SellUnitPrice: &outputPrice, Enabled: true,
	}); err != nil {
		t.Fatalf("写入输出档单价失败: %v", err)
	}
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 1_000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if _, _, err := env.service.ChargeTaskCredits("user-1", "task-1", textStartPriceCredits, "文本起步价"); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}

	quote, _, _, err := env.service.SettleTextTaskCharge(TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 20_000, Cached: 12_000, Output: 1_000},
	})
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	if !quote.Priced || quote.Credits != 3 {
		t.Fatalf("只有输出档时应按输出档成交，实际 priced=%v credits=%d", quote.Priced, quote.Credits)
	}
	if len(quote.MissingTiers) != 2 {
		t.Fatalf("应报出两个缺档，实际 %#v", quote.MissingTiers)
	}
}

// TestSettleTextTaskChargeSkipsWhenNothingIsPriced 覆盖一条价目都没有的模型。
//
// 这时既算不出总额也不该拒绝：结算发生在任务成功之后，报错只会把一条已经成功的任务翻成
// 失败。调用方据 Priced=false 落日志、人工核对。
func TestSettleTextTaskChargeSkipsWhenNothingIsPriced(t *testing.T) {
	env := newCreditTaskEnv(t)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 1_000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}

	quote, entry, created, err := env.service.SettleTextTaskCharge(TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: "unknown-model",
		Usage: TextTokenUsage{Input: 20_000, Output: 1_000},
	})
	if err != nil {
		t.Fatalf("无价目不应报错: %v", err)
	}
	if quote.Priced || created || entry != nil {
		t.Fatalf("无价目不应落账，实际 priced=%v created=%v entry=%#v", quote.Priced, created, entry)
	}
}

// TestSettleTextTaskChargeKeepsStartPriceWhenUsageIsSmall 覆盖真实用量不足起步价时的边界。
//
// 起步价是产品定的下限而不是押金：三档各自向上取整后总额仍不高于 1 积分的调用（用量
// 极小的纯输入）按起步价成交，既不退款也不产生第二条流水。这条分支在真实流量里罕见，
// 但只要存在，它就必须是"不落账"而不是"落一条 0 元流水"或"退一笔钱"。
func TestSettleTextTaskChargeKeepsStartPriceWhenUsageIsSmall(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := settleTestTask(t, modelKey)

	quote, entry, created, err := env.service.SettleTextTaskCharge(TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 100, Cached: 100},
	})
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	if quote.Credits != textStartPriceCredits {
		t.Fatalf("真实用量应为 1 积分，实际 %d", quote.Credits)
	}
	if quote.Delta != 0 || created || entry != nil {
		t.Fatalf("用量低于起步价时不应落账，实际 delta=%d created=%v entry=%#v", quote.Delta, created, entry)
	}
	wallet, err := env.service.CreditWallet("user-1")
	if err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	if wallet.Balance != 999 {
		t.Fatalf("余额应只扣起步价 1，实际 %d", wallet.Balance)
	}
}

// TestSettleTextTaskChargeLiftsLongConversationAboveStartPrice 是这次改动要挡住的资损场景。
//
// 一次带上万 token 上下文的对话，成本按真实用量是十几分；只按起步价成交的话，用户每发
// 一条消息平台就亏一笔，而且账单上看起来完全正常。
func TestSettleTextTaskChargeLiftsLongConversationAboveStartPrice(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := settleTestTask(t, modelKey)

	quote, _, _, err := env.service.SettleTextTaskCharge(TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 25_000, Output: 1_500},
	})
	if err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	// 25000×490/1e6=12.25→13；1500×2450/1e6=3.675→4。
	if quote.Credits != 17 {
		t.Fatalf("长对话应结算 17 积分，实际 %d", quote.Credits)
	}
	if quote.Delta <= 10 {
		t.Fatalf("补扣应明显高于起步价，实际 %d", quote.Delta)
	}
}

// TestTextTierCreditsRoundsUpPerTierAndHonoursLegacyUnit 覆盖换算本身的取整与旧单位兼容。
func TestTextTierCreditsRoundsUpPerTierAndHonoursLegacyUnit(t *testing.T) {
	cases := []struct {
		name     string
		tokens   int64
		price    int64
		unit     string
		expected int64
	}{
		{"不足一分也按一分收", 1, 1, string(UnitPerMillionTokens), 1},
		{"整除时不多收", 1_000_000, 490, string(UnitPerMillionTokens), 490},
		{"零用量不计费", 0, 490, string(UnitPerMillionTokens), 0},
		{"旧的千 token 单位按千换算", 3_300, 600, string(UnitPerThousandTokens), 1_980},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			credits, err := textTierCredits(item.tokens, item.price, item.unit)
			if err != nil {
				t.Fatalf("换算失败: %v", err)
			}
			if credits != item.expected {
				t.Fatalf("期望 %d，实际 %d", item.expected, credits)
			}
		})
	}
}

// TestTextTaskMinimumBalanceCoversOneRound 覆盖水位本身的口径。
//
// 水位要"估得住一轮"：按未命中输入价与输出价分别向上取整后相加，不给缓存命中打折。
// 估小了水位就白设，估大了会把只想问一句的用户挡在门外，所以这里把算式钉住。
func TestTextTaskMinimumBalanceCoversOneRound(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := newCreditTaskEnv(t)
	seedTextPrices(t, env, modelKey, 49, 490, 2450)

	minimum, err := env.service.TextTaskMinimumBalance(modelKey)
	if err != nil {
		t.Fatalf("读取水位失败: %v", err)
	}
	if minimum == nil {
		t.Fatal("配了三档价的模型应有水位")
	}
	// 用例把售价直接配成 490 / 2450：20000×490/1e6=9.8→10；2000×2450/1e6=4.9→5。
	// 线上这两行是"上游价 ×2"算出来的，水位会随之翻倍——水位跟着售价走，不另配一套数。
	if *minimum != 15 {
		t.Fatalf("水位应为 15 积分，实际 %d", *minimum)
	}
}

// TestTextTaskMinimumBalanceSkipsUnpricedModel 覆盖没有价目的模型。
//
// 未定价的模型会在预扣阶段被拒成 409，水位在这里既算不出来也没有意义——回一个 0 会让
// 调用方以为"这个模型不需要余额"。
func TestTextTaskMinimumBalanceSkipsUnpricedModel(t *testing.T) {
	env := newCreditTaskEnv(t)
	minimum, err := env.service.TextTaskMinimumBalance("unknown-model")
	if err != nil {
		t.Fatalf("读取水位失败: %v", err)
	}
	if minimum != nil {
		t.Fatalf("无价目不应给出水位，实际 %d", *minimum)
	}
}

// TestEnsureTextTaskBalanceRejectsBelowWatermark 覆盖提交前的水位校验。
//
// 这道门是"不再按 1 积分一轮对话"的另一半：结算只能在收尾时补钱，收尾时余额不够就只能
// 记账；水位把这件事提前到用户按下发送之前。
func TestEnsureTextTaskBalanceRejectsBelowWatermark(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := newCreditTaskEnv(t)
	seedTextPrices(t, env, modelKey, 49, 490, 2450)

	if err := env.service.EnsureTextTaskBalance("user-1", modelKey); err == nil {
		t.Fatal("零余额账号应被水位挡住")
	} else {
		var authErr *Error
		if !errorsAs(err, &authErr) || authErr.Status != 402 || authErr.Reason != "insufficient_credits" {
			t.Fatalf("水位不足应是 402 insufficient_credits，实际 %#v", err)
		}
		if !strings.Contains(authErr.Message, "15") {
			t.Fatalf("文案要点出需要多少余额，实际 %q", authErr.Message)
		}
	}

	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 14, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if err := env.service.EnsureTextTaskBalance("user-1", modelKey); err == nil {
		t.Fatal("水位差 1 积分也不应放行")
	}
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-2", 1, 0, "补足"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if err := env.service.EnsureTextTaskBalance("user-1", modelKey); err != nil {
		t.Fatalf("刚好到水位应放行，实际 %v", err)
	}
}

// TestQuoteTextStartPriceCarriesMinimumBalance 覆盖试算把水位一并发给前端。
//
// 试算只回起步价的话，前端只能显示"预计预扣 1 积分"，用户按下去才被 402 挡住——
// 面板说够、一按就被拒是最难解释的一类交互。
func TestQuoteTextStartPriceCarriesMinimumBalance(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := newCreditTaskEnv(t)
	seedTextPrices(t, env, modelKey, 49, 490, 2450)

	quote, err := env.service.QuoteTaskCharge(TaskChargeInput{ModelKey: modelKey, Capability: "TEXT"})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if quote.Credits != textStartPriceCredits {
		t.Fatalf("预扣仍是起步价，实际 %d", quote.Credits)
	}
	if quote.MinimumBalance != 15 {
		t.Fatalf("试算应带上水位 15，实际 %d", quote.MinimumBalance)
	}
}

// TestSettleTextTaskChargeRecordsUncollectedGap 覆盖余额不够时的欠款记账。
//
// 结果已经交付、任务已经成功，钱收不回来是业务事实而不是故障。它不能写进流水（余额不
// 允许为负），但必须在后台看得见——否则"少收了多少钱"永远查不出来。
func TestSettleTextTaskChargeRecordsUncollectedGap(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := newCreditTaskEnv(t)
	seedTextPrices(t, env, modelKey, 49, 490, 2450)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 1, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if _, _, err := env.service.ChargeTaskCredits("user-1", "task-1", textStartPriceCredits, "文本起步价"); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}

	quote, entry, created, err := env.service.SettleTextTaskCharge(TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 25_000, Output: 1_500},
	})
	if err != nil {
		t.Fatalf("余额不足不应报错（任务已经成功）：%v", err)
	}
	if quote.Credits != 17 || quote.Uncollected != 16 {
		t.Fatalf("应收 17、未收 16，实际 credits=%d uncollected=%d", quote.Credits, quote.Uncollected)
	}
	if created || entry != nil {
		t.Fatalf("收不回来的钱不应落流水，实际 created=%v entry=%#v", created, entry)
	}

	rows, total, err := env.service.AdminRecentSettleGaps(10)
	if err != nil {
		t.Fatalf("读取欠款失败: %v", err)
	}
	if len(rows) != 1 || rows[0].TaskID != "task-1" || rows[0].Uncollected != 16 {
		t.Fatalf("欠款清单应有一条 16 积分的记录，实际 %#v", rows)
	}
	if total != 16 {
		t.Fatalf("欠款合计应为 16，实际 %d", total)
	}

	// 重放收尾：同一条任务只留一条记录，且不会把已暴露的缺口改小（这里故意用更小的
	// 用量重放，MAX 语义下欠款仍是 16）。
	if _, _, _, err := env.service.SettleTextTaskCharge(TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 10_000, Output: 600},
	}); err != nil {
		t.Fatalf("重放结算不应报错: %v", err)
	}
	rows, total, err = env.service.AdminRecentSettleGaps(10)
	if err != nil {
		t.Fatalf("读取欠款失败: %v", err)
	}
	if len(rows) != 1 || total != 16 {
		t.Fatalf("重放不应新增欠款，实际 rows=%d total=%d", len(rows), total)
	}
}

// TestSettleTextTaskChargeClearsGapAfterTopUp 覆盖充值后重放把欠款清掉。
//
// 任务收尾可能被重放。第一次因余额不足记下的缺口，在用户充值后重放补扣成功时必须消失，
// 否则后台会一直挂着一条已经不存在的缺口，运营会去找用户要一笔已经收过的钱。
func TestSettleTextTaskChargeClearsGapAfterTopUp(t *testing.T) {
	const modelKey = "CHANNEL_000011::gpt-6-sol"
	env := newCreditTaskEnv(t)
	seedTextPrices(t, env, modelKey, 49, 490, 2450)
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 1, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if _, _, err := env.service.ChargeTaskCredits("user-1", "task-1", textStartPriceCredits, "文本起步价"); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
	input := TextSettleInput{
		UserID: "user-1", TaskID: "task-1", ModelKey: modelKey,
		Usage: TextTokenUsage{Input: 25_000, Output: 1_500},
	}
	if _, _, _, err := env.service.SettleTextTaskCharge(input); err != nil {
		t.Fatalf("首次结算失败: %v", err)
	}
	if _, total, err := env.service.AdminRecentSettleGaps(10); err != nil || total != 16 {
		t.Fatalf("应先记下 16 积分欠款，实际 total=%d err=%v", total, err)
	}

	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-2", 100, 0, "充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	quote, entry, created, err := env.service.SettleTextTaskCharge(input)
	if err != nil {
		t.Fatalf("充值后重放结算失败: %v", err)
	}
	if !created || entry == nil || entry.Amount != -16 {
		t.Fatalf("重放应补扣 16，实际 created=%v entry=%#v", created, entry)
	}
	if quote.Uncollected != 0 {
		t.Fatalf("补扣成功后不再有未收金额，实际 %d", quote.Uncollected)
	}
	rows, total, err := env.service.AdminRecentSettleGaps(10)
	if err != nil {
		t.Fatalf("读取欠款失败: %v", err)
	}
	if len(rows) != 0 || total != 0 {
		t.Fatalf("补扣成功后欠款应清空，实际 rows=%d total=%d", len(rows), total)
	}
}
