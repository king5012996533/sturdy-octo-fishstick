package auth

import "testing"

// newPricingTestEnv 复用计费用例的 SQLite 装配，再补建定价表。
//
// 建表在这里显式调用 EnsurePricingSchema，而不是指望 EnsureDevSchema 已经带上定价模型：
// 注册由合并方负责，本模块的用例必须能在"还没接线"的状态下独立跑绿。
func newPricingTestEnv(t *testing.T) *billingTestEnv {
	t.Helper()
	env := newBillingTestService(t)
	if err := EnsurePricingSchema(env.store.db); err != nil {
		t.Fatalf("初始化定价表失败: %v", err)
	}
	return env
}

// TestEnsurePricingSchemaIsIdempotent 覆盖重复建表与两个唯一索引。
//
// 唯一索引缺失时保存会报 "ON CONFLICT clause does not match any PRIMARY KEY or UNIQUE
// constraint"，而重复建表报错会让每次冷启动直接失败，所以这两条都要盯住。
func TestEnsurePricingSchemaIsIdempotent(t *testing.T) {
	env := newPricingTestEnv(t)
	if err := EnsurePricingSchema(env.store.db); err != nil {
		t.Fatalf("重复建表应幂等，实际报错: %v", err)
	}

	first := &ModelPrice{ID: "price-1", ModelKey: "gpt-4o-mini", Capability: string(CapabilityText), Unit: string(UnitPerThousandTokens)}
	if err := env.store.SaveModelPrice(first); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}
	duplicate := &ModelPrice{ID: "price-2", ModelKey: "gpt-4o-mini", Capability: string(CapabilityText), Unit: string(UnitPerThousandTokens)}
	if err := env.store.SaveModelPrice(duplicate); err == nil {
		t.Fatalf("同一模型与能力组合应被唯一索引拒绝")
	}

	if err := env.store.SaveMarkupRule(&MarkupRule{ID: "rule-1", Scope: MarkupScopeGlobal, MultiplierBp: 12000}); err != nil {
		t.Fatalf("写入倍率规则失败: %v", err)
	}
	if err := env.store.SaveMarkupRule(&MarkupRule{ID: "rule-2", Scope: MarkupScopeGlobal, MultiplierBp: 13000}); err == nil {
		t.Fatalf("同一 scope 与 target 应被唯一索引拒绝")
	}
}

// TestPricingModelPriceCRUD 覆盖单价 CRUD 与"未定价 / 免费"的可区分性。
func TestPricingModelPriceCRUD(t *testing.T) {
	env := newPricingTestEnv(t)

	// 未定价：两个价格都留空，读出来必须还是 nil，而不是被零值填成 0。
	unpriced := &ModelPrice{ModelKey: "gpt-4o-mini", Capability: string(CapabilityText), Unit: string(UnitPerThousandTokens), Currency: "CNY", Enabled: true}
	if err := env.store.SaveModelPrice(unpriced); err != nil {
		t.Fatalf("保存未定价配置失败: %v", err)
	}
	// 免费：显式存 0，读出来必须是 0 而不是 nil。
	free := &ModelPrice{
		ModelKey:          "seedream",
		Capability:        string(CapabilityImage),
		Unit:              string(UnitPerImage),
		Currency:          "CNY",
		Enabled:           true,
		UpstreamUnitPrice: pricingInt64Ptr(0),
		SellUnitPrice:     pricingInt64Ptr(0),
	}
	if err := env.store.SaveModelPrice(free); err != nil {
		t.Fatalf("保存免费配置失败: %v", err)
	}

	prices, err := env.store.ModelPrices()
	if err != nil {
		t.Fatalf("读取单价列表失败: %v", err)
	}
	if len(prices) != 2 {
		t.Fatalf("单价列表应为 2 条，实际 %d 条", len(prices))
	}
	loadedUnpriced, err := env.store.ModelPriceByKey("gpt-4o-mini", "text")
	if err != nil {
		t.Fatalf("按标识读取单价失败: %v", err)
	}
	if loadedUnpriced.UpstreamUnitPrice != nil || loadedUnpriced.SellUnitPrice != nil {
		t.Fatalf("未定价配置读出来不能是 0：%+v", loadedUnpriced)
	}
	loadedFree, err := env.store.ModelPriceByKey("seedream", string(CapabilityImage))
	if err != nil {
		t.Fatalf("按标识读取单价失败: %v", err)
	}
	if loadedFree.SellUnitPrice == nil || *loadedFree.SellUnitPrice != 0 {
		t.Fatalf("免费配置读出来必须是 0：%+v", loadedFree.SellUnitPrice)
	}
	if loadedFree.ID != free.ID {
		t.Fatalf("按标识读到的记录不一致：%s / %s", loadedFree.ID, free.ID)
	}

	if err := env.store.DeleteModelPrice(unpriced.ID); err != nil {
		t.Fatalf("删除单价失败: %v", err)
	}
	if _, err := env.store.ModelPriceByID(unpriced.ID); err == nil {
		t.Fatalf("删除后按主键应读不到")
	}
	if prices, err = env.store.ModelPrices(); err != nil || len(prices) != 1 {
		t.Fatalf("删除后应剩 1 条，实际 %d 条（err=%v）", len(prices), err)
	}
}

// TestReplaceMarkupRulesReplacesAll 覆盖全量替换与冲突拒绝。
func TestReplaceMarkupRulesReplacesAll(t *testing.T) {
	env := newPricingTestEnv(t)

	first := []MarkupRule{
		{Scope: MarkupScopeGlobal, MultiplierBp: 12000, Note: "全局加价"},
		{Scope: MarkupScopeCapability, Target: string(CapabilityText), MultiplierBp: 15000},
	}
	if err := env.store.ReplaceMarkupRules(first); err != nil {
		t.Fatalf("首次替换失败: %v", err)
	}
	rules, err := env.store.MarkupRules()
	if err != nil {
		t.Fatalf("读取规则失败: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("替换后应有 2 条规则，实际 %d 条", len(rules))
	}
	for _, rule := range rules {
		if rule.ID == "" || rule.CreatedAt.IsZero() {
			t.Fatalf("替换时没有补齐主键或创建时间：%+v", rule)
		}
	}

	// 第二次替换必须把旧的整批换掉，而不是叠加。
	second := []MarkupRule{{Scope: MarkupScopeVendor, Target: "openai", MultiplierBp: 11000}}
	if err := env.store.ReplaceMarkupRules(second); err != nil {
		t.Fatalf("二次替换失败: %v", err)
	}
	if rules, err = env.store.MarkupRules(); err != nil || len(rules) != 1 || rules[0].Scope != MarkupScopeVendor {
		t.Fatalf("二次替换应只剩 1 条 VENDOR 规则，实际 %+v（err=%v）", rules, err)
	}

	// 同一批里 (scope, target) 重复必须被拒，且拒绝后旧规则原样保留。
	duplicated := []MarkupRule{
		{Scope: MarkupScopeGlobal, MultiplierBp: 12000},
		{Scope: MarkupScopeGlobal, MultiplierBp: 13000},
	}
	assertBillingError(t, env.store.ReplaceMarkupRules(duplicated), 400, "")
	if rules, err = env.store.MarkupRules(); err != nil || len(rules) != 1 || rules[0].MultiplierBp != 11000 {
		t.Fatalf("冲突拒绝后旧规则必须原样保留，实际 %+v（err=%v）", rules, err)
	}
}

// TestSavePricingModelPriceKeepsUnpricedAndFreeDistinct 覆盖服务层的金额语义与默认值。
func TestSavePricingModelPriceKeepsUnpricedAndFreeDistinct(t *testing.T) {
	env := newPricingTestEnv(t)

	// 只填模型、能力与档位：单位、币种、启用状态都走默认，价格保持"未定价"。
	unpriced, err := env.service.SaveModelPrice(ModelPriceInput{ModelKey: "gpt-4o-mini", Capability: "text", TokenTier: "input"})
	if err != nil {
		t.Fatalf("保存未定价配置失败: %v", err)
	}
	if unpriced.UpstreamUnitPrice != nil || unpriced.SellUnitPrice != nil || unpriced.MultiplierBp != nil {
		t.Fatalf("未定价配置不应被填成 0：%+v", unpriced)
	}
	// 文本默认按「分/百万 token」：官方最便宜的一档是 0.02 元/百万 token，换成
	// 分/千 token 是 0.02 分，整数存不下，只能被迫向上取整成 1 分（等于按 ¥10 卖）。
	if unpriced.Unit != string(UnitPerMillionTokens) || unpriced.Currency != "CNY" || !unpriced.Enabled {
		t.Fatalf("默认单位、币种与启用状态不正确：%+v", unpriced)
	}
	if unpriced.Capability != string(CapabilityText) {
		t.Fatalf("能力值应归一大写：%q", unpriced.Capability)
	}
	if unpriced.TokenTier != string(TokenTierInput) {
		t.Fatalf("档位应归一大写：%q", unpriced.TokenTier)
	}
	if unpriced.CreatedAt == "" || unpriced.UpdatedAt == "" {
		t.Fatalf("视图缺少时间字段：%+v", unpriced)
	}

	// 免费：显式传 0，读回来必须还是 0。
	free, err := env.service.SaveModelPrice(ModelPriceInput{ModelKey: "seedream", Capability: "IMAGE", SellUnitPrice: pricingInt64Ptr(0)})
	if err != nil {
		t.Fatalf("保存免费配置失败: %v", err)
	}
	if free.SellUnitPrice == nil || *free.SellUnitPrice != 0 {
		t.Fatalf("0 与 null 必须可区分：%+v", free.SellUnitPrice)
	}
	if free.Unit != string(UnitPerImage) {
		t.Fatalf("IMAGE 的默认单位应为按张：%q", free.Unit)
	}

	// 倍率字符串按 ParseMultiplierBp 解析。
	multiplied, err := env.service.SaveModelPrice(ModelPriceInput{ModelKey: "doubao-audio", Capability: "AUDIO", Multiplier: "1.2"})
	if err != nil {
		t.Fatalf("保存带倍率的配置失败: %v", err)
	}
	if multiplied.MultiplierBp == nil || *multiplied.MultiplierBp != 12000 {
		t.Fatalf("倍率应为 12000，实际 %v", multiplied.MultiplierBp)
	}
	if multiplied.Unit != string(UnitPerSecond) {
		t.Fatalf("AUDIO 的默认单位应为按秒：%q", multiplied.Unit)
	}

	// 同一个模型的另外两档可以并存：文本的三档价本来就是三行，唯一键必须带上档位，
	// 否则"缓存命中 8 分 / 输出 1600 分"这种真实价目根本存不下来。
	for _, tier := range []string{"CACHE", "OUTPUT"} {
		if _, err := env.service.SaveModelPrice(ModelPriceInput{ModelKey: "gpt-4o-mini", Capability: "TEXT", TokenTier: tier}); err != nil {
			t.Fatalf("同一模型的 %s 档位应可单独成行: %v", tier, err)
		}
	}

	// 冲突：同一个 (model_key, capability, token_tier) 不能有第二条。
	_, err = env.service.SaveModelPrice(ModelPriceInput{ModelKey: "gpt-4o-mini", Capability: "TEXT", TokenTier: "INPUT"})
	assertBillingError(t, err, 409, "")

	// 非法输入一律 400。
	_, err = env.service.SaveModelPrice(ModelPriceInput{ModelKey: "", Capability: "TEXT", TokenTier: "INPUT"})
	assertBillingError(t, err, 400, "")
	_, err = env.service.SaveModelPrice(ModelPriceInput{ModelKey: "m1", Capability: "MUSIC"})
	assertBillingError(t, err, 400, "")
	_, err = env.service.SaveModelPrice(ModelPriceInput{ModelKey: "m2", Capability: "TEXT", TokenTier: "INPUT", Multiplier: "abc"})
	assertBillingError(t, err, 400, "")
	_, err = env.service.SaveModelPrice(ModelPriceInput{ModelKey: "m3", Capability: "TEXT", TokenTier: "INPUT", UpstreamUnitPrice: pricingInt64Ptr(-1)})
	assertBillingError(t, err, 400, "")
	_, err = env.service.SaveModelPrice(ModelPriceInput{ModelKey: "m4", Capability: "TEXT", TokenTier: "INPUT", SellUnitPrice: pricingInt64Ptr(-1)})
	assertBillingError(t, err, 400, "")
	_, err = env.service.SaveModelPrice(ModelPriceInput{ID: "not-exist", ModelKey: "m5", Capability: "TEXT", TokenTier: "INPUT"})
	assertBillingError(t, err, 404, "")

	// 文本缺档位、非文本带档位都必须拒绝：静默丢弃会让运营以为自己配的那条生效了。
	_, err = env.service.SaveModelPrice(ModelPriceInput{ModelKey: "m6", Capability: "TEXT"})
	assertBillingError(t, err, 400, "")
	_, err = env.service.SaveModelPrice(ModelPriceInput{ModelKey: "m7", Capability: "IMAGE", TokenTier: "INPUT"})
	assertBillingError(t, err, 400, "")
}

// TestReplaceMarkupRulesValidation 覆盖规则集的服务层校验与归一化。
func TestReplaceMarkupRulesValidation(t *testing.T) {
	env := newPricingTestEnv(t)

	view, err := env.service.ReplaceMarkupRules(MarkupInput{Rules: []MarkupRuleInput{
		{Scope: "global", MultiplierBp: 12000, Note: "全局"},
		{Scope: "capability", Target: "text", MultiplierBp: 15000},
	}})
	if err != nil {
		t.Fatalf("替换规则失败: %v", err)
	}
	if view.DefaultMultiplierBp != 10000 || len(view.Rules) != 2 {
		t.Fatalf("规则集视图不正确：%+v", view)
	}
	for _, rule := range view.Rules {
		if rule.Scope == MarkupScopeGlobal && rule.Target != "" {
			t.Fatalf("GLOBAL 规则的目标必须清空：%+v", rule)
		}
		if rule.Scope == MarkupScopeCapability {
			if rule.Target != string(CapabilityText) {
				t.Fatalf("能力目标应归一大写：%q", rule.Target)
			}
			if rule.Multiplier != "1.5" {
				t.Fatalf("倍率展示文本应为 1.5，实际 %q", rule.Multiplier)
			}
		}
	}

	// 非法输入一律 400：作用域、能力目标、厂商目标、倍率区间与重复。
	invalidInputs := [][]MarkupRuleInput{
		{{Scope: "TENANT", MultiplierBp: 12000}},
		{{Scope: MarkupScopeCapability, Target: "MUSIC", MultiplierBp: 12000}},
		{{Scope: MarkupScopeVendor, Target: "", MultiplierBp: 12000}},
		{{Scope: MarkupScopeModel, Target: "", MultiplierBp: 12000}},
		{{Scope: MarkupScopeGlobal, MultiplierBp: 0}},
		{{Scope: MarkupScopeGlobal, MultiplierBp: -100}},
		{{Scope: MarkupScopeGlobal, MultiplierBp: markupMaxBp + 1}},
		{{Scope: MarkupScopeGlobal, MultiplierBp: 12000}, {Scope: MarkupScopeGlobal, MultiplierBp: 13000}},
		{{Scope: MarkupScopeCapability, Target: "TEXT", MultiplierBp: 12000}, {Scope: "capability", Target: "text", MultiplierBp: 13000}},
	}
	for _, rules := range invalidInputs {
		_, err := env.service.ReplaceMarkupRules(MarkupInput{Rules: rules})
		assertBillingError(t, err, 400, "")
	}

	// 拒绝之后上一批合法规则必须原样保留。
	after, err := env.service.AdminMarkupRules()
	if err != nil || len(after.Rules) != 2 {
		t.Fatalf("校验失败不应动到已保存的规则：%+v（err=%v）", after, err)
	}

	// 清空规则集是合法的：等于"全部按上游原价"，不需要留一条 10000 的全局规则兜底。
	empty, err := env.service.ReplaceMarkupRules(MarkupInput{})
	if err != nil {
		t.Fatalf("清空规则集失败: %v", err)
	}
	if empty.Rules == nil || len(empty.Rules) != 0 {
		t.Fatalf("清空后规则列表应为非 nil 空切片：%+v", empty.Rules)
	}
}

// TestPreviewPricingModelPriceResolves 覆盖服务层试算：命中配置、回落到规则、未定价。
func TestPreviewPricingModelPriceResolves(t *testing.T) {
	env := newPricingTestEnv(t)

	if _, err := env.service.SaveModelPrice(ModelPriceInput{ModelKey: "gpt-4o-mini", Capability: "TEXT", TokenTier: "INPUT", Multiplier: "1.2"}); err != nil {
		t.Fatalf("准备单价配置失败: %v", err)
	}
	// 输出档位单独配一条更贵的倍率：文本三档各自走自己的价与自己的倍率。
	if _, err := env.service.SaveModelPrice(ModelPriceInput{ModelKey: "gpt-4o-mini", Capability: "TEXT", TokenTier: "OUTPUT", Multiplier: "3"}); err != nil {
		t.Fatalf("准备输出档位配置失败: %v", err)
	}
	if _, err := env.service.ReplaceMarkupRules(MarkupInput{Rules: []MarkupRuleInput{
		{Scope: MarkupScopeGlobal, MultiplierBp: 15000},
		{Scope: MarkupScopeCapability, Target: "image", MultiplierBp: 12500},
	}}); err != nil {
		t.Fatalf("准备倍率规则失败: %v", err)
	}

	// 命中模型专属倍率：1000 × 1.2 = 1200。
	resolution, err := env.service.PreviewModelPrice(PricingInput{ModelKey: "gpt-4o-mini", Capability: "TEXT", TokenTier: "INPUT", UpstreamUnitPrice: pricingInt64Ptr(1000)})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if resolution.Source != MarkupScopeModel || resolution.MultiplierBp != 12000 || !resolution.Priced {
		t.Fatalf("模型专属倍率未生效：%+v", resolution)
	}
	if resolution.SellUnitPrice == nil || *resolution.SellUnitPrice != 1200 {
		t.Fatalf("售价应为 1200，实际 %v", resolution.SellUnitPrice)
	}

	// 同一模型换到输出档位：走的是输出那条配置的倍率，不会串到输入档位上。
	resolution, err = env.service.PreviewModelPrice(PricingInput{ModelKey: "gpt-4o-mini", Capability: "TEXT", TokenTier: "OUTPUT", UpstreamUnitPrice: pricingInt64Ptr(1000)})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if resolution.MultiplierBp != 30000 || resolution.SellUnitPrice == nil || *resolution.SellUnitPrice != 3000 {
		t.Fatalf("输出档位应走自己的倍率：%+v", resolution)
	}

	// 文本不给档位就等于没定价：三档价在库里是三行，随便取一行都会算出一个错的价格。
	resolution, err = env.service.PreviewModelPrice(PricingInput{ModelKey: "gpt-4o-mini", Capability: "TEXT", UpstreamUnitPrice: pricingInt64Ptr(1000)})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if resolution.Source != MarkupScopeGlobal {
		t.Fatalf("不带档位的文本试算不应命中任何单价配置：%+v", resolution)
	}

	// 没有单价配置的模型回落到能力规则：800 × 1.25 = 1000。
	resolution, err = env.service.PreviewModelPrice(PricingInput{ModelKey: "unknown-image", Capability: "image", UpstreamUnitPrice: pricingInt64Ptr(800)})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if resolution.Source != MarkupScopeCapability || resolution.SellUnitPrice == nil || *resolution.SellUnitPrice != 1000 {
		t.Fatalf("能力规则未生效：%+v", resolution)
	}

	// 上游价为空：能算倍率，算不出售价。
	resolution, err = env.service.PreviewModelPrice(PricingInput{ModelKey: "unknown-video", Capability: "VIDEO"})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if resolution.Priced || resolution.SellUnitPrice != nil || resolution.Source != MarkupScopeGlobal {
		t.Fatalf("未定价模型不应算出售价：%+v", resolution)
	}

	// 非法入参同样 400，避免表单里一个手误的负数被算成负售价。
	_, err = env.service.PreviewModelPrice(PricingInput{Capability: "MUSIC"})
	assertBillingError(t, err, 400, "")
	_, err = env.service.PreviewModelPrice(PricingInput{Capability: "TEXT", UpstreamUnitPrice: pricingInt64Ptr(-1)})
	assertBillingError(t, err, 400, "")
}

// TestDeletePricingModelPriceMissing 确认删除不存在的配置给出 404 而非 500。
func TestDeletePricingModelPriceMissing(t *testing.T) {
	env := newPricingTestEnv(t)
	assertBillingError(t, env.service.DeleteModelPrice("not-exist"), 404, "")
	assertBillingError(t, env.service.DeleteModelPrice(""), 400, "")
}
