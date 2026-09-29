package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/protocol"
)

func TestProtocolRequestMapsOpenAICanvasRatioToPixelSize(t *testing.T) {
	profile := DefaultImageCapabilityConfig("openai-image", "gpt-image-2")
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "a landscape",
		Config:          providerConfig{Model: "gpt-image-2", InterfaceType: "openai-image", Size: "16:9", Quality: "2k"},
		ImageCapability: profile,
	})
	if request.AspectRatio != "1824x1024" {
		t.Fatalf("AspectRatio = %q, want 1824x1024", request.AspectRatio)
	}
	if request.Output.AspectRatio != "1824x1024" {
		t.Fatalf("Output.AspectRatio = %q, want 1824x1024", request.Output.AspectRatio)
	}
	if request.Quality != "2k" {
		t.Fatalf("Quality = %q, want 2k for plugin-side mapping", request.Quality)
	}
}

func TestProtocolRequestKeepsSeedreamCanvasRatioWhenCapabilityUsesSize(t *testing.T) {
	profile := DefaultImageCapabilityConfig("volcengine-ark-image", "doubao-seedream-5-0-260128")
	if profile.Size.Parameter != "size" {
		t.Fatalf("ark image parameter = %q, want size", profile.Size.Parameter)
	}
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "a landscape",
		Config:          providerConfig{Model: "doubao-seedream-5-0-260128", InterfaceType: "volcengine-ark-image", Size: "16:9"},
		ImageCapability: profile,
	})
	if request.AspectRatio != "16:9" {
		t.Fatalf("AspectRatio = %q, want canvas 16:9 so Seedream can map 2560x1440", request.AspectRatio)
	}
}

func TestProtocolSeedreamPluginMapsCanvasRatioNotHostPixels(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "volcengine-ark-seedream", "volcengine-ark-image")
	profile := DefaultImageCapabilityConfig("volcengine-ark-image", "doubao-seedream-5-0-260128")
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "a landscape",
		Config:          providerConfig{Model: "doubao-seedream-5-0-260128", InterfaceType: "volcengine-ark-image", Size: "16:9"},
		ImageCapability: profile,
	})
	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := marshalProtocolBody(t, spec.Body)
	if body["size"] != "2560x1440" {
		t.Fatalf("seedream size = %#v, want 2560x1440", body["size"])
	}
}

func TestProtocolQwenImagePluginMapsCanvasRatio(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "dashscope-qwen-image", "dashscope-qwen-image")
	profile := DefaultImageCapabilityConfig("dashscope-qwen-image", "qwen-image-3.0-pro")
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "a landscape",
		Config:          providerConfig{Model: "qwen-image-3.0-pro", InterfaceType: "dashscope-qwen-image", Size: "16:9"},
		ImageCapability: profile,
	})
	if request.AspectRatio != "16:9" {
		t.Fatalf("AspectRatio = %q, want 16:9", request.AspectRatio)
	}
	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := marshalProtocolBody(t, spec.Body)
	parameters, _ := body["parameters"].(map[string]any)
	if parameters["size"] != "1536*864" {
		t.Fatalf("qwen size = %#v, want 1536*864", parameters["size"])
	}
}

func TestProtocolGeminiImageKeepsAspectRatio(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "google-gemini-image", "gemini-image")
	profile := DefaultImageCapabilityConfig("gemini-image", "gemini-3-pro-image-preview")
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "a landscape",
		Config:          providerConfig{Model: "gemini-3-pro-image-preview", InterfaceType: "gemini-image", Size: "16:9", Quality: "2k"},
		ImageCapability: profile,
	})
	if request.AspectRatio != "16:9" {
		t.Fatalf("AspectRatio = %q, want 16:9", request.AspectRatio)
	}
	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := marshalProtocolBody(t, spec.Body)
	generationConfig, _ := body["generationConfig"].(map[string]any)
	imageConfig, _ := generationConfig["imageConfig"].(map[string]any)
	if imageConfig["aspectRatio"] != "16:9" {
		t.Fatalf("gemini aspectRatio = %#v, want 16:9", imageConfig["aspectRatio"])
	}
}

func TestProtocolRequestKeepsGrokImageAspectRatio(t *testing.T) {
	profile := DefaultImageCapabilityConfig("grok-image", "grok-imagine-image")
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "a landscape",
		Config:          providerConfig{Model: "grok-imagine-image", InterfaceType: "grok-image", Size: "16:9", Quality: "2k"},
		ImageCapability: profile,
	})
	if request.AspectRatio != "16:9" {
		t.Fatalf("AspectRatio = %q, want 16:9", request.AspectRatio)
	}
}

func TestProtocolRequestLeavesVideoAspectRatioUnconverted(t *testing.T) {
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:   "video",
		Prompt: "a clip",
		Config: providerConfig{Model: "grok-imagine-video-1.5", InterfaceType: "xai-video", Size: "16:9", VideoSeconds: "6"},
	})
	if request.AspectRatio != "16:9" {
		t.Fatalf("video AspectRatio = %q, want 16:9", request.AspectRatio)
	}
}

func TestProtocolOpenAIImagesPluginPayloadUsesPixelSize(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "openai-images", "openai-image")
	profile := DefaultImageCapabilityConfig("openai-image", "gpt-image-2")
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "a landscape",
		Config:          providerConfig{Model: "gpt-image-2", InterfaceType: "openai-image", Size: "16:9", Quality: "2k"},
		ImageCapability: profile,
	})
	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := marshalProtocolBody(t, spec.Body)
	if body["size"] != "1824x1024" {
		t.Fatalf("openai-images size = %#v, want 1824x1024", body["size"])
	}
	if body["quality"] != "medium" {
		t.Fatalf("openai-images quality = %#v, want medium", body["quality"])
	}
	if spec.Path != "/v1/images/generations" {
		t.Fatalf("path = %q", spec.Path)
	}
}

func TestProtocolGrokImagesPluginPayloadKeepsAspectRatio(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "xai-grok-images", "grok-image")
	profile := DefaultImageCapabilityConfig("grok-image", "grok-imagine-image")
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "a landscape",
		Config:          providerConfig{Model: "grok-imagine-image", InterfaceType: "grok-image", Size: "16:9", Quality: "2k"},
		ImageCapability: profile,
	})
	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: request})
	if err != nil {
		t.Fatal(err)
	}
	body := marshalProtocolBody(t, spec.Body)
	if body["aspect_ratio"] != "16:9" {
		t.Fatalf("grok-image aspect_ratio = %#v, want 16:9", body["aspect_ratio"])
	}
	if _, exists := body["size"]; exists {
		t.Fatalf("grok-image must omit size: %#v", body)
	}
	if body["resolution"] != "2k" {
		t.Fatalf("grok-image resolution = %#v, want 2k", body["resolution"])
	}
}

// Replicate 的 OpenAI 图片族（openai/gpt-image-*）输入键与通用图片模型不同：
// 质量直通 input.quality、参考图走 input_images、审核档位缺省 low（最便宜的试跑形态）。
// 这三个键按 model owner 收口，绝不能下发给 flux、seedream 等不认这些字段的模型。
func TestProtocolReplicateGPTImageMapsQualityModerationAndReferences(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "replicate-prediction-image", "replicate-prediction-image")
	profile := DefaultImageCapabilityConfig("replicate-prediction-image", "openai/gpt-image-2")
	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "an astronaut corgi",
		Config:          providerConfig{Model: "openai/gpt-image-2", InterfaceType: "replicate-prediction-image", Size: "16:9", Quality: "low"},
		ImageCapability: profile,
		ReferenceImages: []providerMedia{{URL: "https://example.com/reference.png", MimeType: "image/png"}},
	})})
	if err != nil {
		t.Fatal(err)
	}
	input, ok := marshalProtocolBody(t, spec.Body)["input"].(map[string]any)
	if !ok {
		t.Fatalf("gpt-image input = %#v, want object", marshalProtocolBody(t, spec.Body)["input"])
	}
	if input["quality"] != "low" || input["moderation"] != "low" || input["aspect_ratio"] != "16:9" {
		t.Fatalf("gpt-image input = %#v, want quality/moderation low and aspect_ratio 16:9", input)
	}
	references, ok := input["input_images"].([]any)
	if !ok || len(references) != 1 || references[0] != "https://example.com/reference.png" {
		t.Fatalf("gpt-image input_images = %#v, want the single reference url", input["input_images"])
	}

	fluxSpec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:            "image",
		Prompt:          "an astronaut corgi",
		Config:          providerConfig{Model: "black-forest-labs/flux-schnell", InterfaceType: "replicate-prediction-image", Size: "16:9", Quality: "1k"},
		ImageCapability: DefaultImageCapabilityConfig("replicate-prediction-image", "black-forest-labs/flux-schnell"),
	})})
	if err != nil {
		t.Fatal(err)
	}
	fluxInput, ok := marshalProtocolBody(t, fluxSpec.Body)["input"].(map[string]any)
	if !ok {
		t.Fatalf("flux input = %#v, want object", marshalProtocolBody(t, fluxSpec.Body)["input"])
	}
	for _, key := range []string{"quality", "moderation", "input_images"} {
		if _, exists := fluxInput[key]; exists {
			t.Fatalf("flux input must not carry OpenAI-only key %q: %#v", key, fluxInput)
		}
	}
}

func officialSourceProviderAdapter(t *testing.T, pluginID, providerID string) protocol.Adapter {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "plugin-packages", pluginID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := protocol.LoadInstalledProviders(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, adapter := range adapters {
		if adapter.Metadata().ID == providerID {
			return adapter
		}
	}
	t.Fatalf("provider %s is missing from %s", providerID, pluginID)
	return nil
}

func marshalProtocolBody(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	return body
}
