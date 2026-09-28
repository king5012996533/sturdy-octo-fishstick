package auth

import "testing"

// pricingInt64Ptr / pricingIntPtr 是两个小工具：定价用例里到处是"可空金额与倍率"，
// 直接写局部变量再取地址会让每个用例多两行，且容易取到循环变量。
func pricingInt64Ptr(value int64) *int64 { return &value }

func pricingIntPtr(value int) *int { return &value }

// TestResolvePricingPriority 逐级覆盖倍率的优先级。
func TestResolvePricingPriority(t *testing.T) {
	globalRule := MarkupRule{Scope: MarkupScopeGlobal, Target: "", MultiplierBp: 20000}
	capabilityRule := MarkupRule{Scope: MarkupScopeCapability, Target: string(CapabilityText), MultiplierBp: 15000}
	vendorRule := MarkupRule{Scope: MarkupScopeVendor, Target: "openai", MultiplierBp: 12000}
	modelRule := MarkupRule{Scope: MarkupScopeModel, Target: "gpt-4o-mini", MultiplierBp: 11000}
	input := PricingInput{
		ModelKey:          "gpt-4o-mini",
		VendorCode:        "openai",
		Capability:        string(CapabilityText),
		UpstreamUnitPrice: pricingInt64Ptr(100),
	}

	cases := []struct {
		name       string
		price      *ModelPrice
		rules      []MarkupRule
		wantBp     int
		wantSource string
	}{
		{"模型专属倍率优先于一切规则", &ModelPrice{MultiplierBp: pricingIntPtr(10500)}, []MarkupRule{globalRule, capabilityRule, vendorRule, modelRule}, 10500, "MODEL"},
		{"MODEL 规则覆盖 VENDOR", nil, []MarkupRule{globalRule, capabilityRule, vendorRule, modelRule}, 11000, "MODEL"},
		{"VENDOR 规则覆盖 CAPABILITY 与 GLOBAL", nil, []MarkupRule{globalRule, capabilityRule, vendorRule}, 12000, "VENDOR"},
		{"CAPABILITY 规则覆盖 GLOBAL", nil, []MarkupRule{globalRule, capabilityRule}, 15000, "CAPABILITY"},
		{"只剩 GLOBAL 时用 GLOBAL", nil, []MarkupRule{globalRule}, 20000, "GLOBAL"},
		{"没有任何规则回落默认", nil, nil, 10000, "DEFAULT"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resolution := ResolvePricing(input, testCase.price, testCase.rules)
			if resolution.MultiplierBp != testCase.wantBp || resolution.Source != testCase.wantSource {
				t.Fatalf("期望 %d/%s，实际 %d/%s", testCase.wantBp, testCase.wantSource, resolution.MultiplierBp, resolution.Source)
			}
			// 上游 100 分：100 × 1.05 / 1.1 / 1.2 / 1.5 / 2 都是整除，售价必须与倍率同步。
			wantSell := int64(100) * int64(testCase.wantBp) / 10000
			if resolution.SellUnitPrice == nil || *resolution.SellUnitPrice != wantSell {
				t.Fatalf("%s：售价期望 %d，实际 %v", testCase.name, wantSell, resolution.SellUnitPrice)
			}
		})
	}
}

// TestResolvePricingSkipsVendorRuleWhenVendorCodeEmpty 确认空厂商不会命中厂商规则。
func TestResolvePricingSkipsVendorRuleWhenVendorCodeEmpty(t *testing.T) {
	rules := []MarkupRule{
		// 脏数据：Target 为空的 VENDOR 规则。它不该被"没填厂商"的请求命中，
		// 否则任何模型都会莫名其妙按这条规则的倍率出货。
		{Scope: MarkupScopeVendor, Target: "", MultiplierBp: 30000},
		{Scope: MarkupScopeGlobal, Target: "", MultiplierBp: 13000},
	}
	input := PricingInput{ModelKey: "gpt-4o-mini", VendorCode: "", Capability: string(CapabilityText), UpstreamUnitPrice: pricingInt64Ptr(100)}
	resolution := ResolvePricing(input, nil, rules)
	if resolution.MultiplierBp != 13000 || resolution.Source != MarkupScopeGlobal {
		t.Fatalf("空厂商应跳过 VENDOR 规则回落到 GLOBAL，实际 %d/%s", resolution.MultiplierBp, resolution.Source)
	}

	// 填了厂商才轮到 VENDOR 规则生效。
	input.VendorCode = "openai"
	if resolution = ResolvePricing(input, nil, append([]MarkupRule{{Scope: MarkupScopeVendor, Target: "openai", MultiplierBp: 12000}}, rules...)); resolution.MultiplierBp != 12000 {
		t.Fatalf("填了厂商应命中 VENDOR 规则，实际 %d", resolution.MultiplierBp)
	}
}

// TestResolvePricingWithoutUpstreamPriceIsUnpriced 确认"没上游价"= 未定价而不是免费。
func TestResolvePricingWithoutUpstreamPriceIsUnpriced(t *testing.T) {
	resolution := ResolvePricing(
		PricingInput{ModelKey: "gpt-4o-mini", Capability: string(CapabilityText)},
		nil,
		[]MarkupRule{{Scope: MarkupScopeGlobal, Target: "", MultiplierBp: 12500}},
	)
	if resolution.Priced {
		t.Fatalf("上游价为空时不应标记为已定价：%+v", resolution)
	}
	if resolution.SellUnitPrice != nil {
		t.Fatalf("上游价为空时售价必须是 null，实际 %d", *resolution.SellUnitPrice)
	}
	// 倍率仍然要给出：运营据此知道该按几倍去填上游价。
	if resolution.MultiplierBp != 12500 || resolution.Source != MarkupScopeGlobal {
		t.Fatalf("倍率仍应解析出来，实际 %d/%s", resolution.MultiplierBp, resolution.Source)
	}

	// 直接定价为 0（免费）是有效定价，不能被当成"未定价"。
	free := ResolvePricing(
		PricingInput{ModelKey: "gpt-4o-mini", Capability: string(CapabilityText)},
		&ModelPrice{SellUnitPrice: pricingInt64Ptr(0)},
		nil,
	)
	if !free.Priced || free.SellUnitPrice == nil || *free.SellUnitPrice != 0 {
		t.Fatalf("直接定价 0 应算出免费（已定价），实际 %+v", free)
	}
}

// TestResolvePricingCeilRounding 确认售价向上取整。
func TestResolvePricingCeilRounding(t *testing.T) {
	cases := []struct {
		upstreamUnitPrice int64
		multiplierBp      int
		wantSell          int64
	}{
		// 原价不因为取整多收：10001 ÷ 10000 本来就是整数。
		{10001, 10000, 10001},
		// 0.9999 分直接截断会算成 0，等于免费送；必须进位到 1 分。
		{3, 3333, 1},
		{1, 12000, 2},
		{250, 12500, 313},
		{0, 12000, 0},
	}
	for _, testCase := range cases {
		resolution := ResolvePricing(
			PricingInput{ModelKey: "m", Capability: string(CapabilityText), UpstreamUnitPrice: pricingInt64Ptr(testCase.upstreamUnitPrice)},
			&ModelPrice{MultiplierBp: pricingIntPtr(testCase.multiplierBp)},
			nil,
		)
		if !resolution.Priced || resolution.SellUnitPrice == nil {
			t.Fatalf("upstream=%d bp=%d 应能算出售价：%+v", testCase.upstreamUnitPrice, testCase.multiplierBp, resolution)
		}
		if *resolution.SellUnitPrice != testCase.wantSell {
			t.Fatalf("upstream=%d bp=%d：期望售价 %d，实际 %d", testCase.upstreamUnitPrice, testCase.multiplierBp, testCase.wantSell, *resolution.SellUnitPrice)
		}
	}
}

// TestResolvePricingInvalidMultiplierFallsBack 确认非法倍率回落到原价并在 Source 上留标记。
func TestResolvePricingInvalidMultiplierFallsBack(t *testing.T) {
	resolution := ResolvePricing(
		PricingInput{ModelKey: "m", Capability: string(CapabilityText), UpstreamUnitPrice: pricingInt64Ptr(100)},
		&ModelPrice{MultiplierBp: pricingIntPtr(0)},
		nil,
	)
	if resolution.MultiplierBp != 10000 || resolution.Source != "MODEL:INVALID" {
		t.Fatalf("模型专属的 0 倍率应按原价处理并带 INVALID 标记，实际 %d/%s", resolution.MultiplierBp, resolution.Source)
	}
	if resolution.SellUnitPrice == nil || *resolution.SellUnitPrice != 100 {
		t.Fatalf("非法倍率应回落到原价 100，实际 %v", resolution.SellUnitPrice)
	}

	resolution = ResolvePricing(
		PricingInput{ModelKey: "m", Capability: string(CapabilityText), UpstreamUnitPrice: pricingInt64Ptr(100)},
		nil,
		[]MarkupRule{{Scope: MarkupScopeGlobal, Target: "", MultiplierBp: -100}},
	)
	if resolution.MultiplierBp != 10000 || resolution.Source != "GLOBAL:INVALID" {
		t.Fatalf("负倍率规则应按原价处理并带 INVALID 标记，实际 %d/%s", resolution.MultiplierBp, resolution.Source)
	}
}

// TestResolvePricingPrefersDirectSellPrice 确认直接定价优先于倍率折算。
func TestResolvePricingPrefersDirectSellPrice(t *testing.T) {
	resolution := ResolvePricing(
		PricingInput{ModelKey: "m", Capability: string(CapabilityText), UpstreamUnitPrice: pricingInt64Ptr(100)},
		&ModelPrice{SellUnitPrice: pricingInt64Ptr(888), MultiplierBp: pricingIntPtr(12000)},
		nil,
	)
	if resolution.SellUnitPrice == nil || *resolution.SellUnitPrice != 888 {
		t.Fatalf("直接定价应优先，实际 %v", resolution.SellUnitPrice)
	}
	// 倍率仍要展示：它回答"这个价相对上游成本加了多少"。
	if resolution.MultiplierBp != 12000 || resolution.Source != MarkupScopeModel {
		t.Fatalf("倍率仍应解析出来，实际 %d/%s", resolution.MultiplierBp, resolution.Source)
	}
}

// TestParseMultiplierBp 覆盖后台表单的倍率解析边界。
func TestParseMultiplierBp(t *testing.T) {
	accepted := map[string]int{
		"1.2":     12000,
		"12000":   12000,
		"2":       20000,
		"1":       10000,
		"0.0001":  1,
		"1.25":    12500,
		"1.0001":  10001,
		"1000":    10000000,
		" 1.2 ":   12000,
		"1000.00": 10000000,
	}
	for raw, want := range accepted {
		parsed, err := ParseMultiplierBp(raw)
		if err != nil {
			t.Fatalf("%q 应能解析，实际报错: %v", raw, err)
		}
		if parsed != want {
			t.Fatalf("%q 期望 %d，实际 %d", raw, want, parsed)
		}
	}

	rejected := []string{"", "0", "abc", "1001", "1001.5", "0.00005", "1.23456", "-1", "1.2.3", "1,2"}
	for _, raw := range rejected {
		if parsed, err := ParseMultiplierBp(raw); err == nil {
			t.Fatalf("%q 应被拒绝，实际解析出 %d", raw, parsed)
		}
	}
}

// TestFormatMultiplierBp 覆盖倍数文本的展示形态。
func TestFormatMultiplierBp(t *testing.T) {
	cases := map[int]string{
		10000: "1",
		12000: "1.2",
		12500: "1.25",
		10001: "1.0001",
		11000: "1.1",
		20000: "2",
		0:     "0",
		-100:  "0",
	}
	for multiplierBp, want := range cases {
		if formatted := FormatMultiplierBp(multiplierBp); formatted != want {
			t.Fatalf("%d 期望 %q，实际 %q", multiplierBp, want, formatted)
		}
	}
	// 与解析互为逆运算：后台填进去什么，回显就是什么。
	for _, raw := range []string{"1.2", "0.0001", "1.0001", "2"} {
		parsed, err := ParseMultiplierBp(raw)
		if err != nil {
			t.Fatalf("%q 应能解析: %v", raw, err)
		}
		if formatted := FormatMultiplierBp(parsed); formatted != raw {
			t.Fatalf("%q 往返后期望 %q，实际 %q", raw, raw, formatted)
		}
	}
}
