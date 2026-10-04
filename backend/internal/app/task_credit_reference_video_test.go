package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

// 只有上游按「输入 + 输出」总秒数结算的协议才补参考视频的输入秒数。别的协议补了就
// 等于在没有成本依据的情况下改价，所以这份名单必须显式。
func TestBillsReferenceVideoSecondsOnlyForMiniMaxProtocol(t *testing.T) {
	cases := map[string]bool{
		"minimax-video":        true,
		"newapi-channel-2":     false,
		"volcengine-ark-video": false,
		"agnes-video":          false,
		"":                     false,
	}
	for interfaceType, expected := range cases {
		if got := billsReferenceVideoSeconds(interfaceType); got != expected {
			t.Fatalf("billsReferenceVideoSeconds(%q) = %v, want %v", interfaceType, got, expected)
		}
	}
}

// 5.04 秒的参考视频按上游口径记 6 秒（实测回执 input_seconds=6），所以这里必须向上取整。
func TestReferenceVideoSecondsCeilsAndSumsOwnedResources(t *testing.T) {
	svc := newResourceTestService(t)
	resources := []model.Resource{
		{ID: "clip-a", UserID: "user-1", Kind: "video", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "a.mp4", MimeType: "video/mp4", DurationMs: 5040},
		{ID: "clip-b", UserID: "user-1", Kind: "video", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "b.mp4", MimeType: "video/mp4", DurationMs: 2000},
		{ID: "no-duration", UserID: "user-1", Kind: "video", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "c.mp4", MimeType: "video/mp4"},
		{ID: "foreign", UserID: "user-2", Kind: "video", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "d.mp4", MimeType: "video/mp4", DurationMs: 9000},
	}
	for i := range resources {
		if err := svc.repo.CreateResource(&resources[i]); err != nil {
			t.Fatal(err)
		}
	}

	input := map[string]any{"referenceVideos": []any{
		map[string]any{"storageKey": "resource:clip-a"},
		map[string]any{"storageKey": "resource:clip-b"},
		map[string]any{"storageKey": "resource:no-duration"},
		map[string]any{"storageKey": "resource:foreign"},
		map[string]any{"storageKey": "resource:missing"},
		map[string]any{"url": "https://cdn.example/clip.mp4"},
		map[string]any{"dataUrl": "data:video/mp4;base64,AAAA"},
		"not-an-object",
	}}
	if got := svc.referenceVideoSeconds("user-1", input); got != 8 {
		t.Fatalf("referenceVideoSeconds = %d, want 8（6 + 2，取不到时长的不猜）", got)
	}
	if got := svc.referenceVideoSeconds("user-1", map[string]any{}); got != 0 {
		t.Fatalf("无参考视频时应为 0，实际 %d", got)
	}
}

// 提交预扣与试算共用 taskChargeQuantityFor，这条钉住"两种协议各自加不加输入秒数"。
func TestTaskChargeQuantityAddsReferenceSecondsOnlyForMiniMax(t *testing.T) {
	svc := newResourceTestService(t)
	resource := model.Resource{ID: "clip", UserID: "user-1", Kind: "video", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "a.mp4", MimeType: "video/mp4", DurationMs: 5040}
	if err := svc.repo.CreateResource(&resource); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{ID: "task-1", UserID: "user-1", Type: "video"}
	intent := ModelRequestIntent{Capability: "video", Options: map[string]any{"videoSeconds": "4"}}

	minimaxInput := map[string]any{
		"config":          map[string]any{"interfaceType": string(model.ChannelInterfaceMiniMaxVideo)},
		"referenceVideos": []any{map[string]any{"storageKey": "resource:clip"}},
	}
	if got := svc.taskChargeQuantityFor(task, minimaxInput, intent); got != 10 {
		t.Fatalf("MiniMax 视频用量 = %d, want 10（输出 4 + 输入 6）", got)
	}

	seedanceInput := map[string]any{
		"config":          map[string]any{"interfaceType": string(model.ChannelInterfaceNewAPIChannel2)},
		"referenceVideos": []any{map[string]any{"storageKey": "resource:clip"}},
	}
	if got := svc.taskChargeQuantityFor(task, seedanceInput, intent); got != 4 {
		t.Fatalf("其他协议用量 = %d, want 4（只按输出秒数，不擅自改价）", got)
	}

	imageIntent := ModelRequestIntent{Capability: "image", Options: map[string]any{"count": "2"}}
	if got := svc.taskChargeQuantityFor(task, minimaxInput, imageIntent); got != 2 {
		t.Fatalf("图片用量 = %d, want 2", got)
	}
}
