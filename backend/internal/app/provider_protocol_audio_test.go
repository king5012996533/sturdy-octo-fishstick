package app

import (
	"context"
	"testing"

	"infinite-canvas/backend/internal/protocol"
)

// Replicate 音频是"一模型一 schema"：语音族只认 text/voice_id，音乐族只认 lyrics/prompt，
// 把另一族的键发过去上游直接 422。这组用例锁定按模型族收窄后的请求体形态。
func TestProtocolReplicateAudioRoutesFieldsByModelFamily(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "replicate-prediction-audio", "replicate-prediction-audio")

	speechSpec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:   "audio",
		Prompt: "今天天气不错",
		Config: providerConfig{
			Model: "minimax/speech-2.8-turbo", InterfaceType: "replicate-prediction-audio",
			AudioVoice: "Wise_Woman", AudioSpeed: "1.25", AudioFormat: "flac",
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if speechSpec.Path != "/v1/models/minimax/speech-2.8-turbo/predictions" {
		t.Fatalf("speech path = %q, want the model-scoped predictions endpoint", speechSpec.Path)
	}
	speechInput := replicateAudioInput(t, speechSpec.Body)
	if speechInput["text"] != "今天天气不错" || speechInput["voice_id"] != "Wise_Woman" {
		t.Fatalf("speech input = %#v, want text + voice_id from the unified request", speechInput)
	}
	if speechInput["speed"] != 1.25 || speechInput["audio_format"] != "flac" {
		t.Fatalf("speech input = %#v, want speed 1.25 and audio_format flac", speechInput)
	}
	if speechInput["language_boost"] != "Automatic" {
		t.Fatalf("speech language_boost = %#v, want Automatic so 中文 text is not misread", speechInput["language_boost"])
	}
	for _, key := range []string{"lyrics", "prompt"} {
		if _, exists := speechInput[key]; exists {
			t.Fatalf("speech input must not carry music-only key %q: %#v", key, speechInput)
		}
	}

	musicSpec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:   "audio",
		Prompt: "[Verse]\n海边的风",
		Config: providerConfig{
			Model: "minimax/music-2.5", InterfaceType: "replicate-prediction-audio",
			AudioFormat: "wav", AudioInstructions: "独立民谣，忧郁，慢速，木吉他",
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	musicInput := replicateAudioInput(t, musicSpec.Body)
	if musicInput["lyrics"] != "[Verse]\n海边的风" || musicInput["prompt"] != "独立民谣，忧郁，慢速，木吉他" {
		t.Fatalf("music input = %#v, want lyrics from prompt and style from audioInstructions", musicInput)
	}
	if musicInput["audio_format"] != "wav" {
		t.Fatalf("music audio_format = %#v, want wav", musicInput["audio_format"])
	}
	for _, key := range []string{"text", "voice_id", "speed", "language_boost"} {
		if _, exists := musicInput[key]; exists {
			t.Fatalf("music input must not carry speech-only key %q: %#v", key, musicInput)
		}
	}
}

// 前台配置里可能留着别的模型的值：MiniMax 语音只接受 0.5–2.0 倍语速，音乐族没有 flac 输出。
// 超出枚举的取值必须退回上游默认，而不是原样下发被拒。
func TestProtocolReplicateAudioDropsOutOfRangeSpeedAndFormat(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "replicate-prediction-audio", "replicate-prediction-audio")

	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:   "audio",
		Prompt: "hello",
		Config: providerConfig{
			Model: "minimax/speech-2.8-turbo", InterfaceType: "replicate-prediction-audio",
			AudioVoice: "alloy", AudioSpeed: "4", AudioFormat: "aac",
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	input := replicateAudioInput(t, spec.Body)
	for _, key := range []string{"speed", "audio_format"} {
		if _, exists := input[key]; exists {
			t.Fatalf("out-of-range %q must be omitted: %#v", key, input)
		}
	}
	// 前台传来的 OpenAI 音色名不是 MiniMax 音色，未指定时交给上游默认音色。
	if _, exists := input["voice_id"]; exists {
		t.Fatalf("openai voice name must not be forwarded as a minimax voice_id: %#v", input)
	}

	musicSpec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:   "audio",
		Prompt: "[Verse]\nline",
		Config: providerConfig{Model: "minimax/music-2.5", InterfaceType: "replicate-prediction-audio", AudioFormat: "flac"},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := replicateAudioInput(t, musicSpec.Body)["audio_format"]; exists {
		t.Fatalf("music does not accept flac, want the key omitted: %#v", replicateAudioInput(t, musicSpec.Body))
	}
}

// BeefAPI 与 Replicate 的 MiniMax 音色 id 不通用：同一份前台配置（OpenAI 音色名）在
// 两条线上必须落到不同的默认值，不能把 alloy 或中文音色名发给 Replicate。
func TestProtocolRequestResolvesMiniMaxVoicePerProtocol(t *testing.T) {
	replicate := protocolRequestFromInput(canvasGenerationInput{
		Mode: "audio",
		Config: providerConfig{
			Model: "minimax/speech-2.8-turbo", InterfaceType: "replicate-prediction-audio",
			AudioVoice: "alloy", AudioFormat: "mp3", AudioSpeed: "1",
		},
	})
	if replicate.Extra["audioVoice"] != "" {
		t.Fatalf("replicate audioVoice = %#v, want empty so the plugin uses the upstream default", replicate.Extra["audioVoice"])
	}

	replicateKept := protocolRequestFromInput(canvasGenerationInput{
		Mode: "audio",
		Config: providerConfig{
			Model: "minimax/speech-2.8-turbo", InterfaceType: "replicate-prediction-audio",
			AudioVoice: "Wise_Woman", AudioFormat: "mp3", AudioSpeed: "1",
		},
	})
	if replicateKept.Extra["audioVoice"] != "Wise_Woman" {
		t.Fatalf("replicate custom voice = %#v, want Wise_Woman untouched", replicateKept.Extra["audioVoice"])
	}
}

func TestProtocolReplicateAudioOptionsOverrideWins(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "replicate-prediction-audio", "replicate-prediction-audio")
	request := protocolRequestFromInput(canvasGenerationInput{
		Mode:   "audio",
		Prompt: "ignored",
		Config: providerConfig{Model: "minimax/speech-2.8-turbo", InterfaceType: "replicate-prediction-audio"},
	})
	request.ProviderOptions = map[string]map[string]any{
		"replicate-prediction-audio": {"input": map[string]any{"text": "raw", "emotion": "happy"}},
	}
	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: request})
	if err != nil {
		t.Fatal(err)
	}
	input := replicateAudioInput(t, spec.Body)
	// 厂商扩展键必须是"整块替换"：插件文档声明的逃生口要能把统一字段全部接管，
	// 否则调用方无法表达插件没列举的上游参数。
	if input["text"] != "raw" || input["emotion"] != "happy" || len(input) != 2 {
		t.Fatalf("providerOptions.input must replace the mapped body: %#v", input)
	}
}

func replicateAudioInput(t *testing.T, body any) map[string]any {
	t.Helper()
	input, ok := marshalProtocolBody(t, body)["input"].(map[string]any)
	if !ok {
		t.Fatalf("input = %#v, want object", marshalProtocolBody(t, body)["input"])
	}
	return input
}
