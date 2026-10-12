package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// 调用方按语义把数量、时长、开关写成 JSON 数字/布尔是常态，不能因此让整条任务以
// Go 内部错误原文失败（线上真实反馈）。
func TestProviderConfigUnmarshalAcceptsNumbersAndBooleans(t *testing.T) {
	raw := `{"config":{"videoSeconds":15,"count":4,"vquality":720,"videoGenerateAudio":true,"videoWatermark":false,"audioSpeed":1.25,"size":"16:9"}}`
	var input canvasGenerationInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	config := input.Config
	for _, tt := range []struct{ name, got, want string }{
		{"videoSeconds", config.VideoSeconds, "15"},
		{"count", config.Count, "4"},
		{"vquality", config.VQuality, "720"},
		{"videoGenerateAudio", config.VideoGenerateAudio, "true"},
		{"videoWatermark", config.VideoWatermark, "false"},
		{"audioSpeed", config.AudioSpeed, "1.25"},
		{"size", config.Size, "16:9"},
	} {
		if tt.got != tt.want {
			t.Fatalf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestProviderConfigUnmarshalKeepsTextAndNull(t *testing.T) {
	raw := `{"config":{"videoSeconds":"12","count":"","vquality":null,"baseUrl":"https://relay.example"}}`
	var input canvasGenerationInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	if input.Config.VideoSeconds != "12" || input.Config.Count != "" || input.Config.VQuality != "" {
		t.Fatalf("config = %#v", input.Config)
	}
	if input.Config.BaseURL != "https://relay.example" {
		t.Fatalf("baseUrl = %q", input.Config.BaseURL)
	}
}

// 对象/数组不是标量，仍然要当成类型错误拒绝——只是拒绝时说的话得是人话。
func TestTaskInputDecodeErrorRewritesGoInternals(t *testing.T) {
	var input canvasGenerationInput
	err := json.Unmarshal([]byte(`{"config":{"videoSeconds":{"nested":1}}}`), &input)
	if err == nil {
		t.Fatal("expected a decode error")
	}
	message := taskInputDecodeError(err).Error()
	if !strings.Contains(message, "config.videoSeconds") {
		t.Fatalf("message = %q, want 参数名留在里面", message)
	}
	for _, leaked := range []string{"cannot unmarshal", "of type string", "Go struct field"} {
		if strings.Contains(message, leaked) {
			t.Fatalf("message = %q, 不应出现 Go 内部细节 %q", message, leaked)
		}
	}
}

func TestTaskInputDecodeErrorFallsBackForNonTypeErrors(t *testing.T) {
	message := taskInputDecodeError(errors.New("unexpected end of JSON input")).Error()
	if strings.Contains(message, "unexpected end of JSON input") {
		t.Fatalf("message = %q, 不应回显底层错误原文", message)
	}
	if !strings.Contains(message, "格式不正确") {
		t.Fatalf("message = %q", message)
	}
}
