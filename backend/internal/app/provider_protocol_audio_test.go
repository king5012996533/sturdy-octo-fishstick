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

// Replicate 只为官方模型提供模型作用域的创建入口，社区模型必须走 /v1/predictions 并在
// body 顶层带 version，否则上游返回 404。ACE-Step 同时是唯一能按秒指定时长的音乐模型。
func TestProtocolReplicateAudioSendsCommunityModelToVersionEndpoint(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "replicate-prediction-audio", "replicate-prediction-audio")

	spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:   "audio",
		Prompt: "",
		Config: providerConfig{
			Model: "lucataco/ace-step", InterfaceType: "replicate-prediction-audio",
			AudioInstructions: "cinematic, uplifting piano", AudioDuration: "30",
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "/v1/predictions" {
		t.Fatalf("ace path = %q, want the version endpoint", spec.Path)
	}
	body := marshalProtocolBody(t, spec.Body)
	if version, _ := body["version"].(string); version != "280fc4f9ee507577f880a167f639c02622421d8fecf492454320311217b688f1" {
		t.Fatalf("ace version = %#v, want the pinned ACE-Step version", body["version"])
	}
	input := replicateAudioInput(t, spec.Body)
	// 歌词留空时要下发 [instrumental]：ACE-Step 靠这个标记出纯器乐，空字符串会被当成"没写歌词"。
	if input["lyrics"] != "[instrumental]" || input["tags"] != "cinematic, uplifting piano" {
		t.Fatalf("ace input = %#v, want instrumental lyrics and the style tags", input)
	}
	if input["duration"] != float64(30) {
		t.Fatalf("ace duration = %#v, want the requested 30 seconds", input["duration"])
	}
	// ace 的输入 schema 没有 audio_format，也没有 text/voice_id，多发的键会被上游拒绝。
	for _, key := range []string{"audio_format", "text", "voice_id", "speed", "prompt"} {
		if _, exists := input[key]; exists {
			t.Fatalf("ace input must not carry %q: %#v", key, input)
		}
	}
}

// 官方模型不能带 version（上游会拒），音乐族也不能带 duration（ACE-Step 才认），
// 越界的时长必须在插件内收口，不能原样发给上游。
func TestProtocolReplicateAudioKeepsVersionAndDurationFamiliesApart(t *testing.T) {
	adapter := officialSourceProviderAdapter(t, "replicate-prediction-audio", "replicate-prediction-audio")

	musicSpec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:   "audio",
		Prompt: "[Verse]\n海边的风",
		Config: providerConfig{
			Model: "minimax/music-2.5", InterfaceType: "replicate-prediction-audio",
			AudioInstructions: "独立民谣", AudioDuration: "30",
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if musicSpec.Path != "/v1/models/minimax/music-2.5/predictions" {
		t.Fatalf("music path = %q, want the model-scoped endpoint", musicSpec.Path)
	}
	if _, exists := marshalProtocolBody(t, musicSpec.Body)["version"]; exists {
		t.Fatalf("official model body must not carry version: %#v", marshalProtocolBody(t, musicSpec.Body))
	}
	if input := replicateAudioInput(t, musicSpec.Body); input["duration"] != nil {
		t.Fatalf("music input must not carry duration: %#v", input)
	}

	overSpec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(canvasGenerationInput{
		Mode:   "audio",
		Prompt: "[instrumental]",
		Config: providerConfig{
			Model: "lucataco/ace-step", InterfaceType: "replicate-prediction-audio",
			AudioInstructions: "lofi", AudioDuration: "600",
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if input := replicateAudioInput(t, overSpec.Body); input["duration"] != nil {
		t.Fatalf("out-of-range duration must be dropped: %#v", input)
	}
}
