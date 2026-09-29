package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

// agentVisionFixture 建一个带图片节点的画布，并按需要声明当前文本模型的图片理解张数。
func agentVisionFixture(t *testing.T, maxImages int) (*Service, *gorm.DB) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	if capability.Text == nil {
		capability.Text = &TextCapabilityConfig{}
	}
	capability.Text.References.MaxImages = maxImages
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, capability)).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.Resource{ID: "ref-one", UserID: "user", Kind: "image", Status: "ready", MimeType: "image/png", Width: 640, Height: 480, Size: 50},
		&model.Resource{ID: "pending", UserID: "user", Kind: "image", Status: "uploading", MimeType: "image/png"},
		&model.Resource{ID: "other-image", UserID: "other", Kind: "image", Status: "ready", MimeType: "image/png"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	doc := map[string]any{"nodes": []map[string]any{
		{"id": "note", "type": "text", "title": "剧本", "position": map[string]any{"x": 10, "y": 10}, "width": 300, "metadata": map[string]any{"content": "第一场"}},
		{"id": "cat", "type": "image", "title": "叮当猫在飞", "position": map[string]any{"x": 10, "y": 300}, "width": 300, "metadata": map[string]any{"status": "success", "storageKey": "resource:ref-one"}},
		{"id": "pending-image", "type": "image", "title": "尚未上传完", "position": map[string]any{"x": 400, "y": 300}, "width": 300, "metadata": map[string]any{"status": "success", "storageKey": "resource:pending"}},
		{"id": "stolen", "type": "image", "title": "别人的图", "position": map[string]any{"x": 700, "y": 300}, "width": 300, "metadata": map[string]any{"status": "success", "storageKey": "resource:other-image"}},
		{"id": "remote", "type": "image", "title": "外链图", "position": map[string]any{"x": 1000, "y": 300}, "width": 300, "metadata": map[string]any{"status": "success", "content": "https://example.invalid/a.png"}},
	}, "connections": []any{}}
	raw, _ := json.Marshal(doc)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	return s, db
}

func agentVisionCall(t *testing.T, arguments string) cloudAgentCall {
	t.Helper()
	call := cloudAgentCall{ID: "vision-call"}
	call.Function.Name, call.Function.Arguments = "canvas_inspect_images", arguments
	return call
}

func TestCloudAgentInspectImagesRejectsMalformedArgumentsBeforeReading(t *testing.T) {
	for _, tt := range []struct {
		name, arguments, message string
	}{
		{"unknown field", `{"limit":2}`, "不支持的字段"},
		{"missing nodeIds", `{}`, "1 到 4"},
		{"string nodeIds", `{"nodeIds":"cat"}`, "字段类型不匹配"},
		{"numeric nodeId", `{"nodeIds":[1]}`, "字段类型不匹配"},
		{"empty list", `{"nodeIds":[]}`, "1 到 4"},
		{"too many nodes", `{"nodeIds":["a","b","c","d","e"]}`, "1 到 4"},
		{"empty", ``, "单个 JSON 对象"},
		{"null", `null`, "单个 JSON 对象"},
		{"array", `[]`, "单个 JSON 对象"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// nil repository proves malformed arguments never reach the canvas read.
			_, err := cloudAgentReadTool(nil, "user", &cloudAgentRuntime{Request: agentTestRequest()}, agentVisionCall(t, tt.arguments))
			var argumentErr *cloudAgentArgumentError
			if !errors.As(err, &argumentErr) || !strings.Contains(cloudAgentSafeToolError(err), tt.message) {
				t.Fatalf("expected correctable %q error, got %v", tt.message, err)
			}
		})
	}
}

func TestCloudAgentInspectImagesRequiresVisionCapableModel(t *testing.T) {
	for _, tt := range []struct {
		name      string
		maxImages int
		nodes     string
	}{
		{"text-only model", 0, `{"nodeIds":["cat"]}`},
		{"more images than the model accepts", 1, `{"nodeIds":["cat","cat"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := agentVisionFixture(t, tt.maxImages)
			// 能力不足必须挡住，不能把图片发给上游；这与真实模型是否支持视觉无关，
			// 所以即使节点本身合法也要在读图前失败。
			state := &cloudAgentRuntime{Request: agentTestRequest()}
			if _, err := cloudAgentInspectImages(s.repo, "user", state, agentVisionCall(t, tt.nodes)); err == nil {
				t.Fatal("vision-incapable model was allowed to inspect images")
			}
			if len(state.InspectImages) != 0 {
				t.Fatal("rejected inspection still queued images")
			}
		})
	}
}

func TestCloudAgentInspectImagesRejectsNodesItCannotRead(t *testing.T) {
	for _, tt := range []struct {
		name, nodeID, message string
	}{
		{"missing node", "ghost", "不在当前画布"},
		{"text node", "note", "不能作为图片查看"},
		{"not ready", "pending-image", "不能作为图片查看"},
		{"other user's asset", "stolen", "不能作为图片查看"},
		{"external url", "remote", "不能作为图片查看"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := agentVisionFixture(t, 4)
			state := &cloudAgentRuntime{Request: agentTestRequest()}
			raw, _ := json.Marshal(map[string]any{"nodeIds": []string{tt.nodeID}})
			_, err := cloudAgentInspectImages(s.repo, "user", state, agentVisionCall(t, string(raw)))
			if err == nil || !strings.Contains(cloudAgentSafeToolError(err), tt.message) {
				t.Fatalf("expected %q rejection, got %v", tt.message, err)
			}
			if len(state.InspectImages) != 0 {
				t.Fatal("rejected node still queued images")
			}
		})
	}
}

func TestCloudAgentInspectImagesQueuesOnlyRealImages(t *testing.T) {
	s, _ := agentVisionFixture(t, 4)
	state := &cloudAgentRuntime{Request: agentTestRequest()}
	result, err := cloudAgentInspectImages(s.repo, "user", state, agentVisionCall(t, `{"nodeIds":["cat","cat"]}`))
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if len(state.InspectImages) != 1 {
		t.Fatalf("duplicate node was queued twice: %+v", state.InspectImages)
	}
	image := state.InspectImages[0]
	if image.NodeID != "cat" || image.StorageKey != "resource:ref-one" || image.MIMEType != "image/png" || image.Bytes != 50 || image.Width != 640 {
		t.Fatalf("queued image lost its resource identity: %+v", image)
	}
	// 工具结果只能是文本，图片必须留给下一步挂载；返回值里不能出现像素或存储位置之外的引用。
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "ref-one") || strings.Contains(string(raw), "storageKey") {
		t.Fatalf("tool result leaked the storage locator: %s", raw)
	}
}

func TestCloudAgentAttachInspectionImagesKeepsSessionClean(t *testing.T) {
	state := &cloudAgentRuntime{Request: agentTestRequest()}
	state.Canonical.Messages = []map[string]any{{"role": "user", "content": "看看这张图"}, {"role": "assistant", "content": "好"}}
	state.InspectImages = []cloudAgentVisionImage{
		{NodeID: "cat", StorageKey: "resource:ref-one", MIMEType: "image/png", Bytes: 50, Width: 640, Height: 480},
		{NodeID: "hero", StorageKey: "resource:ref-two", MIMEType: "image/jpeg", Bytes: 60, Width: 640, Height: 480},
	}
	canonical := cloudAgentCanonicalWithPlan(state)
	media := cloudAgentAttachInspectionImages(state, &canonical)
	if len(media) != 2 || media[0].StorageKey != "resource:ref-one" || media[1].StorageKey != "resource:ref-two" {
		t.Fatalf("inspection media was not queued for hydration: %+v", media)
	}
	if len(state.InspectImages) != 0 {
		t.Fatal("queued images survived the attach and would be billed again")
	}
	if len(canonical.Messages) != 3 {
		t.Fatalf("expected one extra multimodal message, got %d", len(canonical.Messages))
	}
	if len(state.Canonical.Messages) != 2 {
		t.Fatal("attaching images polluted the persisted conversation")
	}
	last := canonical.Messages[2]
	if last["role"] != "user" {
		t.Fatalf("inspection must arrive as user content, got role %v", last["role"])
	}
	parts, ok := last["content"].([]any)
	if !ok || len(parts) != 3 {
		t.Fatalf("expected text plus two image parts, got %#v", last["content"])
	}
	for index, nodeID := range []string{"ref-one", "ref-two"} {
		part, _ := parts[index+1].(map[string]any)
		if part["type"] != "image_url" {
			t.Fatalf("part %d is not an image_url: %#v", index, part)
		}
		image, _ := part["image_url"].(map[string]any)
		if image["url"] != "resource:"+nodeID {
			t.Fatalf("image must stay a resource placeholder, got %v", image["url"])
		}
	}
	// 重复挂载不得再产生第二条消息。
	if again := cloudAgentAttachInspectionImages(state, &canonical); again != nil || len(canonical.Messages) != 3 {
		t.Fatal("attach ran twice for the same inspection")
	}
}

func TestCloudAgentInspectImagesIsRegisteredAsReadTool(t *testing.T) {
	req := agentTestRequest()
	if cloudAgentWrite("canvas_inspect_images") {
		t.Fatal("inspection must not require canvas write approval")
	}
	if !cloudAgentToolAllowed(req, "canvas_inspect_images") {
		t.Fatal("inspection tool is missing from the advertised contract")
	}
	if !cloudAgentToolAllowed(req, "canvas_inspect_images") && cloudAgentToolAllowed(CloudAgentRequest{}, "canvas_inspect_images") {
		t.Fatal("inspection tool must follow canvas context scope")
	}
	for _, tool := range cloudAgentTools(req) {
		function, _ := tool["function"].(map[string]any)
		if function["name"] != "canvas_inspect_images" {
			continue
		}
		parameters, _ := function["parameters"].(map[string]any)
		if parameters["additionalProperties"] != false {
			t.Fatal("tool schema must reject unknown fields to match DisallowUnknownFields")
		}
		required, _ := parameters["required"].([]string)
		if len(required) != 1 || required[0] != "nodeIds" {
			t.Fatalf("unexpected required fields: %#v", parameters["required"])
		}
		return
	}
	t.Fatal("inspection tool was never advertised")
}
