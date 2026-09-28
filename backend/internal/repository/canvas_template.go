package repository

import (
	"strings"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// AdminCanvasTemplates 返回全部画布模板（含已下架），供后台管理使用。
//
// 排序规则固定为 sort_order 升序、updated_at 降序：运营用 sort_order 决定前台
// 顺序，同一位置上的模板以最近改动者优先，避免两次保存后顺序随机跳变。
func (r *Repository) AdminCanvasTemplates() ([]model.CanvasTemplate, error) {
	var records []model.CanvasTemplate
	if err := r.db.Order("sort_order ASC, updated_at DESC").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// CanvasTemplateByID 按主键读取画布模板。
func (r *Repository) CanvasTemplateByID(id string) (*model.CanvasTemplate, error) {
	var record model.CanvasTemplate
	if err := r.db.First(&record, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// CanvasTemplateByCode 按业务标识读取画布模板，用于唯一性冲突检查。
func (r *Repository) CanvasTemplateByCode(code string) (*model.CanvasTemplate, error) {
	var record model.CanvasTemplate
	if err := r.db.First(&record, "code = ?", strings.TrimSpace(code)).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// SaveCanvasTemplate 落库一条画布模板。
//
// ID 为空表示新建：主键由序列生成（前缀 CTPL），避免把「新建」写成覆盖已有行；
// 非空则按主键整行保存，编辑时连带上架状态、推荐位与排序一起生效。
func (r *Repository) SaveCanvasTemplate(record *model.CanvasTemplate) error {
	if record == nil {
		return nil
	}
	if strings.TrimSpace(record.ID) == "" {
		id, err := r.NextPrefixedID("CTPL")
		if err != nil {
			return err
		}
		record.ID = id
		return r.db.Create(record).Error
	}
	return r.db.Save(record).Error
}

// DeleteCanvasTemplate 按主键删除画布模板；目标不存在时返回 gorm.ErrRecordNotFound。
func (r *Repository) DeleteCanvasTemplate(id string) error {
	result := r.db.Delete(&model.CanvasTemplate{}, "id = ?", strings.TrimSpace(id))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// CanvasTemplateCatalog 返回用户端可见的模板目录：只含已上架，排序同后台。
func (r *Repository) CanvasTemplateCatalog() ([]model.CanvasTemplate, error) {
	var records []model.CanvasTemplate
	if err := r.db.Where("status = ?", model.CanvasTemplateOnline).
		Order("sort_order ASC, updated_at DESC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}
