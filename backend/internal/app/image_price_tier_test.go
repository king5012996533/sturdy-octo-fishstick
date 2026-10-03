package app

import (
	"testing"

	"infinite-canvas/backend/internal/auth"
)

// TestImagePriceTierMatchesPricingDomain 守住 app 与 auth 两侧的图片档位词表。
//
// 与音频那份用例同源：两侧各写一份档位名是刻意的（app 不 import auth），但名字与顺序都要
// 一致。xhigh / max 是后加的两档，任何一侧漏改都会让这两档在计费口取不到价——表现为
// "用户选了极高画质就被拒"，而不是一条编译错误。
func TestImagePriceTierMatchesPricingDomain(t *testing.T) {
	expected := []string{tierLow, tierMedium, tierHigh, tierXHigh, tierMax}
	if len(auth.ImagePriceTiers) != len(expected) {
		t.Fatalf("图片档位数不一致：app %v，auth %v", expected, auth.ImagePriceTiers)
	}
	for index, tier := range auth.ImagePriceTiers {
		if string(tier) != expected[index] {
			t.Fatalf("图片档位第 %d 位不一致：app %q，auth %q", index, expected[index], tier)
		}
	}
}
