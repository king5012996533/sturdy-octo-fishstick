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

// TestAudioPriceTiersCoverDurationBuckets 锁定音频按时长分档的档位集合。
//
// 与图片那条用例成对读：图片按上游 quality 分档，音频按输出时长分档，两者都允许留空
// 表示"这个模型没有这个维度"。音频三档的顺序必须由短到长——账单与后台按同一顺序排列。
func TestAudioPriceTiersCoverDurationBuckets(t *testing.T) {
	if len(AudioPriceTiers) != 3 || AudioPriceTiers[0] != PriceTierShort || AudioPriceTiers[2] != PriceTierLong {
		t.Fatalf("音频三档的顺序应是 SHORT / MEDIUM / LONG，实际 %v", AudioPriceTiers)
	}
	if AudioPriceTiers[1] != PriceTierMedium {
		t.Fatalf("音频中档应复用 MEDIUM，实际 %v", AudioPriceTiers[1])
	}
	for _, tier := range AudioPriceTiers {
		if !validPriceTier("AUDIO", string(tier)) {
			t.Fatalf("音频档位 %q 应在白名单里", tier)
		}
	}
	// 留空仍然合法：配音与整首歌由上游定长，给它们分档只会逼运营配一堆用不上的价。
	if !validPriceTier("AUDIO", "") {
		t.Fatal("音频应允许留空档位")
	}
	// 图片专属的高 / 低两档不能漏进音频：它们是质量口径，与时长无关。
	// （MEDIUM 是两边共用的词，按能力区分，不在此列。）
	for _, tier := range []PriceTier{PriceTierLow, PriceTierHigh} {
		if validPriceTier("AUDIO", string(tier)) {
			t.Fatalf("音频不应接受图片档位 %q", tier)
		}
	}
}

// TestAudioUnitIsPerRequest 锁定"音频按次、视频按秒"这份口径的默认值与约束。
//
// 两者分开是有业务原因的：视频时长是用户提交时选的，音频时长要等上游产出才知道。
// 把音频也默认成按秒，等于给一个取不到的用量配了个价签。
func TestAudioUnitIsPerRequest(t *testing.T) {
	if got := DefaultUnitFor(CapabilityAudio); got != UnitPerRequest {
		t.Fatalf("AUDIO 默认单位应为 %s，实际 %s", UnitPerRequest, got)
	}
	if got := DefaultUnitFor(CapabilityVideo); got != UnitPerSecond {
		t.Fatalf("VIDEO 默认单位应保持 %s，实际 %s", UnitPerSecond, got)
	}
	for _, unit := range []PriceUnit{UnitPerRequest} {
		if !validPriceUnitForCapability(string(CapabilityAudio), string(unit)) {
			t.Fatalf("AUDIO 应接受单位 %s", unit)
		}
	}
	for _, unit := range []PriceUnit{UnitPerSecond, UnitPerImage, UnitPerMillionTokens} {
		if validPriceUnitForCapability(string(CapabilityAudio), string(unit)) {
			t.Fatalf("AUDIO 不应接受单位 %s", unit)
		}
	}
	// 其余能力不受这条约束，历史配置不能被一次口径调整连带拒掉。
	for _, capability := range []ModelCapability{CapabilityVideo, CapabilityImage, CapabilityText} {
		for _, unit := range []PriceUnit{UnitPerSecond, UnitPerImage, UnitPerMillionTokens, UnitPerRequest} {
			if !validPriceUnitForCapability(string(capability), string(unit)) {
				t.Fatalf("%s 不应被音频这条约束波及（单位 %s）", capability, unit)
			}
		}
	}
}
