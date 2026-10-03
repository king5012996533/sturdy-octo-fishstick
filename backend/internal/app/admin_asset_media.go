package app

import (
	"time"

	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
)

// fillAssetPreviews 给素材行补上素材本体的预览地址。
//
// assets 表只存素材定义，本体是 payload_json 引用的那几条 resources。这里把引用
// 解析出来、一次取回资源，再挑第一条可播的资源现场签名（有效期沿用产物对账页的
// adminResourcePreviewTTL，两个页面的地址口径保持一致，避免一处改了另一处没跟上）。
//
// 签不出来（没配 CANVAS_PUBLIC_BASE_URL、资源不在本地、还没跑完上传）就保持空值，
// 让前端显示"无预览"：个别素材签不出地址不是错误，不该把整页列表带崩。
func (s *Service) fillAssetPreviews(views []AdminAssetRowView, payloads map[string]string) {
	candidates := make(map[string][]string, len(views))
	resourceIDs := make([]string, 0, len(views))
	seen := make(map[string]struct{}, len(views))
	for index := range views {
		referenced := assetPreviewResourceIDs(payloads[views[index].ID])
		if len(referenced) == 0 {
			continue
		}
		candidates[views[index].ID] = referenced
		for _, resourceID := range referenced {
			if _, exists := seen[resourceID]; exists {
				continue
			}
			seen[resourceID] = struct{}{}
			resourceIDs = append(resourceIDs, resourceID)
		}
	}
	if len(resourceIDs) == 0 {
		return
	}
	rows, err := s.repo.ResourcesByIDs(resourceIDs)
	if err != nil {
		return
	}
	byID := make(map[string]model.Resource, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	expiresAt := time.Now().Add(adminResourcePreviewTTL)
	for index := range views {
		if views[index].PreviewURL != "" {
			continue
		}
		for _, resourceID := range candidates[views[index].ID] {
			resource, exists := byID[resourceID]
			if !exists || resource.Status != model.ResourceStatusReady || resource.Provider != "local" {
				continue
			}
			// 素材只该引用自己的资源。跨账号引用在数据上不该出现，真出现了也宁可
			// 不预览，而不是把别人的文件挂到这条素材下面。
			if views[index].UserID != "" && resource.UserID != views[index].UserID {
				continue
			}
			signed, signErr := s.signedPublicResourceURL(&resource, expiresAt)
			if signErr != nil {
				continue
			}
			views[index].ResourceID = resource.ID
			views[index].MediaKind = resource.Kind
			views[index].MimeType = resource.MimeType
			views[index].PreviewURL = signed
			break
		}
	}
}

// assetPreviewResourceIDs 抽出素材 payload 里引用的资源 ID。
//
// 复用画布那套引用扫描：它认识 url / dataUrl / coverUrl / storageKey 这些地址字段
// 与 resourceId 这类裸 ID 字段，也天然忽略 blob: 这类浏览器本地地址。返回按 ID 排序，
// 保证同一条素材每次挑到的是同一条资源。
func assetPreviewResourceIDs(payloadJSON string) []string {
	found := map[string]struct{}{}
	if err := assets.CollectOwnedDocumentReferences(payloadJSON, found); err != nil {
		return nil
	}
	return assets.SortedIDs(found)
}
