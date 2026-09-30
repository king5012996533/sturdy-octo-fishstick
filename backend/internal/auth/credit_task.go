package auth

import (
	"errors"
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
// Tier 是本次调用落在哪个价格档位（见 PriceTier）：图片取上游 quality 的 low/medium/high，
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
	Note       string
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
	Priced           bool   `json:"priced"`
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
	quote := &TaskChargeQuote{
		MultiplierBp:     resolution.MultiplierBp,
		MultiplierSource: resolution.Source,
		Unit:             unit,
		Quantity:         quantity,
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
	quote.Credits = unitPrice * quantity
	return quote, nil
}

// textStartPriceCredits 是文本任务在提交阶段的起步预扣（积分）。
//
// 1 积分是产品定的下限，不是算出来的价：文本的真实费用要等 token 用量回执才能结算，
// 详见 quoteTextStartPrice。
const textStartPriceCredits = 1

// quoteTextStartPrice 给出文本任务在提交阶段的起步价预扣。
//
// 文本按 token 结算，而 token 用量要等上游回执，所以提交时既定不了档位也定不了用量——
// "预扣"在这里只能是一个起步价。产品定的下限是每次 1 积分：它足以表达"这不是一次免费
// 调用"，又不会在按 token 结算上线之前扣住用户的大额余额。按 token 的真实结算需要任务侧
// 记录用量之后再做，届时这笔预扣由"结算退差"替换（见 docs/credits-billing.md）。
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
	startPrice := int64(textStartPriceCredits)
	return &TaskChargeQuote{
		Credits:          startPrice * quantity,
		SellUnitPrice:    &startPrice,
		MultiplierBp:     markupBaseBp,
		MultiplierSource: markupSourceDefault,
		Unit:             string(UnitPerRequest),
		Quantity:         quantity,
		Priced:           true,
	}, nil
}

// ChargeTask 预扣一次任务，返回试算结果、流水与"是否新建"。
//
// 未定价直接拒绝，不放行也不免费：静默按 0 元出货，等到对账时才发现某批模型一直在白送，
// 是这类系统里最难追溯的一种损失。运营想让某个模型免费，就把它的售价显式配成 0——
// 定价域里"未定价"与"免费"本来就是两个可区分的状态。
func (s *Service) ChargeTask(input TaskChargeInput) (*TaskChargeQuote, *CreditLedgerEntryView, bool, error) {
	quote, err := s.QuoteTaskCharge(input)
	if err != nil {
		return nil, nil, false, err
	}
	if !quote.Priced {
		return nil, nil, false, pricingMissing(strings.TrimSpace(input.ModelKey), input.Tier)
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
