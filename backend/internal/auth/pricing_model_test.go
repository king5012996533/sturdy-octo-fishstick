package auth

import "testing"

// TestValidPriceTierFollowsCapability 锁定"哪个能力能用哪些档位"这份契约。
//
// 后台表单、保存入口与取价入口三处都要按它来判，档位认错就意味着按另一个成本出货；
// 图片那三档与上游 quality 一一对应，空档是"不区分质量"，不是"随便哪一档"。
func TestValidPriceTierFollowsCapability(t *testing.T) {
	valid := map[string][]string{
		"TEXT":  {"CACHE", "INPUT", "OUTPUT"},
		"IMAGE": {"", "LOW", "MEDIUM", "HIGH"},
		"VIDEO": {""},
		"AUDIO": {""},
	}
	for capability, allowed := range valid {
		for _, tier := range allowed {
			if !validPriceTier(capability, tier) {
				t.Fatalf("%s 应接受档位 %q", capability, tier)
			}
		}
	}
	rejected := []struct{ capability, tier string }{
		{"TEXT", ""},
		{"TEXT", "LOW"},
		{"IMAGE", "CACHE"},
		{"IMAGE", "AUTO"},
		{"VIDEO", "HIGH"},
		{"AUDIO", "INPUT"},
		{"WEIRD", ""},
	}
	for _, test := range rejected {
		if validPriceTier(test.capability, test.tier) {
			t.Fatalf("%s 不应接受档位 %q", test.capability, test.tier)
		}
	}
}

// TestPriceTierSetsMatchCapability 覆盖两个档位集合本身：顺序固定，且图片三档都在白名单里。
//
// 顺序不是装饰：账单要能按同一顺序复核，集合写漏一档就等于那个档位永远配不上价。
func TestPriceTierSetsMatchCapability(t *testing.T) {
	if len(TextPriceTiers) != 3 || TextPriceTiers[0] != PriceTierCache || TextPriceTiers[2] != PriceTierOutput {
		t.Fatalf("文本三档的顺序应是 CACHE / INPUT / OUTPUT，实际 %v", TextPriceTiers)
	}
	if len(ImagePriceTiers) != 3 || ImagePriceTiers[0] != PriceTierLow || ImagePriceTiers[2] != PriceTierHigh {
		t.Fatalf("图片三档的顺序应是 LOW / MEDIUM / HIGH，实际 %v", ImagePriceTiers)
	}
	for _, tier := range ImagePriceTiers {
		if !validPriceTier("IMAGE", string(tier)) {
			t.Fatalf("图片档位 %q 应在白名单里", tier)
		}
	}
}
