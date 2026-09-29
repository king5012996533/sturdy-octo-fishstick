package auth

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 倍率解析引擎。
//
// 纯函数、无 IO：后台表单的"试算"与将来真正落地的扣费必须共用同一段判定，否则会出现
// "预览显示 1.2 倍、实扣按 1 倍"这种事后无法复现的差额。倍率的来源（模型专属配置、
// 四级规则、默认值）与售价的算法都在这里收敛成一个解析结果。

// errMultiplierRange 是倍率越界时统一返回的原因。
//
// 单独抽出来是为了让单价保存、规则保存与表单解析三处给出同一句提示：各写一份文案，
// 运营就会以为自己填错的是三件不同的事。
var errMultiplierRange = errors.New("倍率需在 0.0001 到 1000 之间")

// markupSourceDefault 是没有命中任何规则时的来源标记。
const markupSourceDefault = "DEFAULT"

// PricingInput 是一次试算的入参。
//
// UpstreamUnitPrice 可空：还没拿到上游报价时留空，解析结果里的售价也是 null，而不是 0。
// Capability 用大写能力值，留空表示"不区分能力"（能力维度的规则不会命中）。
type PricingInput struct {
	ModelKey   string `json:"modelKey"`
	VendorCode string `json:"vendorCode"`
	Capability string `json:"capability"`
	// TokenTier 只在文本能力下有值：文本的三档价在库里是三行，不指定档位就查不到价，
	// 试算会显示"未定价"——那是对的，文本本来就没有"一个价"这回事。
	TokenTier string `json:"tokenTier"`
	// UpstreamUnitPrice 是上游成本，单位随配置的 Unit；可空。
	UpstreamUnitPrice *int64 `json:"upstreamUnitPrice"`
}

// PricingResolution 是一次解析的结果。
//
// Priced=false 时 SellUnitPrice 必为 nil：前端据此显示"未定价"，而不是把 0 当成免费。
type PricingResolution struct {
	// MultiplierBp 是实际生效的倍率（万分比）。非法倍率回落到 10000。
	MultiplierBp int `json:"multiplierBp"`
	// Source 说明倍率来自哪一级：MODEL / VENDOR / CAPABILITY / GLOBAL / DEFAULT。
	// 倍率非法时在后面追加 :INVALID。
	Source string `json:"source"`
	// SellUnitPrice 是算出的售价，可空；nil = 上游价未知且没有直接定价。
	SellUnitPrice *int64 `json:"sellUnitPrice"`
	// Priced 表示是否已经能算出售价。
	Priced bool `json:"priced"`
}

// ResolvePricing 解析某次调用最终生效的倍率与售价。
//
// price 可以为 nil（这个模型还没建配置），rules 为 nil 表示一条规则都没有；两者都不
// 会让解析失败——"还没定价"也是一种正常的业务状态，报错会把它变成故障。
func ResolvePricing(input PricingInput, price *ModelPrice, rules []MarkupRule) PricingResolution {
	multiplierBp, source := resolveMultiplierBp(input, price, rules)
	if multiplierBp <= 0 {
		// 非法倍率按不加价处理，但必须在 Source 上留标记：静默当成 1 倍，会让一条配错
		// 的规则永远查不出来。
		return finishPricingResolution(input, price, markupBaseBp, source+":INVALID")
	}
	return finishPricingResolution(input, price, multiplierBp, source)
}

// finishPricingResolution 组装最终结果：直接定价优先，其次用上游成本乘倍率。
func finishPricingResolution(input PricingInput, price *ModelPrice, multiplierBp int, source string) PricingResolution {
	resolution := PricingResolution{MultiplierBp: multiplierBp, Source: source}
	if price != nil && price.SellUnitPrice != nil {
		// 直接定价优先：运营手工定的价不该被倍率覆盖，倍率此时只用于展示"相对成本加了
		// 多少"。0 是有效定价（免费），所以这里必须判指针而不能判零值。
		sellUnitPrice := *price.SellUnitPrice
		resolution.SellUnitPrice = &sellUnitPrice
		resolution.Priced = true
		return resolution
	}
	if input.UpstreamUnitPrice == nil {
		// 上游价未知又没有直接定价：能给倍率，给不出售价。
		return resolution
	}
	sellUnitPrice := sellPriceFromMultiplierBp(*input.UpstreamUnitPrice, multiplierBp)
	resolution.SellUnitPrice = &sellUnitPrice
	resolution.Priced = true
	return resolution
}

// resolveMultiplierBp 按"由窄到宽"的顺序挑出第一条命中的倍率。
//
// 先命中先返回：模型实体倍率 > MODEL 规则 > VENDOR 规则 > CAPABILITY 规则 > GLOBAL
// 规则 > 默认。顺序写死在这里而不是交给调用方排好 rules，因为覆盖关系是产品口径的
// 一部分：排序散到调用方，就会出现"某个入口的规则顺序不同、售价不同"。
func resolveMultiplierBp(input PricingInput, price *ModelPrice, rules []MarkupRule) (int, string) {
	if price != nil && price.MultiplierBp != nil {
		return *price.MultiplierBp, MarkupScopeModel
	}
	modelKey := strings.TrimSpace(input.ModelKey)
	vendorCode := strings.TrimSpace(input.VendorCode)
	capability := normalizeModelCapability(input.Capability)

	// 目标为空时一律不匹配：脏数据里 Target 为空的规则不该被"没填目标"的请求命中。
	if modelKey != "" {
		for _, rule := range rules {
			if rule.Scope == MarkupScopeModel && strings.TrimSpace(rule.Target) == modelKey {
				return rule.MultiplierBp, MarkupScopeModel
			}
		}
	}
	// VendorCode 为空时跳过 VENDOR：否则"没填厂商"的模型会莫名其妙按某个厂商的倍率出货。
	if vendorCode != "" {
		for _, rule := range rules {
			if rule.Scope == MarkupScopeVendor && strings.TrimSpace(rule.Target) == vendorCode {
				return rule.MultiplierBp, MarkupScopeVendor
			}
		}
	}
	if capability != "" {
		for _, rule := range rules {
			if rule.Scope == MarkupScopeCapability && normalizeModelCapability(rule.Target) == capability {
				return rule.MultiplierBp, MarkupScopeCapability
			}
		}
	}
	for _, rule := range rules {
		if rule.Scope == MarkupScopeGlobal {
			return rule.MultiplierBp, MarkupScopeGlobal
		}
	}
	return markupBaseBp, markupSourceDefault
}

// sellPriceFromMultiplierBp 按万分比折算售价，向上取整。
//
// 向上取整是刻意的：宁可多收一分，也不要因为整除丢分让平台倒贴。尤其是 0.0001 倍这类
// 极小倍率，直接截断会把售价算成 0，等于免费。全程 int64 整数运算，浮点会在
// 大额单价上丢分。
func sellPriceFromMultiplierBp(upstreamUnitPrice int64, multiplierBp int) int64 {
	if upstreamUnitPrice < 0 {
		// 负数上游价没有业务含义（服务层已挡在保存之外），这里直接按 0 处理，
		// 免得"向上取整"把负数算成正数售价。
		return 0
	}
	multiplier := int64(multiplierBp)
	if multiplier <= 0 {
		multiplier = markupBaseBp
	}
	// 溢出保护：上游价与倍率都来自后台输入，极端值相乘会溢出成负数，把"很贵"算成
	// "倒贴"。夹到 int64 上限：一个明显不合理的极值，比一个负售价更容易被看见。
	if upstreamUnitPrice > 0 && upstreamUnitPrice > math.MaxInt64/multiplier {
		return math.MaxInt64
	}
	product := upstreamUnitPrice * multiplier
	if product%markupBaseBp == 0 {
		return product / markupBaseBp
	}
	return product/markupBaseBp + 1
}

// FormatMultiplierBp 把万分比格式化成后台展示用的倍数文本。
//
// 12000 → "1.2"、12500 → "1.25"、10000 → "1"：去掉尾零，运营一眼能看出是不是原价。
// 非法值（0 或负数）返回 "0" 而不是伪装成 "1"——配置错了必须看得出来。
func FormatMultiplierBp(multiplierBp int) string {
	if multiplierBp <= 0 {
		return "0"
	}
	whole := multiplierBp / markupBaseBp
	fraction := multiplierBp % markupBaseBp
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	return strconv.Itoa(whole) + "." + strings.TrimRight(fmt.Sprintf("%04d", fraction), "0")
}

// ParseMultiplierBp 解析后台表单里的倍率，返回万分比。
//
// 表单同时接受两种写法，避免运营在"1.2"和"12000"之间反复问该填哪一个：
//
//   - 带小数点的倍数："1.2" → 12000；
//   - 纯整数的万分比："12000" → 12000。
//
// 纯整数在 10000 以下按"倍数"理解（"2" 就是 2 倍），10000 及以上按万分比理解：手写
// 12000 想表达的是 1.2 倍，而 12000 倍远超上限 1000 倍，两者不可能混淆。十进制用字符串
// 手算而不是 ParseFloat：浮点里 1.2 是 1.19999…，一旦乘出 11999 就会把 1.2 倍存成
// 1.1999 倍，而且肉眼看不出来。
func ParseMultiplierBp(raw string) (int, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0, errors.New("倍率不能为空")
	}
	if strings.Contains(text, ".") {
		multiplierBp, err := decimalMultiplierBp(text)
		if err != nil {
			return 0, err
		}
		return ensureMultiplierBpRange(multiplierBp)
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, errors.New("倍率只能是数字，示例：1.2 或 12000")
	}
	if value >= markupBaseBp {
		return ensureMultiplierBpRange(value)
	}
	if value > markupMaxBp/markupBaseBp {
		// 1001 倍这类超出上界的整数先挡在乘法之前，理由与上界本身一致：倍数越大，
		// 一次手误造成的资损越大。
		return 0, errMultiplierRange
	}
	return ensureMultiplierBpRange(value * markupBaseBp)
}

// decimalMultiplierBp 把十进制倍数文本折算成万分比。
func decimalMultiplierBp(text string) (int, error) {
	parts := strings.Split(text, ".")
	if len(parts) != 2 {
		return 0, errors.New("倍率格式不正确，示例：1.2")
	}
	wholeText := strings.TrimSpace(parts[0])
	fractionText := strings.TrimSpace(parts[1])
	if wholeText == "" {
		wholeText = "0"
	}
	if fractionText == "" {
		fractionText = "0"
	}
	whole, err := strconv.Atoi(wholeText)
	if err != nil || whole < 0 {
		return 0, errors.New("倍率格式不正确，示例：1.2")
	}
	if whole > markupMaxBp/markupBaseBp {
		return 0, errMultiplierRange
	}
	if len(fractionText) > 4 || !pricingDigitsOnly(fractionText) {
		return 0, errors.New("倍率最多保留 4 位小数")
	}
	fraction, err := strconv.Atoi(fractionText)
	if err != nil {
		return 0, errors.New("倍率格式不正确，示例：1.2")
	}
	// 小数位补齐到 4 位再参与整数运算："1.2" 是 2000 个万分之一，不是 2 个。
	scale := 1
	for index := len(fractionText); index < 4; index++ {
		scale *= 10
	}
	return whole*markupBaseBp + fraction*scale, nil
}

// pricingDigitsOnly 判断字符串是否只由 ASCII 数字组成。
//
// 不用正则：这里只需要一个逐字符判断，正则的编译与回溯对这个长度的输入都是纯开销。
func pricingDigitsOnly(text string) bool {
	if text == "" {
		return false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// ensureMultiplierBpRange 校验万分比是否落在可配置区间内。
func ensureMultiplierBpRange(multiplierBp int) (int, error) {
	if multiplierBp < markupMinBp || multiplierBp > markupMaxBp {
		return 0, errMultiplierRange
	}
	return multiplierBp, nil
}
