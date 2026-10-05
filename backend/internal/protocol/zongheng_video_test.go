package protocol

import (
	"context"
	"testing"
)

// 纵横科技多能力 API 的「生视频」profile 逐字段钉死。
//
// 这套接口跟 OpenAI 系没有任何关系：创建是 POST /v1/videos，查询是 GET /v1/tasks/{task_id}，
// 画幅字段叫 ratio 不叫 aspect_ratio，首尾帧叫 start_frame / end_frame。下游只能传平台
// 公开模型名（上游真实模型名被平台隐藏），并且这个 profile 不支持参考视频与参考音频。
// 按别的印象改回去，上游会直接 400。
func TestZonghengVideoSendsPlatformVideoContract(t *testing.T) {
	adapter := officialPackageAdapter(t, "zongheng-video.beeftv-plugin", "zongheng-video")
	if !adapter.Metadata().RequiresPublicMediaURLs {
		t.Fatal("上游只接受公网可直读的 HTTPS 素材地址，必须声明 RequiresPublicMediaURLs")
	}

	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model:       "TTP-grok",
		Prompt:      "镜头缓慢推近，人物回头",
		Duration:    6,
		AspectRatio: "16:9",
		Resolution:  "720p",
		Images: []MediaReference{
			{Role: "first_frame", URL: "https://cdn.example.com/start.jpg"},
			{Role: "last_frame", URL: "https://cdn.example.com/end.jpg"},
			{Role: "reference_image", URL: "https://cdn.example.com/ref.jpg"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if create.Method != "POST" || create.Path != "/v1/videos" {
		t.Fatalf("create = %#v", create)
	}
	body := manifestTestBody(t, create)
	if body["model"] != "TTP-grok" || body["prompt"] != "镜头缓慢推近，人物回头" {
		t.Fatalf("body = %#v", body)
	}
	if body["duration"] != float64(6) && body["duration"] != 6 {
		t.Fatalf("duration = %#v", body["duration"])
	}
	// 画幅字段是 ratio，不是 OpenAI 的 aspect_ratio。
	if body["ratio"] != "16:9" || body["resolution"] != "720p" {
		t.Fatalf("ratio/resolution = %#v / %#v", body["ratio"], body["resolution"])
	}
	if _, present := body["aspect_ratio"]; present {
		t.Fatalf("body = %#v，上游只认 ratio", body)
	}
	// 首尾帧各走独立字段，参考图进 images；三张图不能互相串位。
	if body["start_frame"] != "https://cdn.example.com/start.jpg" {
		t.Fatalf("start_frame = %#v", body["start_frame"])
	}
	if body["end_frame"] != "https://cdn.example.com/end.jpg" {
		t.Fatalf("end_frame = %#v", body["end_frame"])
	}
	images, ok := body["images"].([]any)
	if !ok || len(images) != 1 || images[0] != "https://cdn.example.com/ref.jpg" {
		t.Fatalf("images = %#v，只应包含参考图，首尾帧走独立字段", body["images"])
	}
}

func TestZonghengVideoRejectsUnsupportedReferences(t *testing.T) {
	adapter := officialPackageAdapter(t, "zongheng-video.beeftv-plugin", "zongheng-video")
	cases := []struct {
		name    string
		request GenerationRequest
	}{
		{name: "参考视频", request: GenerationRequest{Model: "TTP-grok", Prompt: "x", Videos: []MediaReference{{URL: "https://cdn.example.com/a.mp4"}}}},
		{name: "参考音频", request: GenerationRequest{Model: "TTP-grok", Prompt: "x", Audios: []MediaReference{{URL: "https://cdn.example.com/a.wav"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := adapter.BuildCreate(context.Background(), RequestContext{Request: tc.request}); err == nil {
				t.Fatal("上游不支持该素材类型，必须在发请求前拦掉")
			}
		})
	}
}

func TestZonghengVideoParsesCreateAndPoll(t *testing.T) {
	adapter := officialPackageAdapter(t, "zongheng-video.beeftv-plugin", "zongheng-video")

	// 创建响应把任务号放在顶层 task_id，不是 OpenAI 的 id。
	created, err := adapter.ParseCreate(context.Background(), []byte(`{"code":0,"success":true,"task_id":"vid_abc","status":"processing","next_poll_seconds":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.TaskID != "vid_abc" || created.Status != StatusProcessing {
		t.Fatalf("create = %#v", created)
	}

	poll, err := adapter.BuildPoll(context.Background(), PollContext{TaskID: "vid_abc"})
	if err != nil {
		t.Fatal(err)
	}
	if poll.Method != "GET" || poll.Path != "/v1/tasks/vid_abc" {
		t.Fatalf("poll = %#v", poll)
	}

	// 完成响应有多个等价地址字段，取到任意一个都必须能出片。
	succeeded, err := adapter.ParsePoll(context.Background(), PollContext{TaskID: "vid_abc"},
		[]byte(`{"task_id":"vid_abc","status":"succeeded","video_url":"https://cdn.example.com/v.mp4","download_url":"https://cdn.example.com/v.mp4?download=1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if succeeded.Status != StatusSucceeded || succeeded.Result == nil || len(succeeded.Result.Videos) == 0 {
		t.Fatalf("poll = %#v", succeeded)
	}
	if succeeded.Result.Videos[0].URL != "https://cdn.example.com/v.mp4" {
		t.Fatalf("videos = %#v", succeeded.Result.Videos)
	}

	// 运行中不能当成功；失败要带上上游原因。
	running, err := adapter.ParsePoll(context.Background(), PollContext{TaskID: "vid_abc"}, []byte(`{"task_id":"vid_abc","status":"processing"}`))
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != StatusProcessing || running.Result != nil {
		t.Fatalf("poll = %#v", running)
	}
	failed, err := adapter.ParsePoll(context.Background(), PollContext{TaskID: "vid_abc"}, []byte(`{"code":400,"success":false,"message":"提示词未通过内容审核","error":"提示词未通过内容审核"}`))
	if err != nil {
		t.Fatal(err)
	}
	if failed.Message != "提示词未通过内容审核" {
		t.Fatalf("message = %#v", failed.Message)
	}
}

func TestZonghengVideoErrorBodyKeepsUpstreamReason(t *testing.T) {
	adapter := officialPackageAdapter(t, "zongheng-video.beeftv-plugin", "zongheng-video")
	// 平台的错误体是 {code, success, message, error}，message 与 error 同值；
	// 必须把原因带出来，否则用户只看到「生成失败」。
	body := []byte(`{"code":402,"success":false,"message":"积分不足","error":"积分不足"}`)
	poll, err := adapter.ParsePoll(context.Background(), PollContext{TaskID: "vid_abc"}, body)
	if err != nil {
		t.Fatal(err)
	}
	if poll.Message != "积分不足" {
		t.Fatalf("message = %#v", poll.Message)
	}
}
