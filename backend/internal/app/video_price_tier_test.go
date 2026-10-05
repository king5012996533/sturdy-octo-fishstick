package app

import (
	"testing"

	"infinite-canvas/backend/internal/auth"
)

// TestTaskChargeTierPicksVideoResolution 覆盖"用户选的清晰度决定按哪一档取价"。
func TestTaskChargeTierPicksVideoResolution(t *testing.T) {
	video := func(options map[string]any) string {
		return taskChargeTier(ModelRequestIntent{Capability: "video", Options: options})
	}
	cases := []struct {
		value any
		want  string
	}{
		{"480p", "480P"},
		{"720p", "720P"},
		{"1080p", "1080P"},
		// 非标准写法原样带上：能力合同里就有 768P、960P 这类档，认不出就等于让运营白配。
		{"768P", "768P"},
		{"960P", "960P"},
		// 面板没选清晰度（auto / 空）时不能猜一档，交给「不区分」那一行兜底。
		{"auto", ""},
		{"", ""},
		{nil, ""},
		{480, ""},
		{"超清", ""},
	}
	for _, item := range cases {
		options := map[string]any{"videoSeconds": 15}
		if item.value != nil {
			options["vquality"] = item.value
		}
		if got := video(options); got != item.want {
			t.Fatalf("清晰度 %#v 应取档位 %q，实际 %q", item.value, item.want, got)
		}
	}

	// 只有带清晰度参数的视频才分档；音频与图片那两条路径不能被带偏。
	if got := video(map[string]any{"videoSeconds": 15}); got != "" {
		t.Fatalf("没带清晰度时应取空档，实际 %q", got)
	}
	if got := taskChargeTier(ModelRequestIntent{Capability: "image", Options: map[string]any{"quality": "HIGH"}}); got != tierHigh {
		t.Fatalf("图片高质档应取 %q，实际 %q", tierHigh, got)
	}
	if got := taskChargeTier(ModelRequestIntent{Capability: "audio", Options: map[string]any{"audioDuration": "180"}}); got != tierLong {
		t.Fatalf("音频长档应取 %q，实际 %q", tierLong, got)
	}
}

// TestVideoResolutionTierMatchesPricingDomain 守住 app 与 auth 两侧的形状规则。
//
// 两侧各写一份是刻意的（app 不 import auth），但形状必须一致：一边放行一边拒绝，
// 取价会静默落到"未定价"，表现为"用户点生成被拒"，而不是任何一条编译错误。
func TestVideoResolutionTierMatchesPricingDomain(t *testing.T) {
	samples := []string{"480P", "720P", "768P", "1080P", "1440P", "2160P", "2K", "360P", "960P", "LOW", "SHORT", "720PP", "P720", "超清", "4K8", ""}
	for _, sample := range samples {
		appSide := isVideoResolutionTierShape(sample)
		authSide := auth.IsVideoResolutionPriceTier(sample)
		if appSide != authSide {
			t.Fatalf("档位形状判定不一致：%q app=%v auth=%v", sample, appSide, authSide)
		}
	}
}
