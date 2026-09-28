package app

import (
	"errors"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

const (
	adminAssetDefaultPageSize = 20
	adminAssetMaxPageSize     = 100
)

// AdminAssetRowView 是管理端素材列表与详情共用的一行。
type AdminAssetRowView struct {
	ID           string `json:"id"`
	UserID       string `json:"userId"`
	UserName     string `json:"userName"`
	Kind         string `json:"kind"`
	Category     string `json:"category"`
	Status       string `json:"status"`
	Title        string `json:"title"`
	VersionCount int64  `json:"versionCount"`
	// PayloadBytes 是占用字节的近似值（payload_json + 各版本 definition_json 的字符长度），
	// 不是对象存储真实用量，详见 repository.AdminAssetRow 的注释。
	PayloadBytes     int64     `json:"payloadBytes"`
	ModerationStatus string    `json:"moderationStatus"`
	ModerationReason string    `json:"moderationReason,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// AdminAssetTotalsView 是素材资源管理的顶部读数。
type AdminAssetTotalsView struct {
	Total      int64 `json:"total"`
	Hidden     int64 `json:"hidden"`
	Removed    int64 `json:"removed"`
	TotalBytes int64 `json:"totalBytes"`
	Users      int64 `json:"users"`
}

// AdminAssetPageView 是分页后的素材列表与顶部读数。
type AdminAssetPageView struct {
	Assets   []AdminAssetRowView  `json:"assets"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"pageSize"`
	Totals   AdminAssetTotalsView `json:"totals"`
}

// AdminAssetPage 返回管理端的素材列表。
func (s *Service) AdminAssetPage(filter repository.AdminAssetFilter) (*AdminAssetPageView, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = adminAssetDefaultPageSize
	}
	if filter.PageSize > adminAssetMaxPageSize {
		filter.PageSize = adminAssetMaxPageSize
	}
	status := strings.ToUpper(strings.TrimSpace(filter.Status))
	switch status {
	case "", string(model.AssetModerationNormal), string(model.AssetModerationHidden), string(model.AssetModerationRemoved):
	default:
		return nil, BadAuthRequest("素材状态取值无效")
	}
	filter.Status = status

	rows, total, err := s.repo.AdminAssetPage(filter)
	if err != nil {
		return nil, err
	}
	totals, err := s.repo.AdminAssetTotals()
	if err != nil {
		return nil, err
	}
	assets := make([]AdminAssetRowView, 0, len(rows))
	for _, row := range rows {
		assets = append(assets, adminAssetRowView(row))
	}
	return &AdminAssetPageView{
		Assets: assets, Total: total, Page: filter.Page, PageSize: filter.PageSize,
		Totals: AdminAssetTotalsView{
			Total: totals.Total, Hidden: totals.Hidden, Removed: totals.Removed,
			TotalBytes: totals.TotalBytes, Users: totals.Users,
		},
	}, nil
}

// AdminAssetDetail 返回单条素材的只读详情。
func (s *Service) AdminAssetDetail(id string) (*AdminAssetRowView, error) {
	row, err := s.repo.AdminAsset(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("素材不存在")
	}
	if err != nil {
		return nil, err
	}
	view := adminAssetRowView(*row)
	return &view, nil
}

// ModerateAsset 记录一次素材处置。
//
// 只落处置结论，不触碰对象存储：本仓库没有 CDN 接入，删除物理文件需要对象存储
// 凭据与异步清理链路，超出这一步的范围。
func (s *Service) ModerateAsset(assetID string, status string, reason string, actorID string) (*AdminAssetRowView, error) {
	normalized := model.AssetModerationStatus(strings.ToUpper(strings.TrimSpace(status)))
	switch normalized {
	case model.AssetModerationNormal, model.AssetModerationHidden, model.AssetModerationRemoved:
	default:
		return nil, BadAuthRequest("素材状态取值无效")
	}
	trimmedReason := truncateRunes(strings.TrimSpace(reason), 500)
	if normalized != model.AssetModerationNormal && trimmedReason == "" {
		return nil, BadAuthRequest("隐藏或删除必须填写理由，留痕需要能解释当时的判断")
	}
	asset, err := s.repo.AdminAsset(assetID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("素材不存在")
	}
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.AssetModerationsByIDs([]string{asset.ID})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	record := model.AssetModeration{
		AssetID: asset.ID, Status: normalized, Reason: trimmedReason,
		ActorID: strings.TrimSpace(actorID), CreatedAt: now, UpdatedAt: now,
	}
	if previous, ok := existing[asset.ID]; ok {
		record.CreatedAt = previous.CreatedAt
	}
	if err := s.repo.UpsertAssetModeration(&record); err != nil {
		return nil, err
	}
	updated, err := s.repo.AdminAsset(asset.ID)
	if err != nil {
		return nil, err
	}
	view := adminAssetRowView(*updated)
	return &view, nil
}

// adminAssetRowView 把仓储行翻成对外视图，并把缺失的审核状态兜底成正常。
func adminAssetRowView(row repository.AdminAssetRow) AdminAssetRowView {
	status := model.AssetModerationStatus(strings.ToUpper(strings.TrimSpace(row.ModerationStatus)))
	switch status {
	case model.AssetModerationHidden, model.AssetModerationRemoved:
	default:
		status = model.AssetModerationNormal
	}
	return AdminAssetRowView{
		ID: row.ID, UserID: row.UserID, UserName: row.UserName, Kind: row.Kind,
		Category: row.Category, Status: row.Status, Title: row.Title,
		VersionCount: row.VersionCount, PayloadBytes: row.PayloadBytes,
		ModerationStatus: string(status), ModerationReason: row.ModerationReason,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
