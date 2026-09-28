package repository

import "infinite-canvas/backend/internal/model"

// AppendAdminAudit keeps an append-only record of local configuration changes.
// It is intentionally independent from accounts, sessions, and SaaS operators.
func (r *Repository) AppendAdminAudit(event *model.AdminAuditEvent) error {
	return r.db.Create(event).Error
}

// AdminAuditEvents 返回按时间倒序的审计记录。
//
// 只读列表，按 (created_at, id) 双列排序：同一秒内落库的多条记录也能稳定分页，
// 否则翻页会出现重复或漏行。
func (r *Repository) AdminAuditEvents(limit int, offset int) ([]model.AdminAuditEvent, int64, error) {
	var total int64
	if err := r.db.Model(&model.AdminAuditEvent{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var events []model.AdminAuditEvent
	if err := r.db.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&events).Error; err != nil {
		return nil, 0, err
	}
	return events, total, nil
}

func (r *Repository) APICallLog(id string) (*model.ApiCallLog, error) {
	var log model.ApiCallLog
	if err := r.db.First(&log, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &log, nil
}
