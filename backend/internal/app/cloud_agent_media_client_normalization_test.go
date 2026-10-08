package app

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

// Hosted canvas clients normalize every node they load before autosaving the
// document: node-level createdAt/updatedAt are stamped from the canvas, and
// media nodes get derived nodeRole/resultOrigin labels. The Agent writes its
// draft node without them, so the approval recorded at that moment must ignore
// presentation-only normalization instead of demanding a re-approval the user
// has no way to trigger.
func TestCloudAgentImageApprovalSurvivesClientNodeNormalization(t *testing.T) {
	s, db, a := agentMediaFixture(t)
	seedCloudAgentImageChannel(t, db)
	a.Mode, a.ChannelModelKey, a.Duration, a.VideoGenerateAudio = "image", "grok-image", 0, nil
	a.Size, a.Quality, a.NodeID, a.Title = "1:1", "2k", "image-shot-1", "画面"
	a.ReferenceNodeIDs = []string{"cat"}
	run, _ := agentMediaRun(t, s, a, "request_approval")
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CloudAgentRun("user", run.ID)
	if err != nil || waiting.Approval == nil {
		t.Fatalf("missing approval: %v", err)
	}
	normalizeCanvasLikeHostedClient(t, s, db, "agent-canvas")
	settings := &CloudAgentMediaSettings{ChannelID: "channel", ChannelModelKey: "grok-image", Size: "1:1", Quality: "2k"}
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", "", settings); err != nil {
		t.Fatalf("client node normalization invalidated the approval: %v", err)
	}
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	state, _ := cloudAgentDecode(run)
	if state.MediaTaskID == "" {
		t.Fatal("approved image task was not submitted")
	}
}

// The same tolerance must not swallow edits generation actually depends on.
func TestCloudAgentMediaApprovalStillRejectsReferenceSwap(t *testing.T) {
	s, db, a := agentMediaFixture(t)
	seedCloudAgentImageChannel(t, db)
	a.Mode, a.ChannelModelKey, a.Duration, a.VideoGenerateAudio = "image", "grok-image", 0, nil
	a.Size, a.Quality, a.NodeID, a.Title = "1:1", "2k", "image-shot-1", "画面"
	a.ReferenceNodeIDs = []string{"cat"}
	run, _ := agentMediaRun(t, s, a, "request_approval")
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CloudAgentRun("user", run.ID)
	if err != nil || waiting.Approval == nil {
		t.Fatalf("missing approval: %v", err)
	}
	normalizeCanvasLikeHostedClient(t, s, db, "agent-canvas")
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := creationObjects(doc["nodes"])
	if err != nil {
		t.Fatal(err)
	}
	nodes["cat"]["metadata"].(map[string]any)["storageKey"] = "resource:someone-else"
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	settings := &CloudAgentMediaSettings{ChannelID: "channel", ChannelModelKey: "grok-image", Size: "1:1", Quality: "2k"}
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", "", settings); err == nil {
		t.Fatal("swapping the approved reference asset must invalidate the approval")
	}
}

func seedCloudAgentImageChannel(t *testing.T, db *gorm.DB) {
	t.Helper()
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceGrokImage), "grok-image")
	for _, row := range []any{
		&model.ChannelModel{ID: "image-cm", ChannelID: "channel", ModelKey: "grok-image", Capability: "image", Protocol: model.ChannelInterfaceGrokImage, CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, capability), Enabled: true},
		&model.ChannelModelVariant{ID: "image-tier", ChannelModelID: "image-cm", SelectorKey: "{}", SelectorJSON: "{}", Enabled: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func normalizeCanvasLikeHostedClient(t *testing.T, s *Service, db *gorm.DB, canvasID string) {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", canvasID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	delete(doc, "revision")
	delete(doc, "remoteContentHash")
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, node := range creationMaps(doc["nodes"]) {
		node["createdAt"], node["updatedAt"] = stamp, stamp
		if !isMediaCanvasNodeType(stringValue(node["type"])) {
			continue
		}
		meta, _ := node["metadata"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
			node["metadata"] = meta
		}
		if stringValue(meta["content"]) != "" || stringValue(meta["storageKey"]) != "" {
			meta["nodeRole"], meta["resultOrigin"] = "result", "unknown"
			continue
		}
		meta["nodeRole"], meta["resultOrigin"] = "generator", "unknown"
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", canvasID).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
}

func isMediaCanvasNodeType(nodeType string) bool {
	return nodeType == "image" || nodeType == "video" || nodeType == "audio"
}
