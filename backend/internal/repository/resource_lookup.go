package repository

import "infinite-canvas/backend/internal/model"

// ResourcesByIDs 按主键批量读取资源，不限定所有者。
//
// 管理端预览要走这条路：素材 payload 里只记了资源 ID，必须取回资源行才能判断它
// 能不能播（是否就绪、是否在本地存储），再现场签发地址。IN 查询不保证顺序，调用方
// 要按自己的列表顺序回填，否则分页顺序会跟着数据库的返回顺序抖。
func (r *Repository) ResourcesByIDs(ids []string) ([]model.Resource, error) {
	if len(ids) == 0 {
		return []model.Resource{}, nil
	}
	resources := make([]model.Resource, 0, len(ids))
	if err := r.db.Where("id IN ?", ids).Find(&resources).Error; err != nil {
		return nil, err
	}
	return resources, nil
}
