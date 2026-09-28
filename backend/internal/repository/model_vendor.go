package repository

import (
	"strings"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// ModelVendors 返回全部厂商，按运营排序、其次按稳定标识排序。
//
// 排序里带上 code 是为了让 sort_order 相同的行有确定顺序：否则两次查询的返回顺序
// 可能不同，后台表格在下一次刷新时会出现“行自己换了位置”的观感。
func (r *Repository) ModelVendors() ([]model.ModelVendor, error) {
	records := make([]model.ModelVendor, 0)
	if err := r.db.Order("sort_order asc, code asc").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// ModelVendorByID 按主键读取厂商；缺失时返回 gorm.ErrRecordNotFound。
func (r *Repository) ModelVendorByID(id string) (*model.ModelVendor, error) {
	var record model.ModelVendor
	if err := r.db.First(&record, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// ModelVendorByCode 按稳定标识读取厂商，用于唯一性冲突检查。
func (r *Repository) ModelVendorByCode(code string) (*model.ModelVendor, error) {
	var record model.ModelVendor
	if err := r.db.First(&record, "code = ?", strings.TrimSpace(code)).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// SaveModelVendor 落库一条厂商。
//
// ID 为空表示新建：主键由序列生成（前缀 VENDOR），避免把“新建”写成覆盖已有行。
func (r *Repository) SaveModelVendor(record *model.ModelVendor) error {
	if record == nil {
		return nil
	}
	if strings.TrimSpace(record.ID) == "" {
		id, err := r.NextPrefixedID("VENDOR")
		if err != nil {
			return err
		}
		record.ID = id
		return r.db.Create(record).Error
	}
	return r.db.Save(record).Error
}

// DeleteModelVendor 按主键软删厂商；目标不存在时返回 gorm.ErrRecordNotFound。
//
// 软删而不是物理删除：调用日志与任务里可能仍引用这条厂商，历史记录不能因为一次
// 后台清理就查不到归属。
func (r *Repository) DeleteModelVendor(id string) error {
	result := r.db.Delete(&model.ModelVendor{}, "id = ?", strings.TrimSpace(id))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// VendorCredentials 返回某厂商下的全部凭据。
func (r *Repository) VendorCredentials(vendorID string) ([]model.VendorCredential, error) {
	records := make([]model.VendorCredential, 0)
	if err := r.db.Where("vendor_id = ?", strings.TrimSpace(vendorID)).
		Order("created_at asc, id asc").
		Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// VendorCredentialByID 按主键读取凭据；缺失时返回 gorm.ErrRecordNotFound。
func (r *Repository) VendorCredentialByID(id string) (*model.VendorCredential, error) {
	var record model.VendorCredential
	if err := r.db.First(&record, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// SaveVendorCredential 落库一条凭据；ID 为空表示新建（前缀 CRED）。
func (r *Repository) SaveVendorCredential(record *model.VendorCredential) error {
	if record == nil {
		return nil
	}
	if strings.TrimSpace(record.ID) == "" {
		id, err := r.NextPrefixedID("CRED")
		if err != nil {
			return err
		}
		record.ID = id
		return r.db.Create(record).Error
	}
	return r.db.Save(record).Error
}

// DeleteVendorCredential 按主键软删凭据；目标不存在时返回 gorm.ErrRecordNotFound。
func (r *Repository) DeleteVendorCredential(id string) error {
	result := r.db.Delete(&model.VendorCredential{}, "id = ?", strings.TrimSpace(id))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// CountCredentialsByVendor 统计厂商下的凭据条数，供删除前的“先清空凭据”判断使用。
func (r *Repository) CountCredentialsByVendor(vendorID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.VendorCredential{}).
		Where("vendor_id = ?", strings.TrimSpace(vendorID)).
		Count(&count).Error
	return count, err
}

// CountModelsByVendor 统计厂商下所有凭据挂载的模型行数。
//
// 走 join 而不是先查凭据再逐条统计：厂商列表每行都要这个读数，逐条查询会把一次
// 列表渲染放大成 N+1 次往返。凭据表的软删条件必须手写，原生 join 不会自动带上。
func (r *Repository) CountModelsByVendor(vendorID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ChannelModel{}).
		Joins("JOIN vendor_credentials ON vendor_credentials.channel_id = channel_models.channel_id").
		Where("vendor_credentials.vendor_id = ? AND vendor_credentials.deleted_at IS NULL", strings.TrimSpace(vendorID)).
		Count(&count).Error
	return count, err
}

// CountChannelModels 统计单条渠道下的模型行数，供凭据列表投影使用。
func (r *Repository) CountChannelModels(channelID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ChannelModel{}).
		Where("channel_id = ?", strings.TrimSpace(channelID)).
		Count(&count).Error
	return count, err
}
