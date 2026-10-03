package repository

import (
	"strings"

	"infinite-canvas/backend/internal/model"
)

// ModelShowcaseEntries 返回全部模型广场文案，供广场与后台共用。
//
// 排序交给调用方：广场要按货架顺序（能力 → 渠道 → 模型）渲染，后台要按更新时间，
// 在仓储层定死一种顺序只会让另一边再排一次。
func (r *Repository) ModelShowcaseEntries() ([]model.ModelShowcaseEntry, error) {
	var records []model.ModelShowcaseEntry
	if err := r.db.Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// SaveModelShowcaseEntry 按模型标识写入一条广场文案，存在即覆盖。
//
// 用模型标识做幂等键而不是主键：发布脚本每次都是"按上游现状重放"，它手里只有模型标识，
// 让它先查一遍主键再决定新增还是更新，等于把同一件事写两遍。ID 为空时由序列生成。
func (r *Repository) SaveModelShowcaseEntry(entry *model.ModelShowcaseEntry) error {
	if entry == nil {
		return nil
	}
	modelKey := strings.TrimSpace(entry.ModelKey)
	if modelKey == "" {
		return nil
	}
	entry.ModelKey = modelKey

	var existing model.ModelShowcaseEntry
	err := r.db.First(&existing, "model_key = ?", modelKey).Error
	if err != nil {
		id, idErr := r.NextPrefixedID("SHOW")
		if idErr != nil {
			return idErr
		}
		entry.ID = id
		return r.db.Create(entry).Error
	}
	entry.ID = existing.ID
	entry.CreatedAt = existing.CreatedAt
	return r.db.Save(entry).Error
}
