package auth

import (
	"strings"
	"time"
)

// 模型定价域（可空单价占位 + 倍率体系）。
//
// 平台走聚合网关一家上游，扣费覆盖文本 / 图片 / 视频 / 音频四类模型。这里回答的是
// "一次调用该收多少钱"里的单价部分，与"这个账号买了什么"（billing_* 表）刻意分开：
// 计费口径会随上游调价频繁变动，账号侧的订单、订阅与优惠券不该跟着改结构。
//
// 两条刻意的设计：
//
//   - 单价一律可空。上游真实价格还没填时读出 null 而不是 0——0 在前端与试算里的含义
//     是"免费"，"还没定价"必须能与它区分，否则一次试算就会把未定价的模型算成白送；
//   - 加价只用一个倍率表达（售价 = 上游成本 × 倍率）。倍率可按模型 / 厂商 / 能力 /
//     全局四级覆盖，后台不必为每个模型维护一张价格表，只在例外处写一条规则。
//
// 金额一律整数存"分"，倍率用万分比（10000 = 不加价）存储：与 billing_* 域同源，
// 避免浮点在折算时丢分。

// ModelCapability 是四类模型能力。落库用大写字符串，前端另行映射中文展示。
type ModelCapability string

const (
	CapabilityText  ModelCapability = "TEXT"
	CapabilityImage ModelCapability = "IMAGE"
	CapabilityVideo ModelCapability = "VIDEO"
	CapabilityAudio ModelCapability = "AUDIO"
)

// PriceUnit 是计费单位标签。
//
// 它只说明"这个价按什么计量"，不参与金额计算：TEXT 按千 token、IMAGE 按张、
// VIDEO/AUDIO 按秒。上游换计量口径时先改标签，价格本身仍然只是数据。
type PriceUnit string

const (
	UnitPerThousandTokens PriceUnit = "TOKEN_1K"
	UnitPerImage          PriceUnit = "IMAGE"
	UnitPerSecond         PriceUnit = "SECOND"
	UnitPerRequest        PriceUnit = "REQUEST"
)

// DefaultUnitFor 给出每类能力的默认计费单位，仅在后台没显式选择时使用。
func DefaultUnitFor(capability ModelCapability) PriceUnit {
	switch capability {
	case CapabilityText:
		return UnitPerThousandTokens
	case CapabilityImage:
		return UnitPerImage
	case CapabilityVideo, CapabilityAudio:
		return UnitPerSecond
	default:
		// 未知能力不让保存失败在单位上：单位只是标签，能力本身另有白名单校验。
		return UnitPerRequest
	}
}

// 倍率基准与可填范围。
const (
	// markupBaseBp 是不加价的万分比基准：10000 表示按上游原价卖。
	markupBaseBp = 10000
	// markupMinBp / markupMaxBp 是后台可填的倍率区间：0.0001 倍到 1000 倍。
	// 下界挡住 0 与负数（那不是折扣，是资损），上界挡住"把 1000 手误多按一位"这类事故。
	markupMinBp = 1
	markupMaxBp = markupBaseBp * 1000
)

// 倍率规则的作用域。由窄到宽依次覆盖，Target 的含义随 Scope 变化。
const (
	MarkupScopeGlobal     = "GLOBAL"
	MarkupScopeCapability = "CAPABILITY"
	MarkupScopeVendor     = "VENDOR"
	MarkupScopeModel      = "MODEL"
)

// pricingDefaultCurrency 是新建单价配置的默认币种。
const pricingDefaultCurrency = "CNY"

// ModelPrice 是某个"平台模型标识"在某类能力下的单价配置。
//
// UpstreamUnitPrice 与 SellUnitPrice 都可空：空 = 还没定价（当前的常态），读出 null
// 而不是 0。SellUnitPrice 非空时它就是最终售价，MultiplierBp 只用于展示"相对成本加了
// 多少"；两者都空时由倍率乘上游成本算出售价。
//
// (model_key, capability) 唯一：同一个模型在同一类能力下只能有一条单价，否则同一笔
// 调用会随读取顺序出现两个价格。
type ModelPrice struct {
	ID         string `gorm:"column:id;primaryKey;size:36"`
	ModelKey   string `gorm:"column:model_key;size:120;uniqueIndex:uk_billing_model_prices_model_capability,priority:1"`
	Capability string `gorm:"column:capability;size:16;uniqueIndex:uk_billing_model_prices_model_capability,priority:2"`
	Unit       string `gorm:"column:unit;size:24"`
	// VendorCode 可空，标注这条价来自哪家厂商，供 VENDOR 作用域的倍率规则匹配。
	VendorCode        string `gorm:"column:vendor_code;size:64"`
	UpstreamUnitPrice *int64 `gorm:"column:upstream_unit_price"`
	SellUnitPrice     *int64 `gorm:"column:sell_unit_price"`
	// MultiplierBp 是该模型专属倍率（万分比），可空 = 跟随倍率规则。
	MultiplierBp *int   `gorm:"column:multiplier_bp"`
	Currency     string `gorm:"column:currency;size:8"`
	Enabled      bool   `gorm:"column:enabled"`
	Note         string `gorm:"column:note;size:255"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (ModelPrice) TableName() string { return "billing_model_prices" }

// MarkupRule 是一条倍率规则。
//
// 作用域由窄到宽依次覆盖，Target 的含义随 Scope 变：GLOBAL 恒为空、CAPABILITY 是能力
// 值、VENDOR 是上游厂商 code、MODEL 是平台模型标识。(scope, target) 唯一：同一作用域
// 下同一个目标有两条规则时，"哪条生效"只能靠读取顺序决定，那是不可以解释的定价。
type MarkupRule struct {
	ID           string `gorm:"column:id;primaryKey;size:36"`
	Scope        string `gorm:"column:scope;size:16;uniqueIndex:uk_billing_markup_rules_scope_target,priority:1"`
	Target       string `gorm:"column:target;size:120;uniqueIndex:uk_billing_markup_rules_scope_target,priority:2"`
	MultiplierBp int    `gorm:"column:multiplier_bp"`
	Note         string `gorm:"column:note;size:255"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (MarkupRule) TableName() string { return "billing_markup_rules" }

// PricingModels 返回定价域的全部模型，供上层注册建表。
func PricingModels() []any {
	return []any{
		&ModelPrice{},
		&MarkupRule{},
	}
}

// normalizeModelCapability 归一能力值：首尾空格与大小写在这里抹平，白名单只认大写形态。
func normalizeModelCapability(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func validModelCapability(raw string) bool {
	switch ModelCapability(raw) {
	case CapabilityText, CapabilityImage, CapabilityVideo, CapabilityAudio:
		return true
	default:
		return false
	}
}

// normalizePriceUnit 归一计费单位，理由同 normalizeModelCapability。
func normalizePriceUnit(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func validPriceUnit(raw string) bool {
	switch PriceUnit(raw) {
	case UnitPerThousandTokens, UnitPerImage, UnitPerSecond, UnitPerRequest:
		return true
	default:
		return false
	}
}

// normalizeMarkupScope 归一作用域，理由同 normalizeModelCapability。
func normalizeMarkupScope(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func validMarkupScope(raw string) bool {
	switch raw {
	case MarkupScopeGlobal, MarkupScopeCapability, MarkupScopeVendor, MarkupScopeModel:
		return true
	default:
		return false
	}
}

// markupRuleKey 生成规则的去重键，供全量替换前的冲突校验使用。
//
// 用 NUL 拼接而不是冒号：scope 与 target 都是自由文本，冒号本身可能是合法的目标字符，
// 用冒号拼出来的字符串会把两条不同的规则误判成重复。
func markupRuleKey(scope string, target string) string {
	return scope + "\x00" + target
}

// ---------- 对外视图 ----------

// ModelPriceView 是后台的单价配置视图。
//
// 金额与倍率都是指针，且都不加 omitempty：null 表示"还没定价"，0 表示真的免费。
// 前端据此决定显示"未定价"还是"¥0.00"，缺值必须显式序列化成 null。
type ModelPriceView struct {
	ID                string `json:"id"`
	ModelKey          string `json:"modelKey"`
	Capability        string `json:"capability"`
	Unit              string `json:"unit"`
	VendorCode        string `json:"vendorCode"`
	UpstreamUnitPrice *int64 `json:"upstreamUnitPrice"`
	SellUnitPrice     *int64 `json:"sellUnitPrice"`
	MultiplierBp      *int   `json:"multiplierBp"`
	Currency          string `json:"currency"`
	Enabled           bool   `json:"enabled"`
	Note              string `json:"note"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

// MarkupRuleView 是后台的倍率规则视图。
//
// Multiplier 是 MultiplierBp 的展示形态（12000 → "1.2"）：运营填的是倍数，落库的是
// 万分比，回显时两个都给出去，前端不必自己再实现一遍折算。
type MarkupRuleView struct {
	ID           string `json:"id"`
	Scope        string `json:"scope"`
	Target       string `json:"target"`
	MultiplierBp int    `json:"multiplierBp"`
	Multiplier   string `json:"multiplier"`
	Note         string `json:"note"`
}

// MarkupView 是倍率规则集视图。
type MarkupView struct {
	Rules []MarkupRuleView `json:"rules"`
	// DefaultMultiplierBp 是没有命中任何规则时的倍率。把它一起回传，前端展示"默认
	// 不加价"时就不必自己写死 10000。
	DefaultMultiplierBp int `json:"defaultMultiplierBp"`
}

// ModelPriceInput 是后台保存单价配置的入参，ID 为空表示新建。
//
// 金额用 *int64、倍率用字符串：前者区分"未定价"与"免费"，后者让运营可以直接填 "1.2"。
// Enabled 为 nil 时新建默认启用、更新沿用原值——表单漏传该字段不该把一条配置静默停用。
type ModelPriceInput struct {
	ID                string `json:"id"`
	ModelKey          string `json:"modelKey"`
	Capability        string `json:"capability"`
	Unit              string `json:"unit"`
	VendorCode        string `json:"vendorCode"`
	UpstreamUnitPrice *int64 `json:"upstreamUnitPrice"`
	SellUnitPrice     *int64 `json:"sellUnitPrice"`
	Multiplier        string `json:"multiplier"`
	Currency          string `json:"currency"`
	Enabled           *bool  `json:"enabled"`
	Note              string `json:"note"`
}

// MarkupInput 是全量替换倍率规则的入参。
type MarkupInput struct {
	Rules []MarkupRuleInput `json:"rules"`
}

// MarkupRuleInput 是单条倍率规则入参。
type MarkupRuleInput struct {
	Scope        string `json:"scope"`
	Target       string `json:"target"`
	MultiplierBp int    `json:"multiplierBp"`
	Note         string `json:"note"`
}

// ModelPriceViewOf 把存储模型投影成视图。
func ModelPriceViewOf(price ModelPrice) ModelPriceView {
	return ModelPriceView{
		ID:                price.ID,
		ModelKey:          price.ModelKey,
		Capability:        price.Capability,
		Unit:              price.Unit,
		VendorCode:        price.VendorCode,
		UpstreamUnitPrice: price.UpstreamUnitPrice,
		SellUnitPrice:     price.SellUnitPrice,
		MultiplierBp:      price.MultiplierBp,
		Currency:          price.Currency,
		Enabled:           price.Enabled,
		Note:              price.Note,
		CreatedAt:         pricingTimeText(price.CreatedAt),
		UpdatedAt:         pricingTimeText(price.UpdatedAt),
	}
}

// MarkupViewOf 把规则集投影成视图，列表恒为非 nil 切片。
func MarkupViewOf(rules []MarkupRule) *MarkupView {
	views := make([]MarkupRuleView, 0, len(rules))
	for _, rule := range rules {
		views = append(views, MarkupRuleViewOf(rule))
	}
	return &MarkupView{Rules: views, DefaultMultiplierBp: markupBaseBp}
}

// MarkupRuleViewOf 把单条规则投影成视图。
func MarkupRuleViewOf(rule MarkupRule) MarkupRuleView {
	return MarkupRuleView{
		ID:           rule.ID,
		Scope:        rule.Scope,
		Target:       rule.Target,
		MultiplierBp: rule.MultiplierBp,
		Multiplier:   FormatMultiplierBp(rule.MultiplierBp),
		Note:         rule.Note,
	}
}

// pricingTimeText 把时间格式化成视图里的字符串。
//
// 与 billing_* 视图直接用 time.Time 不同：这两个字段只用于"最后改过的时间"展示，
// RFC3339 文本让前端少一层解析。零值回空串，免得界面出现 0001-01-01T00:00:00Z。
func pricingTimeText(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}
