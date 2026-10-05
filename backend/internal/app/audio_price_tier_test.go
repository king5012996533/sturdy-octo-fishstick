package app

import (
	"testing"

	"infinite-canvas/backend/internal/auth"
)

// TestAudioPriceTierBucketsDuration 锁定"时长 -> 档位"的边界。
//
// 边界写在 30 与 90 秒上，是为了让前台六个时长档位（15/30/60/90/120/180）正好 2/2/2
// 分完；边界一挪就会有两档落进同一格，运营配的价与用户看到的选择对不上。
func TestAudioPriceTierBucketsDuration(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{15, tierShort},
		{30, tierShort},
		{"30", tierShort},
		{31, tierMedium},
		{60, tierMedium},
		{"60", tierMedium},
		{90, tierMedium},
		{120, tierLong},
		{180, tierLong},
		{float64(180), tierLong},
		// 取不到时长时必须回落到空档，不能猜一个中间值：那会把配音这类没有时长维度的
		// 模型也塞进某一档，然后按一个不存在的价去结算。
		{nil, ""},
		{"", ""},
		{"abc", ""},
		{0, ""},
		{-30, ""},
	}
	for _, item := range cases {
		if got := audioPriceTier(item.value); got != item.want {
			t.Fatalf("时长 %#v 应落在 %q 档，实际 %q", item.value, item.want, got)
		}
	}
}

// TestAudioPriceTierMatchesPricingDomain 守住 app 与 auth 两侧的档位词表。
//
// 两侧各写一份档位名是刻意的（app 不 import auth），但名字必须一致：写错一个字，
// 取价会静默落到"未定价"，表现为"用户点生成被拒"，而不是任何一条编译错误。
func TestAudioPriceTierMatchesPricingDomain(t *testing.T) {
	expected := []string{tierShort, tierMedium, tierLong}
	if len(auth.AudioPriceTiers) != len(expected) {
		t.Fatalf("音频档位数不一致：app %v，auth %v", expected, auth.AudioPriceTiers)
	}
	for index, tier := range auth.AudioPriceTiers {
		if string(tier) != expected[index] {
			t.Fatalf("音频档位第 %d 位不一致：app %q，auth %q", index, expected[index], tier)
		}
	}
}

// TestTaskChargeTierPicksAudioDuration 覆盖计费入口真正拿到的那份参数。
func TestTaskChargeTierPicksAudioDuration(t *testing.T) {
	audio := func(options map[string]any) string {
		return taskChargeTier(ModelRequestIntent{Capability: "audio", Options: options})
	}
	if got := audio(map[string]any{"audioDuration": "180"}); got != tierLong {
		t.Fatalf("三分钟档应取 %q，实际 %q", tierLong, got)
	}
	// 配音与整首歌不带时长参数，必须落回空档（不区分档位那一行）。
	if got := audio(map[string]any{"audioVoice": "alloy"}); got != "" {
		t.Fatalf("没有时长参数时应取空档，实际 %q", got)
	}
	// 图片那条路径不能被这次改动带偏。
	image := taskChargeTier(ModelRequestIntent{Capability: "image", Options: map[string]any{"quality": "HIGH"}})
	if image != tierHigh {
		t.Fatalf("图片高质档应取 %q，实际 %q", tierHigh, image)
	}
	if got := taskChargeTier(ModelRequestIntent{Capability: "video", Options: map[string]any{"videoSeconds": 30}}); got != "" {
		t.Fatalf("视频没带清晰度时应取空档（档位只由清晰度决定），实际 %q", got)
	}
}
