package repository

import (
	"infinite-canvas/backend/internal/model"
)

func (r *Repository) Workspace(id string) (*model.Workspace, error) {
	var value model.Workspace
	if err := r.db.First(&value, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *Repository) DefaultWorkspace() (*model.Workspace, error) {
	var value model.Workspace
	if err := r.db.Order("created_at ASC").First(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

// EnsureWorkspace 保证某个账号在画布库里有对应的工作区，已存在时原样返回。
//
// 工作区的 ID 就是账号 ID：画布、项目、资产的归属全靠这一列，因此这里不做
// 「按名字查找再复用」——同名不同人必须是两个互不可见的工作区。
func (r *Repository) EnsureWorkspace(id string, name string) (*model.Workspace, error) {
	value := model.Workspace{ID: id, Name: name}
	if err := r.db.Where(model.Workspace{ID: id}).FirstOrCreate(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}
