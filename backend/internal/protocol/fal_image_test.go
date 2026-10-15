package protocol

import (
	"context"
	"testing"
)

// fal 把文生图与图生图拆成两条端点：{model}/text-to-image 只吃 prompt，
// {model}/edit 必填 image_urls（实测缺字段与空数组都是 422）。插件按有没有参考图选端点，
// 因此渠道上登记的是基名，不带 /edit 后缀。
//
// fal 的队列端点没法在本插件的执行模型里用：状态接口只回状态不回图片，
// 取结果的接口在任务未完成时返回 HTTP 400，而轮询遇到非 2xx 会被判失败。
// 因此插件走 fal.run 同步端点，一次 POST 直接拿最终图片。
//
// fal 只接受根层入参：曾经把参数包在 input 里、再在根层带 model 的写法，
// 队列会受理但实际被上游拒绝（{"loc":["body","prompt"]}），这里逐字段钉死。
func TestFalImageSendsFlatRootLevelBody(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-image.beeftv-plugin", "fal-image")
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model:       "openai/gpt-image-2.5/sunburst",
		Prompt:      "换成蓝色背景",
		AspectRatio: "16:9",
		Quality:     "Medium",
		ImageCount:  2,
		Images: []MediaReference{
			{URL: "https://cdn.example/anchor.png", Role: "edit_source", Order: 0},
			{DataURL: "data:image/png;base64,QUJD", Role: "edit_source", Order: 1},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// 带了参考图 → 走图生图端点。
	if create.Method != "POST" || create.Path != "/openai/gpt-image-2.5/sunburst/edit" {
		t.Fatalf("create = %#v", create)
	}
	// fal 的路径没有版本段（基址就是 https://fal.run），必须声明 originPath，
	// 否则传输层会按 OpenAI 兼容协议补上 /v1，被上游当成 owner=v1 回 404。
	if !create.OriginPath {
		t.Fatalf("create.OriginPath = false，会被补成 /v1/openai/... 而 404")
	}
	body := manifestTestBody(t, create)

	// fal 的模型 ID 只出现在 URL 路径里；根层出现 model/input 说明退回了队列写法。
	for _, forbidden := range []string{"model", "input", "version", "workflow"} {
		if _, present := body[forbidden]; present {
			t.Fatalf("body = %#v，fal 只接受根层入参，不能发送 %s", body, forbidden)
		}
	}
	if body["prompt"] != "换成蓝色背景" {
		t.Fatalf("prompt = %#v", body["prompt"])
	}
	urls, ok := body["image_urls"].([]any)
	if !ok || len(urls) != 2 || urls[0] != "https://cdn.example/anchor.png" || urls[1] != "data:image/png;base64,QUJD" {
		t.Fatalf("image_urls = %#v", body["image_urls"])
	}
	if body["num_images"] != float64(2) {
		t.Fatalf("num_images = %#v", body["num_images"])
	}
	if body["quality"] != "medium" {
		t.Fatalf("quality = %#v，期望小写透传", body["quality"])
	}
	if body["image_size"] != "landscape_16_9" {
		t.Fatalf("image_size = %#v", body["image_size"])
	}
}

// 文生图（没有参考图）不能出现 image_urls：编辑类端点必填，纯生成端点收到空数组反而会报错。
func TestFalImageOmitsEmptyReferenceList(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-image.beeftv-plugin", "fal-image")
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model: "openai/gpt-image-2.5/sunburst", Prompt: "一只橘猫",
	}})
	if err != nil {
		t.Fatal(err)
	}
	// 没有参考图 → 走文生图端点，路径里不能出现 /edit。
	if create.Path != "/openai/gpt-image-2.5/sunburst/text-to-image" {
		t.Fatalf("create.Path = %q，没有参考图时必须走 text-to-image", create.Path)
	}
	body := manifestTestBody(t, create)
	if _, present := body["image_urls"]; present {
		t.Fatalf("body = %#v，没有参考图时不应发送 image_urls", body)
	}
	if _, present := body["num_images"]; present {
		t.Fatalf("body = %#v，张数为零时不应发送 num_images", body)
	}
	if _, present := body["quality"]; present {
		t.Fatalf("body = %#v，未指定质量时应交给 fal 自己的默认值", body)
	}
	// 没给尺寸也不能发送，否则会被 fal 当成非法枚举。
	if _, present := body["image_size"]; present {
		t.Fatalf("body = %#v，比例为空时不应发送 image_size", body)
	}
}

func TestFalImageMapsAspectRatioToImageSize(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-image.beeftv-plugin", "fal-image")
	tests := []struct {
		name, aspectRatio string
		want              any
	}{
		{name: "正方形", aspectRatio: "1:1", want: "square_hd"},
		{name: "竖屏", aspectRatio: "9:16", want: "portrait_16_9"},
		{name: "四比三", aspectRatio: "4:3", want: "landscape_4_3"},
		{name: "三比二走像素", aspectRatio: "3:2", want: map[string]any{"width": float64(1536), "height": float64(1024)}},
		{name: "像素尺寸原样拆开", aspectRatio: "2752x1536", want: map[string]any{"width": float64(2752), "height": float64(1536)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
				Model: "openai/gpt-image-2.5/sunburst", Prompt: "x", AspectRatio: test.aspectRatio,
			}})
			if err != nil {
				t.Fatal(err)
			}
			got := manifestTestBody(t, create)["image_size"]
			wantMap, isMap := test.want.(map[string]any)
			if isMap {
				gotMap, ok := got.(map[string]any)
				if !ok || gotMap["width"] != wantMap["width"] || gotMap["height"] != wantMap["height"] {
					t.Fatalf("image_size = %#v, 期望 %#v", got, test.want)
				}
				return
			}
			if got != test.want {
				t.Fatalf("image_size = %#v, 期望 %#v", got, test.want)
			}
		})
	}
}

// fal 只认 auto/low/medium/high/xhigh/max。遇到别的值宁可不下发、让 fal 用自己的默认值，
// 也不能把上游不认识的档位发过去换回一个 422。
func TestFalImageDropsUnknownQuality(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-image.beeftv-plugin", "fal-image")
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model: "openai/gpt-image-2.5/sunburst", Prompt: "x", Quality: "4k",
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := manifestTestBody(t, create)
	if _, present := body["quality"]; present {
		t.Fatalf("body = %#v, 4k 不是 fal 的质量档位", body)
	}
}

// providerOptions 是插件命名空间里的逃生口：换模型时用它补 fal 的其它入参。
func TestFalImageProviderOptionsOverridesBody(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-image.beeftv-plugin", "fal-image")
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model: "openai/gpt-image-2.5/sunburst", Prompt: "x", Quality: "low",
		ProviderOptions: map[string]map[string]any{
			"fal-image": {"input": map[string]any{
				"quality":       "high",
				"output_format": "webp",
				"mask_url":      "https://cdn.example/mask.png",
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := manifestTestBody(t, create)
	if body["quality"] != "high" {
		t.Fatalf("quality = %#v, providerOptions 应覆盖内置字段", body["quality"])
	}
	if body["output_format"] != "webp" || body["mask_url"] != "https://cdn.example/mask.png" {
		t.Fatalf("body = %#v", body)
	}
}

// 同步端点直接回最终结果，所以状态只看响应体里有没有 images。
func TestFalImageParsesSyncResponse(t *testing.T) {
	adapter := officialPackageAdapter(t, "fal-image.beeftv-plugin", "fal-image")
	completed, err := adapter.ParseCreate(context.Background(), []byte(`{"images":[{"url":"https://v3.fal.media/out.png","content_type":"image/png"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusSucceeded || len(completed.Result.Images) != 1 || completed.Result.Images[0].URL != "https://v3.fal.media/out.png" {
		t.Fatalf("completed = %#v", completed)
	}
	failed, err := adapter.ParseCreate(context.Background(), []byte(`{"detail":[{"loc":["body","prompt"],"msg":"Field required"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed {
		t.Fatalf("failed = %#v, 没有 images 就必须判失败", failed)
	}
	if failed.Message == "" || failed.Message == "null" {
		t.Fatalf("failed.Message = %q, 要把 detail 带进错误文案", failed.Message)
	}
}
