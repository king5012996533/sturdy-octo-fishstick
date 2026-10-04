package app

import (
	"strings"
	"testing"
)

// 只有 MiniMax 原生结算的协议才按"前 5 张免费、第 6 张起加收"记账。
func TestReferenceImageSurchargeBeyondFreeQuota(t *testing.T) {
	svc := &Service{}
	miniMax := func(count int) map[string]any {
		images := make([]any, 0, count)
		for i := 0; i < count; i++ {
			images = append(images, map[string]any{"storageKey": "resource:img"})
		}
		return map[string]any{
			"config":          map[string]any{"interfaceType": "minimax-video"},
			"referenceImages": images,
		}
	}

	if credits, note := svc.referenceImageSurcharge(miniMax(5)); credits != 0 || note != "" {
		t.Fatalf("免费额度内不该加收，实际 %d 分 %q", credits, note)
	}
	credits, note := svc.referenceImageSurcharge(miniMax(9))
	if credits != 4*15 {
		t.Fatalf("9 张应加收 60 分，实际 %d", credits)
	}
	if !strings.Contains(note, "9 张") || !strings.Contains(note, "15 分/张") {
		t.Fatalf("流水备注要说清张数与单价，实际 %q", note)
	}

	// 别的协议不套这条规则：Seedance 的上游按条结算，参考图不计价。
	seedance := miniMax(9)
	seedance["config"] = map[string]any{"interfaceType": "newapi-channel-2"}
	if credits, note := svc.referenceImageSurcharge(seedance); credits != 0 || note != "" {
		t.Fatalf("非 MiniMax 协议被误加收：%d 分 %q", credits, note)
	}
}

// 试算端没有素材数组，只能带张数摘要；摘要绝不能盖过提交时的数组。
func TestReferenceImageCountPrefersSubmittedArray(t *testing.T) {
	svc := &Service{}
	summary := map[string]any{
		"config":              map[string]any{"interfaceType": "minimax-video"},
		"referenceImageCount": float64(9),
	}
	if credits, _ := svc.referenceImageSurcharge(summary); credits != 4*15 {
		t.Fatalf("试算摘要应能算出 60 分，实际 %d", credits)
	}
	// 摘要说 9 张、数组只有 5 张：以数组为准，客户端改不动实扣。
	summary["referenceImages"] = []any{
		map[string]any{"storageKey": "resource:a"},
		map[string]any{"storageKey": "resource:b"},
		map[string]any{"storageKey": "resource:c"},
		map[string]any{"storageKey": "resource:d"},
		map[string]any{"storageKey": "resource:e"},
	}
	if credits, _ := svc.referenceImageSurcharge(summary); credits != 0 {
		t.Fatalf("数组优先，5 张应免费，实际 %d", credits)
	}
	if got := referenceImageCountOf(map[string]any{"referenceImageCount": "7"}); got != 7 {
		t.Fatalf("字符串摘要应可解析，实际 %d", got)
	}
	if got := referenceImageCountOf(map[string]any{"referenceImageCount": -3}); got != 0 {
		t.Fatalf("负数摘要按 0 处理，实际 %d", got)
	}
}
