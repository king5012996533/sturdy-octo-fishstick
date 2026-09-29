package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/model"
)

// 这条链路是「Agent 能看图」的全部关键：工具登记 → 消息挂载 → 资源水合 → 上游请求体。
// 任何一环断开，模型收到的仍然只是文字，Agent 就会退回聊天机器人。
func TestCloudAgentInspectionImagesReachUpstreamRequest(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	s, db := agentVisionFixture(t, 4)

	// 真实本地资源字节，走完整水合链路；不用内嵌假数据绕过存储。
	imageBytes := []byte("\x89PNG\r\n\x1a\nkino-vision-fixture")
	if err := asset.NewFileStore(s.dataDir).Write("users/user/image/ref-one.png", bytes.NewReader(imageBytes)); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Resource{}).Where("id = ?", "ref-one").Updates(map[string]any{
		"provider": "local", "object_key": "users/user/image/ref-one.png", "size": len(imageBytes),
	}).Error; err != nil {
		t.Fatal(err)
	}

	bodies := make(chan map[string]any, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"我看到画布上是一只白色的猫。","tool_calls":[]}}]}`))
	}))
	defer upstream.Close()

	// 1) 模型调用看图工具，与真实 Agent 走同一个入口。
	state := &cloudAgentRuntime{Request: agentTestRequest()}
	state.Canonical.Messages = []map[string]any{{"role": "user", "content": "画布上那张图里是什么"}}
	state.Canonical.Tools = cloudAgentTools(state.Request)
	state.Canonical.ToolChoice = "auto"
	if _, err := cloudAgentInspectImages(s.repo, "user", state, agentVisionCall(t, `{"nodeIds":["cat"]}`)); err != nil {
		t.Fatalf("inspect failed: %v", err)
	}

	// 2) 下一步请求：挂载图片、水合资源、展开上游协议。
	canonical := cloudAgentCanonicalWithPlan(state)
	media := cloudAgentAttachInspectionImages(state, &canonical)
	if len(media) != 1 {
		t.Fatalf("expected one queued image, got %d", len(media))
	}
	input := canvasGenerationInput{
		Mode:            "text",
		Config:          providerConfig{BaseURL: upstream.URL, APIKey: "key", Model: "vision-model", InterfaceType: string(model.ChannelInterfaceChatCompletion)},
		AgentRequests:   &agentToolRequests{Canonical: &canonical},
		ReferenceImages: media,
	}
	if err := s.hydrateGenerationMedia("user", &input, providerMediaHydrationPolicyFor(context.Background(), input)); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if !strings.HasPrefix(input.ReferenceImages[0].DataURL, "data:image/png;base64,") {
		t.Fatalf("local resource was not hydrated into inline data: %q", input.ReferenceImages[0].DataURL)
	}
	resolved, err := resolveAgentResourcePlaceholders(input, true)
	if err != nil {
		t.Fatalf("resolve placeholders: %v", err)
	}
	if _, err := runAgentToolTask(context.Background(), resolved); err != nil {
		t.Fatalf("runAgentToolTask: %v", err)
	}

	body := <-bodies
	messages, _ := body["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("upstream received no messages: %#v", body)
	}
	last, _ := messages[len(messages)-1].(map[string]any)
	if last["role"] != "user" {
		t.Fatalf("inspection must arrive as user content, got %#v", last)
	}
	parts, _ := last["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected text plus one image part, got %#v", last["content"])
	}
	part, _ := parts[1].(map[string]any)
	image, _ := part["image_url"].(map[string]any)
	url, _ := image["url"].(string)
	if part["type"] != "image_url" || !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("upstream did not receive real image bytes: %#v", part)
	}
	if strings.Contains(strings.Join(messagesText(messages), "\n"), "resource:") {
		t.Fatal("an unresolved resource placeholder reached the upstream")
	}
	// 工具必须在本轮请求里可见，否则模型无从知道可以看图。
	tools, _ := body["tools"].([]any)
	names := map[string]bool{}
	for _, item := range tools {
		function, _ := item.(map[string]any)["function"].(map[string]any)
		names[stringField(function, "name")] = true
	}
	if !names["canvas_inspect_images"] || !names["canvas_get_state"] {
		t.Fatalf("inspection tool was not advertised upstream: %#v", names)
	}
	// 图片只发一次：持久化会话不能被污染，否则每一步都会重复计费。
	for _, message := range state.Canonical.Messages {
		if _, ok := message["content"].([]any); ok {
			t.Fatal("inspection images leaked into the persisted conversation")
		}
	}
}

func messagesText(messages []any) []string {
	texts := make([]string, 0, len(messages))
	for _, item := range messages {
		message, _ := item.(map[string]any)
		texts = append(texts, canonicalAgentText(message["content"]))
	}
	return texts
}
