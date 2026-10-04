package app

import (
	"infinite-canvas/backend/internal/model"
)

// 模型广场文案的读写入口。
//
// 放在 app 层而不是直接让托管包拿仓储：托管包只该面向"画布侧服务"这一个依赖，
// 它拿到 *gorm.DB 之后会顺手写别的表，而那张表大概率不属于托管侧。这一层很薄，
// 但它是"哪些表允许从托管侧写"的显式清单。
//
// 这张表只在托管实例上存在（见 database.MigrateHostedSharedSchema）；桌面端不会调用
// 这两个方法，因此本地库里没有这张表也不影响任何桌面路径。

// ModelShowcaseEntries 返回全部广场文案。
func (s *Service) ModelShowcaseEntries() ([]model.ModelShowcaseEntry, error) {
	return s.repo.ModelShowcaseEntries()
}

// ModelShowcaseEntryByModelKey 取单个模型的文案，供详情页读取自述文件正文。
func (s *Service) ModelShowcaseEntryByModelKey(modelKey string) (*model.ModelShowcaseEntry, error) {
	return s.repo.ModelShowcaseEntryByModelKey(modelKey)
}

// SaveModelShowcaseEntry 按模型标识幂等写入一条广场文案。
func (s *Service) SaveModelShowcaseEntry(entry *model.ModelShowcaseEntry) error {
	return s.repo.SaveModelShowcaseEntry(entry)
}
