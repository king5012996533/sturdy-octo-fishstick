package repository

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// AdminCanvasFilter 是画布列表的筛选条件。
type AdminCanvasFilter struct {
	Keyword string
	UserID  string
	// Status 取 NORMAL/HIDDEN/REMOVED；空表示不限。
	Status string
	// RestrictIDs / ExcludeIDs 由上层用审核状态换算而来：审核表是托管专属表，
	// 不能出现在这里的 SQL 里。RestrictIDs 非空表示只取这些画布，ExcludeIDs
	// 非空表示排除这些画布。
	RestrictIDs []string
	ExcludeIDs  []string
	Page        int
	Limit       int
}

// AdminCanvasRow 是管理端画布列表的一行。
//
// 只带列表需要的列：PayloadJSON 可能有几百 KB，列表里读全量会让一次翻页变成
// 几十 MB 的搬运。
type AdminCanvasRow struct {
	ID           string
	UserID       string
	ProjectID    string
	Title        string
	Revision     int64
	PayloadBytes int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AdminCanvasPage 返回分页后的画布列表与总数。
//
// 审核状态不参与 SQL：canvas_moderation 是托管专属表，桌面库里根本没有，写进
// WHERE 或 JOIN 会让这条查询在桌面形态下直接报 "no such table"。这里先按画布库
// 自己的条件取页，状态过滤与合并交给上层。
func (r *Repository) AdminCanvasPage(filter AdminCanvasFilter) ([]AdminCanvasRow, int64, error) {
	return r.AdminCanvasPageFiltered(filter)
}

// AdminCanvasPageFiltered 在分页查询上再叠加审核状态换算出来的 ID 限制。
func (r *Repository) AdminCanvasPageFiltered(filter AdminCanvasFilter) ([]AdminCanvasRow, int64, error) {
	build := func() *gorm.DB {
		query := r.db.Model(&model.CanvasProject{})
		if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
			like := "%" + keyword + "%"
			query = query.Where("title LIKE ? OR id LIKE ? OR project_id LIKE ? OR user_id = ?", like, like, like, keyword)
		}
		if userID := strings.TrimSpace(filter.UserID); userID != "" {
			query = query.Where("user_id = ?", userID)
		}
		if len(filter.RestrictIDs) > 0 {
			query = query.Where("id IN ?", filter.RestrictIDs)
		}
		if len(filter.ExcludeIDs) > 0 {
			query = query.Where("id NOT IN ?", filter.ExcludeIDs)
		}
		return query
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []AdminCanvasRow
	query := build().
		Select("id, user_id, project_id, title, revision, LENGTH(payload_json) AS payload_bytes, created_at, updated_at").
		Order("updated_at DESC, id DESC")
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit).Offset((filter.Page - 1) * filter.Limit)
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// CanvasIDsByModerationStatus 返回审核状态对应的画布 ID。
//
// status 为空表示"任何非正常状态"，用于表达"全部已处置画布"。
func (r *Repository) CanvasIDsByModerationStatus(status string) ([]string, error) {
	var ids []string
	query := r.db.Model(&model.CanvasModeration{}).Select("canvas_id")
	if normalized := strings.TrimSpace(status); normalized != "" {
		query = query.Where("status = ?", normalized)
	} else {
		query = query.Where("status <> ?", model.CanvasModerationNormal)
	}
	if err := query.Find(&ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// AdminCanvasProject 按主键读取画布，不限定所有者。
func (r *Repository) AdminCanvasProject(id string) (*model.CanvasProject, error) {
	var project model.CanvasProject
	if err := r.db.First(&project, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return nil, err
	}
	return &project, nil
}

// CanvasModerationsByIDs 批量读取画布的审核状态，避免列表逐行查询。
func (r *Repository) CanvasModerationsByIDs(canvasIDs []string) (map[string]model.CanvasModeration, error) {
	result := make(map[string]model.CanvasModeration, len(canvasIDs))
	if len(canvasIDs) == 0 {
		return result, nil
	}
	var records []model.CanvasModeration
	if err := r.db.Where("canvas_id IN ?", canvasIDs).Find(&records).Error; err != nil {
		return nil, err
	}
	for _, record := range records {
		result[record.CanvasID] = record
	}
	return result, nil
}

// CanvasModeration 读取单个画布的审核状态。
func (r *Repository) CanvasModeration(canvasID string) (*model.CanvasModeration, error) {
	var record model.CanvasModeration
	if err := r.db.First(&record, "canvas_id = ?", strings.TrimSpace(canvasID)).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// UpsertCanvasModeration 写入或覆盖一条审核结论。
//
// 恢复（NORMAL）同样落一行，而不是删掉旧结论：审核历史必须能回答"这块画布被处理过
// 几次、分别是谁做的"，删行等于把前一次处置从证据链里抽走。
func (r *Repository) UpsertCanvasModeration(record *model.CanvasModeration) error {
	if record == nil {
		return nil
	}
	return r.db.Save(record).Error
}

// CanvasModerationsByUser 返回某个账号名下处于非正常状态的画布审核记录。
func (r *Repository) CanvasModerationsByUser(userID string) ([]model.CanvasModeration, error) {
	var records []model.CanvasModeration
	if err := r.db.Where("user_id = ? AND status <> ?", strings.TrimSpace(userID), model.CanvasModerationNormal).
		Order("updated_at DESC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}
