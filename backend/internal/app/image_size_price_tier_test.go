package app

import (
	"testing"

	"infinite-canvas/backend/internal/auth"
)

// TestImageSizePriceTierBucketsByPixels 锁定尺寸到档位的映射。
//
// 边界两侧各取一个前台真实可选的尺寸：1K 组最大 1824×1024、2K 组最小 2048×2048、
// 4K 组最小 2336×3504。边界挪进去就会让某个常用尺寸掉到隔壁档，而账单上看不出异常。
func TestImageSizePriceTierBucketsByPixels(t *testing.T) {
	cases := map[any]string{
		"1024x1024": tierSize1K,
		"1824x1024": tierSize1K,
		"2048x878":  tierSize1K,
		"2048x2048": tierSize2K,
		"3136x1344": tierSize2K,
		"2336x3504": tierSize4K,
		"2880x2880": tierSize4K,
		"3840x2160": tierSize4K,
		// 小尺寸归 1K：按面积分档时它本来就在最便宜的那一档，另开一档只会多一行用不上的价。
		"512x512": tierSize1K,
		// 大写与空格容错，面板与接口一路传下来的写法并不统一。
		" 1024X1024 ": tierSize1K,
	}
	for size, want := range cases {
		if got := imageSizePriceTier(size); got != want {
			t.Fatalf("尺寸 %v 应落在 %q，实际 %q", size, want, got)
		}
	}
}

// TestImageSizePriceTierIgnoresUnclassifiableSizes 覆盖"算不出面积就不猜档位"。
//
// auto 与比例串的最终面积由模型自己决定，我们按面积分档的前提不成立；返回空档让这一批请求
// 落到"不区分档位"那一行，由运营按兜底价配。猜一个档位等于把成本压在猜错的那一侧。
func TestImageSizePriceTierIgnoresUnclassifiableSizes(t *testing.T) {
	for _, size := range []any{nil, "", "auto", "AUTO", "1:1", "16:9", "1024", "1024x", "x1024", "0x0", "1024xabc", 1024} {
		if got := imageSizePriceTier(size); got != "" {
			t.Fatalf("尺寸 %v 不应给出档位，实际 %q", size, got)
		}
	}
}

// TestImageSizeTierNamesMatchPricingDomain 守住 app 与 auth 两侧的尺寸档词表。
//
// 与质量档那条用例同源：两侧各写一份档位名是刻意的（app 不 import auth 的取值域），
// 但名字要一一对上——错一个的后果是"用户选了 4K 就被拒"，而不是一条编译错误。
func TestImageSizeTierNamesMatchPricingDomain(t *testing.T) {
	expected := []string{tierSize1K, tierSize2K, tierSize4K}
	if len(auth.ImageSizePriceTiers) != len(expected) {
		t.Fatalf("尺寸档位数不一致：app %v，auth %v", expected, auth.ImageSizePriceTiers)
	}
	for index, tier := range auth.ImageSizePriceTiers {
		if string(tier) != expected[index] {
			t.Fatalf("尺寸档第 %d 位不一致：app %q，auth %q", index, expected[index], tier)
		}
	}
}
