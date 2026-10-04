package auth

import "testing"

// TestSizeTiersAreValidForImagePricing 锁定尺寸档是图片的合法档位。
//
// 保存入口与取价入口都按 validPriceTier 判：这一档不被接受时，运营连价目行都存不进去，
// 表现为"后台点保存没反应"，而不是一条能定位到定价域的错误。
func TestSizeTiersAreValidForImagePricing(t *testing.T) {
	for _, tier := range ImageSizePriceTiers {
		if !validPriceTier("IMAGE", string(tier)) {
			t.Fatalf("图片应接受尺寸档 %q", tier)
		}
		if !IsSizePriceTier(string(tier)) {
			t.Fatalf("%q 应被认作尺寸档", tier)
		}
	}
	// 尺寸档只属于图片：别的能力分档口径不同，混用就等于给错误的维度定价。
	for _, capability := range []string{"TEXT", "VIDEO", "AUDIO"} {
		for _, tier := range ImageSizePriceTiers {
			if validPriceTier(capability, string(tier)) {
				t.Fatalf("%s 不应接受图片尺寸档 %q", capability, tier)
			}
		}
	}
	// 质量档不能被当尺寸档：这决定"取不到价时能不能回落"，认错就会把漏配的
	// 质量档静默按另一行的价成交。
	for _, tier := range ImagePriceTiers {
		if IsSizePriceTier(string(tier)) {
			t.Fatalf("质量档 %q 不应被认作尺寸档", tier)
		}
	}
	if IsSizePriceTier(string(PriceTierNone)) {
		t.Fatal("空档不应被认作尺寸档")
	}
}

// TestImageSizeTierOrderIsAscending 锁定尺寸档的顺序由小到大。
//
// 顺序不是装饰：后台与账单按同一顺序排列，写漏一分档就等于那一档永远配不上价。
func TestImageSizeTierOrderIsAscending(t *testing.T) {
	want := []PriceTier{PriceTierSize1K, PriceTierSize2K, PriceTierSize4K}
	if len(ImageSizePriceTiers) != len(want) {
		t.Fatalf("尺寸档位数应为 %d，实际 %v", len(want), ImageSizePriceTiers)
	}
	for index, tier := range want {
		if ImageSizePriceTiers[index] != tier {
			t.Fatalf("尺寸档第 %d 位应是 %q，实际 %q", index, tier, ImageSizePriceTiers[index])
		}
	}
}
