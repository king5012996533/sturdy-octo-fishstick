package canvas

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

func adoptedResource(id string) model.Resource {
	now := time.Now().UTC()
	return model.Resource{
		ID: id, UserID: "owner", Kind: "image", Status: model.ResourceStatusReady,
		Provider: "local", ObjectKey: "users/owner/image/" + id + ".png", MimeType: "image/png",
		Size: 4096, Width: 1536, Height: 1024, Source: "generation", CreatedAt: now, UpdatedAt: now,
	}
}

func mediaNodeCanvas(id string, resourceID string) json.RawMessage {
	return mediaNodeCanvasAtRevision(id, resourceID, 0)
}

func mediaNodeCanvasAtRevision(id string, resourceID string, revision int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"id":"%s","revision":%d,"title":"测试画布",
		"nodes":[{"id":"node","type":"image","title":"AI 平台背景图","metadata":{"status":"success","storageKey":"resource:%s","content":"/api/resources/%s/file"}}],
		"connections":[]
	}`, id, revision, resourceID, resourceID))
}

func nodeAssetID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var document struct {
		Nodes []struct {
			Metadata struct {
				AssetID string `json:"assetId"`
			} `json:"metadata"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Nodes) == 0 {
		t.Fatal("画布没有节点")
	}
	return document.Nodes[0].Metadata.AssetID
}

// Agent 回写、历史数据都可能留下"指向本人资源但没有素材记录"的节点。
// 这类画布以前整份保存都会被拒（400），用户看到的是画布存不上、素材也用不了。
func TestUpsertCanvasAdoptsMissingMediaAsset(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	resource := adoptedResource("resource-adopted")
	if err := svc.repo.CreateResource(&resource); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertUserCanvasProject("owner", mediaNodeCanvas("canvas-adopt", resource.ID)); err != nil {
		t.Fatalf("保存含未登记素材的画布失败: %v", err)
	}
	assetID := MediaAssetIDForResource("owner", resource.ID)
	payload, err := svc.UserAsset("owner", assetID)
	if err != nil {
		t.Fatalf("补建的素材不存在: %v", err)
	}
	if !strings.Contains(string(payload), "resource:"+resource.ID) || !strings.Contains(string(payload), "AI 平台背景图") {
		t.Fatalf("补建素材没有指向原资源或缺少标题: %s", payload)
	}
	persisted, err := svc.UserCanvasProject("owner", "canvas-adopt")
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeAssetID(t, persisted); got != assetID {
		t.Fatalf("节点绑定 = %q, want %q", got, assetID)
	}
}

func TestUpsertCanvasAdoptionIsIdempotent(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	resource := adoptedResource("resource-repeat")
	if err := svc.repo.CreateResource(&resource); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertUserCanvasProject("owner", mediaNodeCanvas("canvas-repeat", resource.ID)); err != nil {
		t.Fatal(err)
	}
	// 第二次保存提交的是同一个文档（客户端本地那份还没有 assetId）：引用已经被绑定，
	// 服务端应该继续接受，而不是再补建一条素材。
	if _, err := svc.UpsertUserCanvasProject("owner", mediaNodeCanvasAtRevision("canvas-repeat", resource.ID, 1)); err != nil {
		t.Fatal(err)
	}
	assets, err := svc.repo.Assets("owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Fatalf("素材数量 = %d, want 1", len(assets))
	}
}

// 不能确定的引用（别人的资源、还没就绪）保持原样报错，不做猜测。
func TestUpsertCanvasKeepsRejectingUnresolvableMedia(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	notReady := adoptedResource("resource-not-ready")
	notReady.Status = model.ResourceStatusPending
	if err := svc.repo.CreateResource(&notReady); err != nil {
		t.Fatal(err)
	}
	otherOwner := adoptedResource("resource-other-owner")
	otherOwner.UserID = "someone-else"
	if err := svc.repo.CreateResource(&otherOwner); err != nil {
		t.Fatal(err)
	}
	for _, resourceID := range []string{"resource-missing", notReady.ID, otherOwner.ID} {
		_, err := svc.UpsertUserCanvasProject("owner", mediaNodeCanvas("canvas-"+resourceID, resourceID))
		if err == nil {
			t.Fatalf("%s 不该被接受", resourceID)
		}
	}
	assets, err := svc.repo.Assets("owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 0 {
		t.Fatalf("不该补建素材，实际 %d 条", len(assets))
	}
}

// 时间轴上的直接引用同样要补建并绑定，否则画布仍然存不进去。
func TestUpsertCanvasAdoptsTimelineMediaAsset(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	resource := adoptedResource("resource-timeline")
	if err := svc.repo.CreateResource(&resource); err != nil {
		t.Fatal(err)
	}
	project := json.RawMessage(`{
		"id":"canvas-timeline","revision":0,"title":"时间轴画布","nodes":[],
		"timeline":{"clips":[{"id":"clip","directMedia":{"kind":"image","storageKey":"resource:` + resource.ID + `","url":"/api/resources/` + resource.ID + `/file"}}]},
		"connections":[]
	}`)
	if _, err := svc.UpsertUserCanvasProject("owner", project); err != nil {
		t.Fatalf("保存含时间轴引用的画布失败: %v", err)
	}
	persisted, err := svc.UserCanvasProject("owner", "canvas-timeline")
	if err != nil {
		t.Fatal(err)
	}
	assetID := MediaAssetIDForResource("owner", resource.ID)
	if !strings.Contains(string(persisted), `"assetId":"`+assetID+`"`) {
		t.Fatalf("时间轴剪辑没有绑定素材: %s", persisted)
	}
}

// 已登记的素材不能因为补建逻辑被替换掉：绑定的仍然应该是客户端那条。
func TestUpsertCanvasKeepsClientBoundAsset(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	resource := adoptedResource("resource-bound")
	if err := svc.repo.CreateResource(&resource); err != nil {
		t.Fatal(err)
	}
	payload, err := CanvasMediaAssetDocument("generation_client_bound", "客户端素材", "image", resource)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := AssetFromJSON("owner", payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.UpsertAsset(&asset); err != nil {
		t.Fatal(err)
	}
	project := json.RawMessage(`{
		"id":"canvas-bound","revision":0,"title":"测试画布",
		"nodes":[{"id":"node","type":"image","title":"AI 平台背景图","metadata":{"status":"success","assetId":"generation_client_bound","storageKey":"resource:` + resource.ID + `","content":"/api/resources/` + resource.ID + `/file"}}],
		"connections":[]
	}`)
	if _, err := svc.UpsertUserCanvasProject("owner", project); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	persisted, err := svc.UserCanvasProject("owner", "canvas-bound")
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeAssetID(t, persisted); got != "generation_client_bound" {
		t.Fatalf("节点绑定被改写为 %q", got)
	}
	assets, err := svc.repo.Assets("owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Fatalf("不该补建重复素材，实际 %d 条", len(assets))
	}
}
