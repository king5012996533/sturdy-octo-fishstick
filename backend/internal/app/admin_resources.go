package app

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const (
	adminResourceDefaultPageSize = 20
	adminResourceMaxPageSize     = 100
	// adminResourcePreviewTTL 是后台预览地址的有效期：够管理员翻完几页对账，又不至于
	// 让一个从后台抄出来的地址长期能匿名下载用户的产物。
	adminResourcePreviewTTL = 12 * time.Hour
)

// AdminResourceView 是管理端「生成产物」列表与详情共用的一行。
type AdminResourceView struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Provider string `json:"provider"`
	// ObjectKey 是存储里的相对路径，对账时要能跟磁盘目录一一对上。
	ObjectKey      string `json:"objectKey"`
	MimeType       string `json:"mimeType"`
	Size           int64  `json:"size"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	DurationMs     int64  `json:"durationMs"`
	PlaybackStatus string `json:"playbackStatus"`
	// Referenced 表示这条产物被用户侧引用过（进了素材库或画布）。为 false 的就是
	// 「上游出了结果、用户那边没拿到」的那批，也是对账最该先看的一批。
	Referenced bool `json:"referenced"`
	// PreviewURL 是现场签发的只读地址，超时自动失效；签不出来时留空，不让整页挂掉。
	PreviewURL string    `json:"previewUrl"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// AdminResourceTotalsView 是产物管理的顶部读数，口径恒为全量，不随筛选变化。
type AdminResourceTotalsView struct {
	Total        int64 `json:"total"`
	Unreferenced int64 `json:"unreferenced"`
	TotalBytes   int64 `json:"totalBytes"`
	Users        int64 `json:"users"`
}

// AdminResourcePageView 是分页后的产物列表与顶部读数。
type AdminResourcePageView struct {
	Resources []AdminResourceView     `json:"resources"`
	Total     int64                   `json:"total"`
	Page      int                     `json:"page"`
	PageSize  int                     `json:"pageSize"`
	Totals    AdminResourceTotalsView `json:"totals"`
}

// AdminResourcePage 返回管理端的产物列表，并补上现场签发的预览地址。
func (s *Service) AdminResourcePage(filter repository.AdminResourceFilter) (*AdminResourcePageView, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = adminResourceDefaultPageSize
	}
	if filter.PageSize > adminResourceMaxPageSize {
		filter.PageSize = adminResourceMaxPageSize
	}
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Kind = strings.TrimSpace(filter.Kind)
	filter.UserID = strings.TrimSpace(filter.UserID)

	rows, total, err := s.repo.AdminResourcePage(filter)
	if err != nil {
		return nil, err
	}
	totals, err := s.repo.AdminResourceTotals()
	if err != nil {
		return nil, err
	}
	resources := make([]AdminResourceView, 0, len(rows))
	expiresAt := time.Now().Add(adminResourcePreviewTTL)
	for _, row := range rows {
		resources = append(resources, s.adminResourceRowView(row, expiresAt))
	}
	return &AdminResourcePageView{
		Resources: resources, Total: total, Page: filter.Page, PageSize: filter.PageSize,
		Totals: AdminResourceTotalsView{
			Total: totals.Total, Unreferenced: totals.Unreferenced,
			TotalBytes: totals.TotalBytes, Users: totals.Users,
		},
	}, nil
}

// adminResourceRowView 把仓储行翻成对外视图。
//
// 预览地址统一用同一个到期时间：列表里每行各算一次 Now() 会让同一页的地址有效期参差，
// 排查时容易误判成「这条链接坏了」。
func (s *Service) adminResourceRowView(row repository.AdminResourceRow, expiresAt time.Time) AdminResourceView {
	resource := row.Resource
	view := AdminResourceView{
		ID: resource.ID, UserID: resource.UserID, UserName: row.UserName,
		Kind: resource.Kind, Status: string(resource.Status), Provider: resource.Provider,
		ObjectKey: resource.ObjectKey, MimeType: resource.MimeType, Size: resource.Size,
		Width: resource.Width, Height: resource.Height, DurationMs: resource.DurationMs,
		PlaybackStatus: resource.PlaybackStatus, Referenced: row.Referenced,
		Error: resource.Error, CreatedAt: resource.CreatedAt, UpdatedAt: resource.UpdatedAt,
	}
	if resource.Status == model.ResourceStatusReady && resource.Provider == "local" {
		if signed, err := s.signedPublicResourceURL(&resource, expiresAt); err == nil {
			view.PreviewURL = signed
		}
	}
	return view
}
