package app

import "testing"

func TestProviderProgressFromResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
		ok   bool
	}{
		{name: "direct number", body: `{"progress":35}`, want: 35, ok: true},
		{name: "nested percent string", body: `{"data":{"task":{"progress_percent":"68%"}}}`, want: 68, ok: true},
		{name: "fraction", body: `{"result":{"progress":0.42}}`, want: 42, ok: true},
		{name: "fractional explicit percent", body: `{"progress":"0.5%"}`, want: 1, ok: true},
		{name: "camel case", body: `{"operation":{"progressPercentage":87.6}}`, want: 88, ok: true},
		{name: "unrelated completion tokens", body: `{"usage":{"completion_tokens":120}}`, ok: false},
		{name: "invalid range", body: `{"progress":140}`, ok: false},
		{name: "not json", body: `not-json`, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := providerProgressFromResponse([]byte(tt.body))
			if got != tt.want || ok != tt.ok {
				t.Fatalf("providerProgressFromResponse() = (%d, %v), want (%d, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestTaskUsesUpstreamReportedProgress(t *testing.T) {
	for _, taskType := range []string{"canvas_image", "canvas_video", "video_shot"} {
		if !taskUsesUpstreamReportedProgress(taskType) {
			t.Fatalf("task type %q should use upstream progress", taskType)
		}
	}
	for _, taskType := range []string{"canvas_text", "canvas_audio", "text_generation"} {
		if taskUsesUpstreamReportedProgress(taskType) {
			t.Fatalf("task type %q should keep internal progress", taskType)
		}
	}
}

// 上游报满进度但结果还没取回时，阶段文案必须如实说明，否则界面停在最大进度上，
// 用户看到的就是"100% 卡死"（线上真实反馈）。
func TestProviderProgressStageForRunningTask(t *testing.T) {
	relayCompleted := `{"code":0,"status":"succeeded","progress":100,"delivery_status":"ready","result_url":"https://relay.example/api/video-content/vid_1","download_url":"https://relay.example/api/video-content/vid_1?download=1"}`
	tests := []struct {
		name     string
		body     string
		progress int
		stale    bool
		want     string
	}{
		{name: "halfway", body: `{"progress":60}`, progress: 60, want: "上游生成中"},
		{name: "full without result locator", body: `{"progress":100}`, progress: 100, want: "上游生成中"},
		{name: "full with relative url is not a locator", body: `{"progress":100,"result_url":"/videos/1/content"}`, progress: 100, want: "上游生成中"},
		{name: "full with result locator", body: relayCompleted, progress: 100, want: "上游已生成，正在取回结果"},
		{name: "stale beats generic running stage", body: `{"progress":60}`, progress: 60, want: videoPollStaleStage, stale: true},
		{name: "stale does not hide result retrieval", body: relayCompleted, progress: 100, want: "上游已生成，正在取回结果", stale: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerProgressStage([]byte(tt.body), tt.progress, tt.stale); got != tt.want {
				t.Fatalf("providerProgressStage() = %q, want %q", got, tt.want)
			}
		})
	}
}
