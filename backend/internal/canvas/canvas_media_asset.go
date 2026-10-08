package canvas

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
)

// 画布媒体节点必须通过本人素材记录引用资源（见 canvas_asset_guard.go）。
// 客户端生成是自己登记素材的，服务端生成（Agent 回写）和路径不一致的历史数据不会，
// 于是画布既能存不进去，也不能再被引用。这里提供两边共用的补建件：
// 确定性 ID 保证同一份资源只会得到同一条素材记录，重复保存不会堆出重复素材。

func MediaAssetIDForResource(userID string, resourceID string) string {
	sum := sha256.Sum256([]byte("canvas-media:" + strings.TrimSpace(userID) + ":" + strings.TrimSpace(resourceID)))
	return "generation_" + hex.EncodeToString(sum[:])
}

// CanvasMediaAssetDocument 生成与前端素材合同一致的素材文档。
// 字段形状对齐 web 端 canvasNodeToAsset 的结果，避免同一份资源在后台显示成两种素材。
func CanvasMediaAssetDocument(assetID string, title string, kind string, resource model.Resource) (json.RawMessage, error) {
	url := resourceURLFromStorageKey("resource:" + resource.ID)
	trimmedTitle := strings.TrimSpace(title)
	if trimmedTitle == "" {
		trimmedTitle = canvasMediaAssetDefaultTitle(kind)
	}
	data := map[string]any{
		"storageKey": "resource:" + resource.ID,
		"bytes":      resource.Size,
		"mimeType":   resource.MimeType,
	}
	if kind == "image" {
		data["dataUrl"] = url
	}
	if kind == "image" || kind == "video" {
		width, height := resource.Width, resource.Height
		if width <= 0 {
			width = 1
		}
		if height <= 0 {
			height = 1
		}
		data["width"], data["height"] = width, height
	}
	if kind != "image" {
		data["url"] = url
	}
	return json.Marshal(map[string]any{
		"id":       assetID,
		"title":    trimmedTitle,
		"coverUrl": url,
		"tags":     []string{"生成"},
		"kind":     kind,
		"status":   "confirmed",
		"category": "material",
		"source":   "生成任务",
		"metadata": map[string]any{"source": "canvas-media", "resourceId": resource.ID},
		"data":     data,
	})
}

func canvasMediaAssetDefaultTitle(kind string) string {
	switch kind {
	case "video":
		return "生成视频"
	case "audio":
		return "生成音频"
	default:
		return "生成图片"
	}
}

// adoptCanvasMediaAssets 为「指向本人已就绪资源、但缺少素材记录」的画布媒体引用补建素材。
// 只补能确定的：资源必须属于本人且已就绪、类型是画布媒体。其余保持原样交给校验报错，
// 不猜测、不代替用户修复。
func (s *Service) adoptCanvasMediaAssets(userID string, raw json.RawMessage, candidates []model.Asset) ([]model.Asset, error) {
	references, err := MediaAssetReferences(raw)
	if err != nil {
		return nil, err
	}
	if len(references) == 0 {
		return nil, nil
	}
	assetIDs := map[string]struct{}{}
	resourceIDs := map[string]struct{}{}
	for _, reference := range references {
		resourceIDs[reference.ResourceID] = struct{}{}
		if reference.AssetID != "" {
			assetIDs[reference.AssetID] = struct{}{}
		}
	}
	// 已满足的引用：素材属于本人，且这份素材记录的确实是同一个资源。
	assetResources := map[string]map[string]struct{}{}
	owned, err := s.repo.AssetsForUserIDs(userID, assets.SortedIDs(assetIDs))
	if err != nil {
		return nil, err
	}
	for _, asset := range owned {
		assetResources[asset.ID] = assets.DocumentReferencedIDs(asset.PayloadJSON, resourceIDs)
	}
	// 本次写入随请求带来的候选素材还没落库，同样算已登记。
	for index := range candidates {
		asset := candidates[index]
		if asset.UserID != userID {
			continue
		}
		assetResources[asset.ID] = assets.DocumentReferencedIDs(asset.PayloadJSON, resourceIDs)
	}
	satisfied := map[string]struct{}{}
	for _, reference := range references {
		if ids, exists := assetResources[reference.AssetID]; exists {
			if _, matches := ids[reference.ResourceID]; matches {
				satisfied[reference.ResourceID] = struct{}{}
			}
		}
	}
	missing := make(map[string]struct{}, len(resourceIDs))
	for resourceID := range resourceIDs {
		if _, ok := satisfied[resourceID]; !ok {
			missing[resourceID] = struct{}{}
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	resources, err := s.repo.ResourcesForUserIDs(userID, assets.SortedIDs(missing))
	if err != nil {
		return nil, err
	}
	adopted := make([]model.Asset, 0, len(resources))
	for _, resource := range resources {
		if resource.Status != model.ResourceStatusReady || !isCanvasMediaKind(resource.Kind) {
			continue
		}
		assetID := MediaAssetIDForResource(userID, resource.ID)
		payload, err := CanvasMediaAssetDocument(assetID, canvasMediaAssetTitle(raw, resource.ID), resource.Kind, resource)
		if err != nil {
			return nil, err
		}
		asset, err := AssetFromJSON(userID, payload)
		if err != nil {
			return nil, err
		}
		adopted = append(adopted, asset)
	}
	if len(adopted) == 0 {
		return nil, nil
	}
	return adopted, nil
}

// canvasMediaAssetTitle 取节点标题做素材标题，让后台素材管理里看到的还是用户在画布上看到的名字。
func canvasMediaAssetTitle(raw json.RawMessage, resourceID string) string {
	var document struct {
		Nodes []struct {
			Title    string `json:"title"`
			Metadata struct {
				StorageKey string `json:"storageKey"`
				Content    string `json:"content"`
			} `json:"metadata"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return ""
	}
	for _, node := range document.Nodes {
		if firstCanvasResourceID(node.Metadata.StorageKey, node.Metadata.Content) == resourceID {
			return node.Title
		}
	}
	return ""
}

// bindCanvasMediaAssetIDs 把补建出来的素材回填进画布引用。
// 节点和时间轴向剪辑都要覆盖：漏掉任何一处，校验依然会拦下这份画布。
func bindCanvasMediaAssetIDs(raw json.RawMessage, adopted []model.Asset) (json.RawMessage, error) {
	byResource := map[string]string{}
	for index := range adopted {
		ids := map[string]struct{}{}
		if err := assets.CollectOwnedDocumentReferences(adopted[index].PayloadJSON, ids); err != nil {
			return nil, err
		}
		for resourceID := range ids {
			byResource[resourceID] = adopted[index].ID
		}
	}
	if len(byResource) == 0 {
		return raw, nil
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	bind := func(metadata map[string]json.RawMessage) {
		var storageKey, content string
		_ = json.Unmarshal(metadata["storageKey"], &storageKey)
		_ = json.Unmarshal(metadata["content"], &content)
		if assetID := byResource[firstCanvasResourceID(storageKey, content)]; assetID != "" {
			metadata["assetId"], _ = json.Marshal(assetID)
		}
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(document["nodes"], &nodes); err != nil && len(document["nodes"]) > 0 {
		return nil, err
	}
	for _, node := range nodes {
		var kind string
		_ = json.Unmarshal(node["type"], &kind)
		if !isCanvasMediaKind(kind) {
			continue
		}
		var metadata map[string]json.RawMessage
		if json.Unmarshal(node["metadata"], &metadata) != nil {
			continue
		}
		bind(metadata)
		node["metadata"], _ = json.Marshal(metadata)
	}
	document["nodes"], _ = json.Marshal(nodes)
	if len(document["timeline"]) > 0 {
		var timeline map[string]json.RawMessage
		var clips []map[string]json.RawMessage
		if json.Unmarshal(document["timeline"], &timeline) == nil {
			if json.Unmarshal(timeline["clips"], &clips) == nil {
				for _, clip := range clips {
					var media map[string]json.RawMessage
					if json.Unmarshal(clip["directMedia"], &media) != nil || media == nil {
						continue
					}
					var kind string
					_ = json.Unmarshal(media["kind"], &kind)
					if !isCanvasMediaKind(kind) {
						continue
					}
					if assetID := byResource[firstCanvasResourceID(jsonString(media["storageKey"]), jsonString(media["url"]), jsonString(media["dataUrl"]), jsonString(media["content"]))]; assetID != "" {
						media["assetId"], _ = json.Marshal(assetID)
						clip["directMedia"], _ = json.Marshal(media)
					}
				}
				timeline["clips"], _ = json.Marshal(clips)
				document["timeline"], _ = json.Marshal(timeline)
			}
		}
	}
	return json.Marshal(document)
}

func jsonString(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 {
		return ""
	}
	_ = json.Unmarshal(raw, &value)
	return value
}
