package repository

import (
	"strings"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// AdminCreationInspirations 返回全部精选灵感（含已下架），供后台管理使用。
//
// 排序固定为 sort_order 升序、updated_at 降序，与画布模板一致：运营用 sort_order
// 决定前台顺序，同一位置以最近改动者优先，避免两次保存后顺序随机跳变。
func (r *Repository) AdminCreationInspirations() ([]model.CreationInspiration, error) {
	var records []model.CreationInspiration
	if err := r.db.Order("sort_order ASC, updated_at DESC").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// CreationInspirationByID 按主键读取精选灵感。
func (r *Repository) CreationInspirationByID(id string) (*model.CreationInspiration, error) {
	var record model.CreationInspiration
	if err := r.db.First(&record, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// SaveCreationInspiration 落库一条精选灵感。
//
// ID 为空表示新建：主键由序列生成（前缀 INSP），避免把「新建」写成覆盖已有行；
// 非空则按主键整行保存，编辑时连带上架状态、推荐位与排序一起生效。
func (r *Repository) SaveCreationInspiration(record *model.CreationInspiration) error {
	if record == nil {
		return nil
	}
	if strings.TrimSpace(record.ID) == "" {
		id, err := r.NextPrefixedID("INSP")
		if err != nil {
			return err
		}
		record.ID = id
		return r.db.Create(record).Error
	}
	return r.db.Save(record).Error
}

// DeleteCreationInspiration 按主键删除精选灵感；目标不存在时返回 gorm.ErrRecordNotFound。
func (r *Repository) DeleteCreationInspiration(id string) error {
	result := r.db.Delete(&model.CreationInspiration{}, "id = ?", strings.TrimSpace(id))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// CreationInspirationCatalog 返回前台可见的精选灵感：只含已上架，排序同后台。
func (r *Repository) CreationInspirationCatalog() ([]model.CreationInspiration, error) {
	var records []model.CreationInspiration
	if err := r.db.Where("status = ?", model.CreationInspirationOnline).
		Order("sort_order ASC, updated_at DESC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// CountCreationInspirations 统计精选灵感条数，用于首次启动的空表播种判断。
//
// 只用来回答「这张表是不是全新的」：播种必须只在空库发生，否则运营删掉几条种子后
// 下一次重启会把它们原样塞回来，后台的删除操作就变成了假的。
func (r *Repository) CountCreationInspirations() (int64, error) {
	var count int64
	if err := r.db.Model(&model.CreationInspiration{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
