package protocol

import (
	"context"
	"testing"
)

// 混元生图 3.5 走 TokenHub 的 WAND 同步口：一次请求直接出图，没有任务提交与轮询。
// 官方参数表只认 model、messages、size、seed、generate_max_pixels、resize_max_pixels、
// session、footnote、use_search_tool：把 OpenAI 的 n / quality / response_format 之类字段
// 塞进去只会得到 400。这里逐字段钉死线协议，防止后续有人按"OpenAI 兼容"的印象改回去。
func TestTencentHunyuanImage35SendsMessagesProtocol(t *testing.T) {
	adapter := officialPackageAdapter(t, "tencent-hunyuan-image-35.beeftv-plugin", "tencent-hunyuan-image-35")
	if adapter.Metadata().RequiresPublicMediaURLs {
		t.Fatal("TokenHub 接受 data URL 参考图，不应强制要求公共媒体 URL")
	}
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model: "hy-image-v3.5-preview", Prompt: "雨夜屋檐下的侠客", AspectRatio: "1824x1024",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if create.Method != "POST" || create.Path != "/v1/wand/hunyuan-image/v35-generation" {
		t.Fatalf("create = %#v", create)
	}
	body := manifestTestBody(t, create)
	if body["model"] != "hy-image-v3.5-preview" {
		t.Fatalf("model = %#v", body["model"])
	}
	if body["size"] != "1824x1024" {
		t.Fatalf("size = %#v, 精确像素必须原样透传", body["size"])
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v，本轮指令只能有一条 user 消息", body["messages"])
	}
	message, _ := messages[0].(map[string]any)
	if message["role"] != "user" {
		t.Fatalf("role = %#v", message["role"])
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v，文生图只有一个文本片段", message["content"])
	}
	text, _ := content[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "雨夜屋檐下的侠客" {
		t.Fatalf("content[0] = %#v", content[0])
	}
	// 官方参数表之外的字段会被上游按"不支持该字段"拒绝，逐个钉死。
	for _, forbidden := range []string{"prompt", "n", "quality", "response_format", "images", "max_tokens"} {
		if _, present := body[forbidden]; present {
			t.Fatalf("body = %#v, TokenHub 参数表没有 %s 字段，不能发送", body, forbidden)
		}
	}
}

func TestTencentHunyuanImage35PutsReferencesInUserMessage(t *testing.T) {
	adapter := officialPackageAdapter(t, "tencent-hunyuan-image-35.beeftv-plugin", "tencent-hunyuan-image-35")
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model: "hy-image-v3.5-preview", Prompt: "保持人物，改成冬装", AspectRatio: "1024x1360",
		Images: []MediaReference{
			{URL: "https://cdn.example/anchor.png", Role: "edit_source", Order: 0},
			{DataURL: "data:image/png;base64,QUJD", Role: "edit_source", Order: 1},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := manifestTestBody(t, create)
	messages, _ := body["messages"].([]any)
	message, _ := messages[0].(map[string]any)
	content, _ := message["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("content = %#v，期望一段文本加两张参考图", content)
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("content[0] = %#v，文本必须排在参考图之前", content[0])
	}
	// 有 Data URL 时优先 Base64：自部署环境没有公网出口，data URL 是唯一可靠通道。
	for index, want := range []string{"https://cdn.example/anchor.png", "data:image/png;base64,QUJD"} {
		part, _ := content[index+1].(map[string]any)
		if part["type"] != "image_url" {
			t.Fatalf("content[%d] = %#v", index+1, content[index+1])
		}
		imageURL, _ := part["image_url"].(map[string]any)
		if imageURL["url"] != want {
			t.Fatalf("content[%d].image_url.url = %#v，期望 %q", index+1, imageURL["url"], want)
		}
	}
}

func TestTencentHunyuanImage35SizeFollowsPixelsOrRatio(t *testing.T) {
	adapter := officialPackageAdapter(t, "tencent-hunyuan-image-35.beeftv-plugin", "tencent-hunyuan-image-35")
	tests := []struct {
		name        string
		aspectRatio string
		wantSize    any
	}{
		{name: "精确像素原样透传", aspectRatio: "2752x1536", wantSize: "2752x1536"},
		// 上游只接受 WxH。画布与 Agent 传比例时按 1K 档兜底，避免同一个模型在不同入口一个能用一个报错。
		{name: "比例兜底到 1K 像素", aspectRatio: "16:9", wantSize: "1824x1024"},
		{name: "auto 交给模型决定宽高", aspectRatio: "auto", wantSize: nil},
		{name: "空比例同样省略 size", aspectRatio: "", wantSize: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
				Model: "hy-image-v3.5-preview", Prompt: "circle", AspectRatio: test.aspectRatio,
			}})
			if err != nil {
				t.Fatal(err)
			}
			got, present := manifestTestBody(t, create)["size"]
			if test.wantSize == nil {
				if present {
					t.Fatalf("size = %#v, %q 不得下发 size", got, test.aspectRatio)
				}
				return
			}
			if got != test.wantSize {
				t.Fatalf("size = %#v, 期望 %q", got, test.wantSize)
			}
		})
	}
}

func TestTencentHunyuanImage35ForwardsProviderOptions(t *testing.T) {
	adapter := officialPackageAdapter(t, "tencent-hunyuan-image-35.beeftv-plugin", "tencent-hunyuan-image-35")
	create, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{
		Model: "hy-image-v3.5-preview", Prompt: "雪山日出", AspectRatio: "auto",
		ProviderOptions: map[string]map[string]any{
			"tencent-hunyuan-image-35": {"session": "chat-1", "seed": 42, "footnote": "KinoTV", "generate_max_pixels": 4194304},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := manifestTestBody(t, create)
	if body["session"] != "chat-1" {
		t.Fatalf("session = %#v", body["session"])
	}
	if manifestInt(body["seed"]) != 42 {
		t.Fatalf("seed = %#v", body["seed"])
	}
	if body["footnote"] != "KinoTV" {
		t.Fatalf("footnote = %#v", body["footnote"])
	}
	if manifestInt(body["generate_max_pixels"]) != 4194304 {
		t.Fatalf("generate_max_pixels = %#v", body["generate_max_pixels"])
	}
	if _, present := body["resize_max_pixels"]; present {
		t.Fatal("未配置的扩展键不得出现空值")
	}
}

func TestTencentHunyuanImage35ReadsFinalImageAndErrors(t *testing.T) {
	adapter := officialPackageAdapter(t, "tencent-hunyuan-image-35.beeftv-plugin", "tencent-hunyuan-image-35")
	result, err := adapter.ParseCreate(context.Background(), []byte(`{
		"id":"1374200352-WandImage-085e",
		"object":"image.chat.completion.chunk",
		"choices":[{"index":0,"delta":{"type":"image","image":{"url":"https://aigc-output-image-1326893053.cos.ap-guangzhou.myqcloud.com/xxx/main.png?sign=1","width":4096,"height":4096,"source":"generate"}},"finish_reason":null}],
		"tokenhub_usage":{"total_tokens":20000}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSucceeded || result.Result == nil || len(result.Result.Images) != 1 {
		t.Fatalf("parse = %#v", result)
	}
	image := result.Result.Images[0]
	if image.URL == "" || !image.Ephemeral {
		t.Fatalf("image = %#v，上游地址是 12 小时临时链接，必须标为 ephemeral 由宿主转存", image)
	}

	// 终态失败帧：HTTP 200 但带 error 与 finish_reason=error（内容安全拦截就是这一种）。
	for _, body := range []string{
		`{"choices":[{"index":0,"delta":{},"finish_reason":"error"}],"error":{"type":"invalid_request_error","code":"content_filter","message":"input moderation rejected"}}`,
		`{"error":{"code":"400004","message":"The model or service ID does not exist"}}`,
	} {
		failed, err := adapter.ParseCreate(context.Background(), []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if failed.Status != StatusFailed {
			t.Fatalf("status = %q, 上游错误必须映射成失败而不是空结果", failed.Status)
		}
		if failed.Message == "" {
			t.Fatalf("错误文案缺失：%#v", failed)
		}
	}
}
