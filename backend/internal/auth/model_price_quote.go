package auth

import "strings"

// ModelPriceQuote 是一条"对外可展示"的档位售价。
//
// 刻意不带上游成本：广场、活动页这类地方是给用户看的，上游进价属于经营信息，多留一个
// 字段就会顺着某个"顺手复用一下"的接口漏出去。倍率同理——用户只需要知道卖多少钱。
type ModelPriceQuote struct {
	PriceTier     string `json:"priceTier"`
	Unit          string `json:"unit"`
	SellUnitPrice *int64 `json:"sellUnitPrice"`
	// Priced=false 表示这一档还没定价，页面必须显示成"暂不可用"而不是 0（0 是真的免费）。
	Priced bool `json:"priced"`
}

// ModelPriceQuotes 按模型标识返回各档位售价，键与入参的模型标识一致。
//
// 解析走的是与扣费完全相同的路径——同一批价目行、同一份倍率规则、同一个 ResolvePricing，
// 所以广场上写的价和用户点下去实际扣的价不可能分叉。这是"页面一个价、账单另一个价"的
// 唯一防法：任何在展示侧重新乘一遍倍率的实现，都会在改倍率那天开始说两种话。
//
// 只返回启用中的行；没有任何价目的模型不会出现在结果里，调用方据此判断"这个模型现在
// 能不能卖"，而不是把未定价的模型当成免费或可用。
func (s *Service) ModelPriceQuotes(modelKeys []string) (map[string][]ModelPriceQuote, error) {
	wanted := make(map[string]struct{}, len(modelKeys))
	for _, key := range modelKeys {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			wanted[trimmed] = struct{}{}
		}
	}
	result := make(map[string][]ModelPriceQuote, len(wanted))
	if len(wanted) == 0 {
		return result, nil
	}

	prices, err := s.store.ModelPrices()
	if err != nil {
		return nil, internalFailure(err)
	}
	rules, err := s.store.MarkupRules()
	if err != nil {
		return nil, internalFailure(err)
	}

	for _, price := range prices {
		if !price.Enabled {
			continue
		}
		modelKey := strings.TrimSpace(price.ModelKey)
		if _, ok := wanted[modelKey]; !ok {
			continue
		}
		resolution := ResolvePricing(PricingInput{
			ModelKey:          modelKey,
			VendorCode:        price.VendorCode,
			Capability:        price.Capability,
			PriceTier:         price.PriceTier,
			UpstreamUnitPrice: price.UpstreamUnitPrice,
		}, &price, rules)
		result[modelKey] = append(result[modelKey], ModelPriceQuote{
			PriceTier:     price.PriceTier,
			Unit:          price.Unit,
			SellUnitPrice: resolution.SellUnitPrice,
			Priced:        resolution.Priced,
		})
	}

	for key := range result {
		sortModelPriceQuotes(result[key])
	}
	return result, nil
}

// sortModelPriceQuotes 按档位从便宜到贵排列。
//
// 顺序写死在这里，是因为页面按这个顺序渲染价格表：低档在前，用户一眼看到的是入门价，
// 而运营在后台填价的顺序本来就不可控（先补 high 再补 low 是常态）。
func sortModelPriceQuotes(quotes []ModelPriceQuote) {
	order := func(tier string) int {
		switch PriceTier(tier) {
		case PriceTierCache, PriceTierShort:
			return 0
		case PriceTierLow, PriceTierMedium, PriceTierInput:
			return 1
		case PriceTierHigh:
			return 2
		case PriceTierXHigh:
			return 3
		case PriceTierMax, PriceTierOutput, PriceTierLong:
			return 4
		default:
			// 空档排在最后：它代表"未指定质量/时长"的兜底价，不是任何一档的别名。
			return 5
		}
	}
	for i := 1; i < len(quotes); i++ {
		for j := i; j > 0 && order(quotes[j].PriceTier) < order(quotes[j-1].PriceTier); j-- {
			quotes[j], quotes[j-1] = quotes[j-1], quotes[j]
		}
	}
}
