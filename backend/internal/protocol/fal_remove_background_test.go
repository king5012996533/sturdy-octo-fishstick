package protocol

import (
	"context"
	"strings"
	"testing"
)

// BiRefNet 与 Crisp 一样，是 fal 目录里的一条独立 app 端点，不是"某个模型的 /edit"：
// POST https://fal.run/fal-ai/birefnet/v2。把渠道上的模型标识当成模型名去拼路径
// （/fal-ai/birefnet/v2/edit 之类）只会拿到 404，所以路径在 manifest 里写死。
//
// 上游只吃一个字段 image_url（外加可选的 operating_resolution 等分段参数），
// 因此这里逐字段钉死请求体：多出来的 prompt 或 model 都不是 BiRefNet 的入参。
func TestFalRemoveBackgroundSendsOnlyImageURL(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-remove-background.beeftv-plugin", "fal-remove-background")
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model:  "fal-ai/birefnet/v2",
		Prompt: "移除背景",
		Images: []MediaReference{{DataURL: "data:image/png;base64,QUJD", Role: "edit_source", Order: 0}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if create.Method != "POST" || create.Path != "/fal-ai/birefnet/v2" {
		t.Fatalf("create = %#v，BiRefNet 的端点与模型名无关，必须写死", create)
	}
	// fal 的路径没有版本段（基址就是 https://fal.run），必须声明 originPath，
	// 否则传输层会按 OpenAI 兼容协议补上 /v1，被上游当成另一条路径回 404。
	if !create.OriginPath {
		t.Fatalf("create.OriginPath = false，会被补成 /v1/fal-ai/... 而 404")
	}
	body := manifestTestBody(t, create)
	if len(body) != 1 {
		t.Fatalf("body = %#v，BiRefNet 只接受 image_url（可选字段走 providerOptions）", body)
	}
	if body["image_url"] != "data:image/png;base64,QUJD" {
		t.Fatalf("image_url = %#v", body["image_url"])
	}
	// 画布提交的 prompt 只是任务侧的非空校验，不能漏进请求体。
	for _, forbidden := range []string{"prompt", "model", "input", "num_images", "image_size"} {
		if _, present := body[forbidden]; present {
			t.Fatalf("body = %#v，BiRefNet 不接受 %s", body, forbidden)
		}
	}
}

// 抠图没有原图这件事本身就不成立：校验规则要在本地拦下，而不是发一次注定失败的上游请求。
func TestFalRemoveBackgroundRejectsMissingSourceImage(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-remove-background.beeftv-plugin", "fal-remove-background")
	_, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model:  "fal-ai/birefnet/v2",
		Prompt: "移除背景",
	}})
	if err == nil {
		t.Fatal("没有原图时必须报错")
	}
	if !strings.Contains(err.Error(), "一张原图") {
		t.Fatalf("err = %v，校验文案要直接告诉用户缺什么", err)
	}
}

// 多张参考图只取第一张：BiRefNet 只接受一张输入，静默多发会让用户以为后一张也参与了抠图。
func TestFalRemoveBackgroundUsesFirstImageOnly(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-remove-background.beeftv-plugin", "fal-remove-background")
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model: "fal-ai/birefnet/v2",
		Images: []MediaReference{
			{URL: "https://cdn.example/first.png", Role: "edit_source", Order: 0},
			{URL: "https://cdn.example/second.png", Role: "edit_source", Order: 1},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := manifestTestBody(t, create)["image_url"]; got != "https://cdn.example/first.png" {
		t.Fatalf("image_url = %#v，只该取第一张", got)
	}
}

// 同步端点直接回最终结果，所以状态只看响应体里有没有 image.url。
// 抠图的产物是带 alpha 的 PNG，响应里的 content_type 不进产物（尺寸与类型由宿主下载真实文件后读取）。
func TestFalRemoveBackgroundParsesSyncResponse(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-remove-background.beeftv-plugin", "fal-remove-background")
	completed, err := adapter.ParseCreate(context.Background(), []byte(`{"image":{"url":"https://v3.fal.media/out.png","content_type":"image/png","width":4096,"height":2731},"mask_image":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusSucceeded || len(completed.Result.Images) != 1 || completed.Result.Images[0].URL != "https://v3.fal.media/out.png" {
		t.Fatalf("completed = %#v", completed)
	}
	failed, err := adapter.ParseCreate(context.Background(), []byte(`{"detail":[{"loc":["body","image_url"],"msg":"Field required"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed {
		t.Fatalf("failed = %#v，没有 image.url 就必须判失败", failed)
	}
	if failed.Message == "" || failed.Message == "null" {
		t.Fatalf("failed.Message = %q，要把 detail 带进错误文案", failed.Message)
	}
}
