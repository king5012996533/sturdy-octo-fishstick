package auth

import "testing"

// TestQuoteTaskChargePicksVideoResolutionTier 覆盖"同一模型两个分辨率档两行价"。
//
// 上游按分辨率分别定价时，两档差价真实存在；取错一档就是一条长期按错价成交的通道。
func TestQuoteTaskChargePicksVideoResolutionTier(t *testing.T) {
	env := newCreditTaskEnv(t)
	for tier, price := range map[string]int64{"480P": 150, "720P": 180} {
		value := price
		if err := env.store.SaveModelPrice(&ModelPrice{
			ModelKey:      "grok-imagine-video/v1.5",
			Capability:    string(CapabilityVideo),
			PriceTier:     tier,
			Unit:          string(UnitPerRequest),
			SellUnitPrice: &value,
			Enabled:       true,
		}); err != nil {
			t.Fatalf("写入 %s 档单价失败: %v", tier, err)
		}
	}

	for tier, want := range map[string]int64{"480P": 150, "720P": 180} {
		quote, err := env.service.QuoteTaskCharge(TaskChargeInput{ModelKey: "grok-imagine-video/v1.5", Capability: "VIDEO", Tier: tier, Quantity: 15})
		if err != nil {
			t.Fatalf("%s 档试算失败: %v", tier, err)
		}
		if !quote.Priced || quote.Credits != want {
			t.Fatalf("%s 档应扣 %d 分，实际 %#v", tier, want, quote)
		}
	}
}

// TestQuoteTaskChargeVideoResolutionFallsBackToUntiered 覆盖"模型不按分辨率分价"。
//
// 只配了「不区分」那一行时，用户选任何分辨率都该用这一行；否则给某个模型加一档分辨率
// 就会把其余档位打成未定价——那是一次全量报价失败，而不是一个可以慢慢补的配置缺口。
func TestQuoteTaskChargeVideoResolutionFallsBackToUntiered(t *testing.T) {
	env := newCreditTaskEnv(t)
	value := int64(200)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:      "seedance-2.5",
		Capability:    string(CapabilityVideo),
		Unit:          string(UnitPerSecond),
		SellUnitPrice: &value,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}

	quote, err := env.service.QuoteTaskCharge(TaskChargeInput{ModelKey: "seedance-2.5", Capability: "VIDEO", Tier: "720P", Quantity: 10})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if quote.Credits != 2000 {
		t.Fatalf("应回落到不区分档并按秒计价（200 × 10），实际 %#v", quote)
	}
}

// TestQuoteTaskChargeVideoResolutionWithoutAnyPriceIsNotFree 覆盖"漏配的档位不放行"。
//
// 只配了 480P 一行，用户选 720P 时既取不到 720P 也取不到不区分档：这里必须明确回一个
// 未定价，而不是拿 480P 的价顶上——那等于用户点 720P、按 480P 出货。
func TestQuoteTaskChargeVideoResolutionWithoutAnyPriceIsNotFree(t *testing.T) {
	env := newCreditTaskEnv(t)
	value := int64(150)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:      "grok-imagine-video/v1.5",
		Capability:    string(CapabilityVideo),
		PriceTier:     "480P",
		Unit:          string(UnitPerRequest),
		SellUnitPrice: &value,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}

	quote, err := env.service.QuoteTaskCharge(TaskChargeInput{ModelKey: "grok-imagine-video/v1.5", Capability: "VIDEO", Tier: "720P", Quantity: 15})
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if quote.Priced {
		t.Fatalf("漏配的档位不该按别的档成交，实际 %#v", quote)
	}

	// 配过的那一档仍然正常。
	priced, err := env.service.QuoteTaskCharge(TaskChargeInput{ModelKey: "grok-imagine-video/v1.5", Capability: "VIDEO", Tier: "480P", Quantity: 15})
	if err != nil {
		t.Fatalf("480P 试算失败: %v", err)
	}
	if !priced.Priced || priced.Credits != 150 {
		t.Fatalf("480P 应扣 150 分，实际 %#v", priced)
	}
}

// TestValidPriceTierForVideoResolution 锁定视频档位的合法取值形状。
func TestValidPriceTierForVideoResolution(t *testing.T) {
	valid := []string{"", "480P", "720P", "768P", "1080P", "1440P", "2160P", "2K", "480p"}
	for _, tier := range valid {
		if !validPriceTier(string(CapabilityVideo), tier) {
			t.Fatalf("视频档位 %q 应合法", tier)
		}
	}
	// 图片质量档、音频时长档、拼错的分辨率都不能混进视频价目：它们既取不到价，
	// 又会让运营以为自己配生效了。
	invalid := []string{"LOW", "HIGH", "SHORT", "720PP", "P720", "超清", "72 0P", "4K8"}
	for _, tier := range invalid {
		if validPriceTier(string(CapabilityVideo), tier) {
			t.Fatalf("视频档位 %q 应被拒绝", tier)
		}
	}
}
