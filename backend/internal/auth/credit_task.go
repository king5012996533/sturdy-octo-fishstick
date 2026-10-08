package auth

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 任务计费：把"这个模型这次调用该收多少"接到积分域上。
//
// 本文件是定价域与积分域之间唯一的桥。放在 auth 而不是 app，是因为两个关键判断都只能
// 在这里做对：单价来自本域的定价表，余额来自本域的积分账户——散到调用方去算，就会出现
// "后台显示 1.2 倍、实扣按 1 倍"和"两个入口各算一次余量"这两类只能靠对账发现的问题。

// TaskChargeInput 是一次任务计费的入参。
//
// Quantity 是本次用量，单位由模型的 Unit 决定：图片按张、视频与音频按秒、文本按百万 token。
// 用量未知时传 0，由本层回退成 1 个单位——"按次计费"是这类模型的常态，把 0 当成 0 元
// 会让一次真实调用白送。
//
// Tier 是本次调用落在哪个价格档位（见 PriceTier）：图片取上游 quality 的档位，
// 没有质量维度时留空。它是取价的第三个维度，缺了就只能取到"不区分档位"那一行——
// 对分档计费的模型，那一行要么不存在（拒绝，正确），要么是运营显式配的兜底价。
type TaskChargeInput struct {
	UserID     string
	TaskID     string
	ModelKey   string
	VendorCode string
	Capability string
	Tier       string
	Quantity   int64
	// SurchargeCredits 是与用量无关的附加费（积分），加在"单价 × 用量"之外结算。
	// 目前唯一的来源是参考图超出免费额度后的按张加收：模型按秒计价，张数没法表达成秒，
	// 硬折算会让同一件事在不同档位上算出不同的价。
	SurchargeCredits int64
	// SurchargeNote 说明这笔附加费是什么，会拼进流水备注。
	SurchargeNote string
	Note          string
}

// TaskChargeQuote 是一次任务计费的试算结果。
//
// Credits 是 Quantity 个单位的总额；SellUnitPrice 是单价，两者一起回传是为了让
// 账单页能写出"450 积分 = 15 秒 × 30 积分/秒"这种用户能自己复核的算式。
type TaskChargeQuote struct {
	Credits          int64  `json:"credits"`
	SellUnitPrice    *int64 `json:"sellUnitPrice"`
	MultiplierBp     int    `json:"multiplierBp"`
	MultiplierSource string `json:"multiplierSource"`
	Unit             string `json:"unit"`
	Quantity         int64  `json:"quantity"`
	// SurchargeCredits 是总额里"单价 × 用量"之外的那部分，前端据此说明差额。
	SurchargeCredits int64 `json:"surchargeCredits"`
	Priced           bool  `json:"priced"`
	// MinimumBalance 是这次提交需要保留的最低余额（积分），0 表示没有这条要求。
	//
	// 只有文本会给出非零值：它的真实费用要等用量回执，提交时扣不动，只能在放行前要求
	// 余额够跑一轮。它与 Credits 是两件事——Credits 是"这次扣多少"，MinimumBalance 是
	// "余额得有多少才允许开始"。前端要分开显示，把水位说成价格会让用户以为一次对话要花
	// 那么多少。
	MinimumBalance int64 `json:"minimumBalance"`
}

// pricingMissing 是模型还没定价时的对外错误。
//
// 用 failed_precondition 而不是"余额不足"：这两件事的下一步动作完全不同——一个要找
// 管理员配价，一个要去充值。合成同一个错误会让用户在充值页反复付款却依然生成不了。
//
// 档位必须出现在文案里：分档计费的模型常常是"配了两档、漏了一档"，只报模型名会让
// 管理员反复确认一个明明已经配好的模型。
func pricingMissing(modelKey string, tier string) *Error {
	subject := "模型「" + modelKey + "」"
	if normalized := normalizePriceTier(tier); normalized != string(PriceTierNone) {
		subject += "的 " + normalized + " 档"
	}
	return &Error{
		Status:  409,
		Code:    409,
		Reason:  "failed_precondition",
		Message: subject + "尚未定价，暂时无法生成；请联系管理员在后台配置单价",
	}
}

// QuoteTaskCharge 试算一次任务要扣多少积分，不写库。
func (s *Service) QuoteTaskCharge(input TaskChargeInput) (*TaskChargeQuote, error) {
	capability := normalizeModelCapability(input.Capability)
	if !validModelCapability(capability) {
		return nil, invalidArgument("模型能力只能是 TEXT / IMAGE / VIDEO / AUDIO")
	}
	modelKey := strings.TrimSpace(input.ModelKey)
	if modelKey == "" {
		return nil, invalidArgument("缺少模型标识")
	}
	quantity := input.Quantity
	if quantity <= 0 {
		quantity = 1
	}

	tier := normalizePriceTier(input.Tier)
	if !validPriceTier(capability, tier) {
		// 文本的 token 档位（缓存命中 / 未命中 / 输出）要等上游回执才知道，提交时必然是空档。
		// 这不是"档位填错了"，而是"这一步定不了价"，所以走起步价预扣而不是报 400——
		// 400 是给管理员看"你配错了"的口径，拿它去回一个正常提交的用户只会把人引去改配置。
		if capability == string(CapabilityText) && tier == string(PriceTierNone) {
			return s.quoteTextStartPrice(modelKey, capability, quantity)
		}
		return nil, invalidArgument(priceTierRequirementMessage(capability))
	}
	// 取价必须带上档位：分档计费的模型在库里是多行，按"不区分档位"去取只会拿到
	// 那唯一一行（多半不存在），然后一次真实调用会被当成"尚未定价"拒掉或按错价成交。
	var price *ModelPrice
	found, err := s.store.ModelPriceByTier(modelKey, capability, tier)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, internalFailure(err)
	}
	if err == nil {
		price = found
	}
	if price == nil && tierFallsBackToUntiered(capability, tier) {
		// 尺寸档（图片）与分辨率档（视频）都是我们自己加的轴，不是上游的必填维度：模型没给
		// 这一档配价，说明它不按这条轴分档，按"不区分档位"那一行结算即可。没有这条回落，
		// 给某个模型加一档分辨率就会把其余档位打成未定价——那是一次全量报价失败，而不是
		// 一个可以慢慢补的配置缺口。
		if fallback, fallbackErr := s.store.ModelPriceByTier(modelKey, capability, string(PriceTierNone)); fallbackErr == nil {
			price = fallback
		}
	}
	rules, err := s.store.MarkupRules()
	if err != nil {
		return nil, internalFailure(err)
	}

	resolution := ResolvePricing(PricingInput{
		ModelKey:   modelKey,
		VendorCode: strings.TrimSpace(input.VendorCode),
		Capability: capability,
		PriceTier:  tier,
		// 上游成本取自定价行本身：调用方手上只有"这次要生成什么"，报价属于本域数据。
		UpstreamUnitPrice: upstreamUnitPriceOf(price),
	}, price, rules)

	unit := unitOf(price, capability)
	// 按条计费的视频用量恒为 1。上游有一类模型是"一条一个价"、与时长无关（纵横科技的
	// TTP-grok 就是：6 秒与 15 秒同价），这类价目在后台配成"次"。用量本身由能力决定，
	// 视频取的是 videoSeconds，不在这里归位的话，一条 180 分的视频会被算成 180 × 15 分——
	// 账单上写着"180分/条"，扣的却是按秒的钱，用户按算式复核必然对不上。
	if PriceUnit(unit) == UnitPerRequest && capability == string(CapabilityVideo) {
		quantity = 1
	}
	surcharge := input.SurchargeCredits
	if surcharge < 0 {
		surcharge = 0
	}
	quote := &TaskChargeQuote{
		MultiplierBp:     resolution.MultiplierBp,
		MultiplierSource: resolution.Source,
		Unit:             unit,
		Quantity:         quantity,
		SurchargeCredits: surcharge,
		Priced:           resolution.Priced,
	}
	if !resolution.Priced || resolution.SellUnitPrice == nil {
		return quote, nil
	}
	unitPrice := *resolution.SellUnitPrice
	quote.SellUnitPrice = &unitPrice
	// 溢出必须在相乘之前挡住：单价与用量都来自运营配置，一个多按一位的单价乘上一个
	// 正常的用量就能把总额绕成负数，而负数在积分域里的含义是"给用户打钱"。
	if unitPrice > 0 && quantity > math.MaxInt64/unitPrice {
		return nil, invalidArgument("本次调用金额过大，请降低时长或数量后重试")
	}
	credits := unitPrice * quantity
	// 附加费同样要在相加前挡溢出：负数在积分域里的含义是"给用户打钱"。
	if credits > math.MaxInt64-surcharge {
		return nil, invalidArgument("本次调用金额过大，请降低时长或数量后重试")
	}
	quote.Credits = credits + surcharge
	return quote, nil
}

// textStartPriceCredits 是文本任务在提交阶段的起步预扣（积分）。
//
// 1 积分是产品定的下限，不是算出来的价：文本的真实费用要等 token 用量回执才能结算，
// 详见 quoteTextStartPrice。
const textStartPriceCredits = 1

// 文本任务的"跑一轮大概要多少"估计值，用来定提交前的最低余额水位。
//
// 取"常规一轮"而不是"最重一轮"：水位定得太高会把余额充足、只想再问一句的用户挡在门外，
// 而它的作用只是保证结算有东西可扣，不是给一轮会话定价。生产实测的常规一轮在 1–2 万
// 输入、几百到几千输出之间（见 docs/credits-billing.md），这里按输入不命中缓存估——
// 真实会话的缓存命中率不可预测，给它打折是"算少"的那一侧，水位就白设了。
//
// 超出一轮估计的会话由欠款清单兜底（credit_settle_gaps）：那部分收不回来时后台看得见，
// 而不是靠把水位抬高到最坏情况来预防——那等于向所有用户预收最坏情况的钱。
//
// 这两个数只影响"允不允许开始"，不影响实际扣费：真实费用照旧按 token 用量结算。
const (
	textMinimumInputTokens  = 20_000
	textMinimumOutputTokens = 2_000
)

// textMinimumBalance 按价目算出一轮文本会话所需的余额水位。
//
// 返回 nil 表示这个模型没有任何可用的文本价目：此时不给水位，未定价的模型会在预扣阶段
// 被直接拒成 409，水位在这里既算不出来、也没有意义。
func (s *Service) textMinimumBalance(prices map[string]*ModelPrice, rules []MarkupRule) (*int64, error) {
	tiers := []struct {
		tier   PriceTier
		tokens int64
	}{
		{PriceTierInput, textMinimumInputTokens},
		{PriceTierOutput, textMinimumOutputTokens},
	}
	total := int64(0)
	priced := false
	for _, item := range tiers {
		price := prices[string(item.tier)]
		if price == nil {
			continue
		}
		resolution := ResolvePricing(PricingInput{
			ModelKey:          price.ModelKey,
			VendorCode:        strings.TrimSpace(price.VendorCode),
			Capability:        string(CapabilityText),
			PriceTier:         string(item.tier),
			UpstreamUnitPrice: upstreamUnitPriceOf(price),
		}, price, rules)
		if !resolution.Priced || resolution.SellUnitPrice == nil || *resolution.SellUnitPrice <= 0 {
			continue
		}
		credits, err := textTierCredits(item.tokens, *resolution.SellUnitPrice, unitOf(price, string(CapabilityText)))
		if err != nil {
			return nil, err
		}
		total += credits
		priced = true
	}
	if !priced {
		return nil, nil
	}
	return &total, nil
}

// TextTaskMinimumBalance 读出一个文本模型的最低余额水位，nil 表示不设水位。
func (s *Service) TextTaskMinimumBalance(modelKey string) (*int64, error) {
	prices, err := s.store.ModelPricesByCapability(modelKey, string(CapabilityText))
	if err != nil {
		return nil, internalFailure(err)
	}
	rules, err := s.store.MarkupRules()
	if err != nil {
		return nil, internalFailure(err)
	}
	minimum, err := s.textMinimumBalance(prices, rules)
	if err != nil {
		return nil, err
	}
	return minimum, nil
}

// EnsureTextTaskBalance 校验一次文本任务提交是否够得上最低余额水位。
//
// 为什么需要它：文本的预扣只是一个起步价，真实费用要等用量回执。若不加这道门，用户可以
// 用 1 积分反复发起会话，每一轮的真实成本都由平台垫——这不是"用户欠费"，是平台在无人
// 察觉的情况下持续漏收。水位把这件事提前到"按下发送之前"，代价是余额不足的用户会被挡
// 住，因此文案必须说清要求多少、当前多少。
//
// 只补位不替代结算：通过水位的会话仍然按真实用量结算，水位不是价格。
func (s *Service) EnsureTextTaskBalance(userID string, modelKey string) error {
	minimum, err := s.TextTaskMinimumBalance(modelKey)
	if err != nil {
		return err
	}
	if minimum == nil || *minimum <= 0 {
		return nil
	}
	account, err := s.store.CreditAccountFor(userID)
	if err != nil {
		return internalFailure(err)
	}
	balance := int64(0)
	if account != nil {
		balance = account.Balance
	}
	if balance >= *minimum {
		return nil
	}
	return insufficientCredits(fmt.Sprintf(
		"文本会话按实际用量计费，余额需保持在 %d 积分以上（当前 %d 积分），请先充值",
		*minimum, balance,
	))
}

// quoteTextStartPrice 给出文本任务在提交阶段的起步价预扣。
//
// 文本按 token 结算，而 token 用量要等上游回执，所以提交时既定不了档位也定不了用量——
// "预扣"在这里只能是一个起步价。产品定的下限是每次 1 积分：它足以表达"这不是一次免费
// 调用"，又不会在拿到用量之前扣住用户的大额余额。真实费用由任务成功收尾时的按 token
// 结算补上差额（SettleTextTaskCharge），起步价算在总额之内，见 docs/credits-billing.md。
//
// 起步价只在模型确实配过文本价目时生效。否则一个拼错的模型标识会变成一条永远免费的
// 通道——"未定价不给生成"是这类系统里最该保住的一条规矩，文本不该是它的例外。
func (s *Service) quoteTextStartPrice(modelKey string, capability string, quantity int64) (*TaskChargeQuote, error) {
	prices, err := s.store.ModelPricesByCapability(modelKey, capability)
	if err != nil {
		return nil, internalFailure(err)
	}
	if len(prices) == 0 {
		// 没有价目：回一个未定价的报价，由 ChargeTask 统一拒成 409。
		return &TaskChargeQuote{Unit: unitOf(nil, capability), Quantity: quantity}, nil
	}
	rules, err := s.store.MarkupRules()
	if err != nil {
		return nil, internalFailure(err)
	}
	minimum, err := s.textMinimumBalance(prices, rules)
	if err != nil {
		return nil, err
	}
	startPrice := int64(textStartPriceCredits)
	quote := &TaskChargeQuote{
		Credits:          startPrice * quantity,
		SellUnitPrice:    &startPrice,
		MultiplierBp:     markupBaseBp,
		MultiplierSource: markupSourceDefault,
		Unit:             string(UnitPerRequest),
		Quantity:         quantity,
		Priced:           true,
	}
	if minimum != nil {
		quote.MinimumBalance = *minimum
	}
	return quote, nil
}

// QuoteTask 试算一次任务的消耗，未定价时返回与提交相同的 409。
//
// 生成前的"预计消耗"必须与真正预扣走同一条取价路径：各写一份的话，报价会在取价档位、
// 倍率或单位上与实扣分叉，而"面板说 30 积分、扣款却扣 45"是用户最不能接受的一类错误。
// 未定价在这里同样报 409 而不是回一个 0：让用户以为这次免费，比让他直接看到配价缺失更糟。
//
// 与 QuoteTaskCharge 的区别只有一点——它把"未定价"也变成一个错误，因此调用方（提交与
// 试算接口）不需要各自再判一次 priced。
func (s *Service) QuoteTask(input TaskChargeInput) (*TaskChargeQuote, error) {
	quote, err := s.QuoteTaskCharge(input)
	if err != nil {
		return nil, err
	}
	if !quote.Priced {
		return nil, pricingMissing(strings.TrimSpace(input.ModelKey), input.Tier)
	}
	return quote, nil
}

// ChargeTask 预扣一次任务，返回试算结果、流水与"是否新建"。
//
// 未定价直接拒绝，不放行也不免费：静默按 0 元出货，等到对账时才发现某批模型一直在白送，
// 是这类系统里最难追溯的一种损失。运营想让某个模型免费，就把它的售价显式配成 0——
// 定价域里"未定价"与"免费"本来就是两个可区分的状态。
func (s *Service) ChargeTask(input TaskChargeInput) (*TaskChargeQuote, *CreditLedgerEntryView, bool, error) {
	quote, err := s.QuoteTask(input)
	if err != nil {
		return nil, nil, false, err
	}
	// 售价为 0 是"免费"，但零额变动本身被积分域拒绝（它不改变余额，只会污染流水），
	// 因此这里直接返回免费结果，不落流水。
	if quote.Credits == 0 {
		return quote, nil, false, nil
	}
	entry, created, err := s.ChargeTaskCredits(input.UserID, input.TaskID, quote.Credits, taskChargeNote(input, quote))
	if err != nil {
		return nil, nil, false, err
	}
	return quote, entry, created, nil
}

// RefundTaskCharge 退回一次任务预扣，供失败与取消路径调用。
//
// 退多少从流水里读，不由调用方传：调用方手上只有"这个任务失败了"，让它自己算金额，
// 上游调价之后就会退成一个不再等于当初扣款的值。返回 refunded=false 表示没有可退的
// 东西（任务免费，或这笔预扣早已退过），这不是错误——失败路径会被重放，第二次退
// 理应是空操作。
func (s *Service) RefundTaskCharge(userID string, taskID string, note string) (int64, bool, error) {
	charge, err := s.store.CreditEntryByRef(userID, CreditKindCharge, CreditRefTask, taskID)
	if err != nil {
		return 0, false, internalFailure(err)
	}
	if charge == nil || charge.Amount >= 0 {
		return 0, false, nil
	}
	credits := -charge.Amount
	_, created, err := s.RefundTaskCredits(userID, taskID, credits, note)
	if err != nil {
		return 0, false, err
	}
	return credits, created, nil
}

// TextTokenUsage 是一次文本任务在上游实际消耗的 token。
//
// 三档与定价表的 PriceTier 一一对应：Cached 是输入里命中提示词缓存的那部分，Input 是
// 输入总量（含命中部分），Output 是模型生成的 token。Input 减去 Cached 才是按"未命中"
// 计费的那部分——两档价格差着一个量级，直接拿 Input 当未命中会把这笔钱按最贵的档收。
type TextTokenUsage struct {
	Input  int64
	Cached int64
	Output int64
}

// TextSettleInput 是一次文本任务的按用量结算入参。
type TextSettleInput struct {
	UserID   string
	TaskID   string
	ModelKey string
	Usage    TextTokenUsage
}

// TextSettleQuote 是结算读数：按实际用量算出的总价、提交时已预扣的部分，以及本次补扣额。
type TextSettleQuote struct {
	// Credits 是按实际 token 用量算出的总价。
	Credits int64
	// Charged 是提交时已经预扣的起步价。
	Charged int64
	// Delta 是本次补扣额（恒为非负）。0 表示起步价已经盖住实际用量，不落流水。
	Delta int64
	// Priced 为 false 表示这个模型没有任何文本价目，无法结算。
	Priced bool
	// MissingTiers 是缺价的档位。非空表示这次结算少算了钱，调用方必须落日志。
	MissingTiers []string
	// Uncollected 是余额不够、这次收不回来的差额（积分）。非零表示已记入后台欠款清单。
	Uncollected int64
	Note        string
}

// SettleTextTaskCharge 按上游回执的真实 token 用量给一次文本任务结算补扣。
//
// 为什么预扣之外还要有这一步：文本的档位（缓存命中 / 未命中 / 输出）与用量都要等上游
// 回执才知道，提交时只能按起步价预扣。少了结算，一次带着几十万 token 上下文的 Agent
// 会话会按起步价成交，平台每跑一轮亏一轮，而且亏得越多越看不出来——账单上那一行与
// 实际情况一样"正常"。
//
// 只补扣、不退款：预扣的那 1 积分是产品定的起步价（见 textStartPriceCredits），不是
// 押金。差额为负时按起步价成交，这样这条路径恒为出账，与失败退款路径不会互相干扰，
// 也不会出现"退了一笔又补扣一笔"的对账噪音。
//
// 幂等由 (user_id, kind=TASK_SETTLE, ref_type=TASK, ref_id=taskID) 的唯一索引保证：
// 任务收尾可能被重放，重放时补扣只会命中已有流水。
func (s *Service) SettleTextTaskCharge(input TextSettleInput) (*TextSettleQuote, *CreditLedgerEntryView, bool, error) {
	userID := strings.TrimSpace(input.UserID)
	taskID := strings.TrimSpace(input.TaskID)
	modelKey := strings.TrimSpace(input.ModelKey)
	if userID == "" || taskID == "" {
		return nil, nil, false, invalidArgument("文本结算缺少账号或任务标识")
	}
	if modelKey == "" {
		return nil, nil, false, invalidArgument("文本结算缺少模型标识")
	}
	usage := input.Usage
	if usage.Input < 0 || usage.Cached < 0 || usage.Output < 0 {
		return nil, nil, false, invalidArgument("文本结算的 token 用量不能为负数")
	}
	// 上游回执偶尔会给出 cached 大于 input 的组合（缓存统计含上一轮的提示词）。夹回
	// 去而不是报错：报错会让整条结算路径停摆，而它本来就只是一次账面修正。
	if usage.Cached > usage.Input {
		usage.Cached = usage.Input
	}

	quote := &TextSettleQuote{Priced: false}
	charge, err := s.store.CreditEntryByRef(userID, CreditKindCharge, CreditRefTask, taskID)
	if err != nil {
		return nil, nil, false, internalFailure(err)
	}
	if charge != nil && charge.Amount < 0 {
		quote.Charged = -charge.Amount
	}

	rules, err := s.store.MarkupRules()
	if err != nil {
		return nil, nil, false, internalFailure(err)
	}
	tiers := []struct {
		tier   PriceTier
		tokens int64
	}{
		{PriceTierCache, usage.Cached},
		{PriceTierInput, usage.Input - usage.Cached},
		{PriceTierOutput, usage.Output},
	}
	credits := int64(0)
	found := 0
	formula := make([]string, 0, len(tiers))
	for _, item := range tiers {
		price, priceErr := s.store.ModelPriceByTier(modelKey, string(CapabilityText), string(item.tier))
		if priceErr != nil && !errors.Is(priceErr, ErrNotFound) {
			return nil, nil, false, internalFailure(priceErr)
		}
		if price == nil {
			// 缺一档不能按 0 悄悄放过：那一档的钱要么由平台垫，要么会在对账时才被发现。
			// 记进 MissingTiers 由调用方落日志，结算本身继续——把已经能算的部分算完，
			// 比整笔不算更接近真实成本。
			quote.MissingTiers = append(quote.MissingTiers, string(item.tier))
			continue
		}
		found++
		resolution := ResolvePricing(PricingInput{
			ModelKey: modelKey, VendorCode: strings.TrimSpace(price.VendorCode),
			Capability: string(CapabilityText), PriceTier: string(item.tier),
			UpstreamUnitPrice: upstreamUnitPriceOf(price),
		}, price, rules)
		if !resolution.Priced || resolution.SellUnitPrice == nil {
			quote.MissingTiers = append(quote.MissingTiers, string(item.tier))
			continue
		}
		unitCredits, unitErr := textTierCredits(item.tokens, *resolution.SellUnitPrice, unitOf(price, string(CapabilityText)))
		if unitErr != nil {
			return nil, nil, false, unitErr
		}
		credits += unitCredits
		formula = append(formula, fmt.Sprintf("%s %d×%d", textTierLabel(item.tier), item.tokens, *resolution.SellUnitPrice))
	}
	if found == 0 {
		// 一条价目都没有：这个模型本来就不该走到计费，交回调用方按"未定价"处理。
		return quote, nil, false, nil
	}
	quote.Priced = true
	quote.Credits = credits
	quote.Delta = credits - quote.Charged
	if quote.Delta <= 0 {
		quote.Delta = 0
		return quote, nil, false, nil
	}
	quote.Note = fmt.Sprintf("文本按 token 结算：%s（分/百万token），补扣 %d 分", strings.Join(formula, " + "), quote.Delta)
	entry, created, err := s.ApplyCreditMutations([]CreditMutation{{
		UserID:  userID,
		Kind:    CreditKindSettle,
		Amount:  -quote.Delta,
		RefType: CreditRefTask,
		RefID:   taskID,
		Note:    strings.TrimSpace(modelKey + " " + quote.Note),
	}})
	if err != nil {
		// 余额不足不是故障，而是"这轮的钱收不回来"这个业务事实：用户已经拿到结果，
		// 任务不会因为钱不够而被撤销。把它记进欠款清单并照常返回，让调用方能在后台看到
		// 缺口；当成错误抛出去只会让一条已经成功的任务在日志里变成一次"结算失败"。
		if isInsufficientCreditsError(err) {
			quote.Uncollected = quote.Delta
			if recordErr := s.store.RecordCreditSettleGap(CreditSettleGap{
				UserID:      userID,
				TaskID:      taskID,
				ModelKey:    modelKey,
				Uncollected: quote.Delta,
			}); recordErr != nil {
				return quote, nil, false, internalFailure(recordErr)
			}
			return quote, nil, false, nil
		}
		return quote, nil, false, err
	}
	// 补扣成功就把同一条任务的欠款记录清掉：任务收尾会被重放，第一次因余额不足记下的
	// 缺口在用户充值后重放补扣成功时必须消失，否则后台会一直挂着一条不存在的缺口。
	if clearErr := s.store.ClearCreditSettleGap(taskID); clearErr != nil {
		return quote, &entry[0], created[0], internalFailure(clearErr)
	}
	return quote, &entry[0], created[0], nil
}

// textTierCredits 把某一档的 token 用量换算成积分，向上取整到分。
//
// 单位由定价行的 Unit 决定而不是写死百万：库里有历史遗留的"分 / 千 token"行，按百万
// 换算会把它放大一千倍。向上取整发生在每一档：先求和再取整会让三档各自的小数部分
// 互相抵消，长期下来每次结算都少收一点点。
func textTierCredits(tokens int64, sellUnitPrice int64, unit string) (int64, error) {
	if tokens <= 0 || sellUnitPrice <= 0 {
		return 0, nil
	}
	denominator := int64(1_000_000)
	if PriceUnit(unit) == UnitPerThousandTokens {
		denominator = 1_000
	}
	if tokens > math.MaxInt64/sellUnitPrice {
		return 0, invalidArgument("本次调用的 token 用量过大，无法结算")
	}
	total := tokens * sellUnitPrice
	credits := total / denominator
	if total%denominator != 0 {
		credits++
	}
	return credits, nil
}

func textTierLabel(tier PriceTier) string {
	switch tier {
	case PriceTierCache:
		return "缓存命中"
	case PriceTierInput:
		return "输入"
	case PriceTierOutput:
		return "输出"
	default:
		return string(tier)
	}
}

// upstreamUnitPriceOf 取定价行上的上游成本，行不存在时返回 nil。
func upstreamUnitPriceOf(price *ModelPrice) *int64 {
	if price == nil {
		return nil
	}
	return price.UpstreamUnitPrice
}

// unitOf 取计费单位标签，缺行时按能力给出默认值。
func unitOf(price *ModelPrice, capability string) string {
	if price != nil && strings.TrimSpace(price.Unit) != "" {
		return price.Unit
	}
	return string(DefaultUnitFor(ModelCapability(capability)))
}

// taskChargeNote 生成流水备注。
//
// 备注要能独立回答"这笔钱是怎么来的"，因为账单页上它与任务详情是分开渲染的：
// 只写模型名，用户看到一行 -450 却无从复核；带上单价与用量，他就能自己乘一遍。
func taskChargeNote(input TaskChargeInput, quote *TaskChargeQuote) string {
	parts := []string{strings.TrimSpace(input.ModelKey)}
	if quote != nil && quote.SellUnitPrice != nil {
		parts = append(parts, chargeFormula(*quote.SellUnitPrice, quote.Unit, quote.Quantity))
	}
	if quote != nil && quote.SurchargeCredits > 0 {
		label := strings.TrimSpace(input.SurchargeNote)
		if label == "" {
			label = "附加费"
		}
		parts = append(parts, label)
	}
	if note := strings.TrimSpace(input.Note); note != "" {
		parts = append(parts, note)
	}
	return strings.Join(parts, " ")
}

// chargeFormula 把"单价 × 用量"渲染成用户能自己复核的算式。
//
// 用中文单位而不是 TOKEN_1M / IMAGE 这类枚举名：账单是给用户看的，"600分/REQUEST × 1"
// 他看不出这是一条视频的钱。按条计费时用量恒为 1，写出来只是噪音，整段省略。
func chargeFormula(unitPrice int64, unit string, quantity int64) string {
	label := strings.TrimSpace(unit)
	switch PriceUnit(unit) {
	case UnitPerMillionTokens:
		label = "百万token"
	case UnitPerThousandTokens:
		label = "千token"
	case UnitPerImage:
		label = "张"
	case UnitPerSecond:
		label = "秒"
	case UnitPerRequest:
		return strconv.FormatInt(unitPrice, 10) + "分/条"
	}
	return strconv.FormatInt(unitPrice, 10) + "分/" + label + " × " + strconv.FormatInt(quantity, 10)
}
