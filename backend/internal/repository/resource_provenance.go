package repository

import (
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// 产物溯源的读写。
//
// 溯源要回答的是对账问题：这条产物是哪次生成任务产出的、那个任务有没有扣费。
// 表上没有外键——resources.task_id 与 tasks.id 只是逻辑关联，因为任务清理、数据迁移
// 都可能让一侧先消失；对账场景里"对不上"本身就是结论，不该被外键挡在写入之外。

// ResourceProvenanceUpdate 是一次历史回填要落的关联。
type ResourceProvenanceUpdate struct {
	ResourceID string
	TaskID     string
	Source     string
}

// ResourcesMissingTaskID 返回还没关联任务的产物，按创建时间正序。
//
// 只取回填需要的列：result_json 可能很大，而回填只需要 id 去跟任务结果做匹配。
func (r *Repository) ResourcesMissingTaskID() ([]model.Resource, error) {
	resources := make([]model.Resource, 0)
	err := r.db.Model(&model.Resource{}).
		Select("id", "user_id", "kind", "created_at").
		Where("task_id IS NULL OR task_id = ''").
		Order("created_at asc").
		Find(&resources).Error
	return resources, err
}

// TasksWithResultJSON 返回带结果的任务，按创建时间正序，最多 limit 条。
//
// 倒序取最近 limit 条：回填是补历史，越新的任务越可能还对应着磁盘上真实存在的产物；
// 老任务的结果里引用的资源多半已经清理，扫描它们只会浪费内存。
func (r *Repository) TasksWithResultJSON(limit int) ([]model.Task, error) {
	tasks := make([]model.Task, 0)
	query := r.db.Model(&model.Task{}).
		Select("id", "user_id", "created_at", "result_json").
		Where("result_json IS NOT NULL AND result_json != ''").
		Order("created_at desc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&tasks).Error
	return tasks, err
}

// ApplyResourceProvenance 批量写入回填结果，返回实际更新的条数。
//
// 每条只更新 task_id 仍为空的记录：回填可能被重复执行，第二次不该覆盖第一次已经确认的
// 关联，也不该把 source 从别的来路改写成 generation。
func (r *Repository) ApplyResourceProvenance(updates []ResourceProvenanceUpdate) (int64, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	var applied int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		for _, update := range updates {
			result := tx.Model(&model.Resource{}).
				Where("id = ? AND (task_id IS NULL OR task_id = '')", update.ResourceID).
				Updates(map[string]interface{}{"task_id": update.TaskID, "source": update.Source})
			if result.Error != nil {
				return result.Error
			}
			applied += result.RowsAffected
		}
		return nil
	})
	return applied, err
}

// ResourceProvenanceIndex 返回全量产物的溯源索引，供后台做汇总读数。
//
// 刻意不带上 object_key、mime 这些大字段：对账要的是"有多少条、分别属于谁、什么时候建的"，
// 拉全表只是为了在内存里算三个计数，字段越少越好。
func (r *Repository) ResourceProvenanceIndex() ([]model.Resource, error) {
	resources := make([]model.Resource, 0)
	err := r.db.Model(&model.Resource{}).
		Select("id", "user_id", "kind", "task_id", "created_at").
		Order("created_at desc").
		Find(&resources).Error
	return resources, err
}

// AdminResourceChargeCandidates 返回计费上线后创建、且带任务的产物。
//
// "计费上线后"这个时间点由账号库给出（第一条任务扣费的时间），不是写死的常量：
// 上线前本来就不扣费，把它们算成漏单只会制造噪音。
func (r *Repository) AdminResourceChargeCandidates(since time.Time, limit int) ([]model.Resource, error) {
	// 与后台列表同一个理由：时间列是本地时区，绑定值必须同区，SQLite 的字符串比较才成立。
	since = since.In(time.Local)
	resources := make([]model.Resource, 0)
	query := r.db.Model(&model.Resource{}).
		Where("task_id IS NOT NULL AND task_id != ''")
	if !since.IsZero() {
		query = query.Where("created_at >= ?", since)
	}
	query = query.Order("created_at desc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&resources).Error
	return resources, err
}

// AdminTasksByIDs 批量读任务，键为任务 ID。
//
// 只取后台展示与对账需要的列：prompt、input_json 可能很大，列表页一页几十条，
// 全量读回来会让一次分页请求拖着几 MB 的文本。
func (r *Repository) AdminTasksByIDs(ids []string) (map[string]model.Task, error) {
	result := make(map[string]model.Task, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	tasks := make([]model.Task, 0, len(ids))
	if err := r.db.Model(&model.Task{}).
		Select("id", "user_id", "type", "status", "operation", "provider", "model", "provider_request_id", "error", "created_at", "completed_at").
		Where("id IN ?", ids).
		Find(&tasks).Error; err != nil {
		return nil, err
	}
	for _, task := range tasks {
		result[task.ID] = task
	}
	return result, nil
}

// ResourceCountsByTaskIDs 统计每个任务产出了多少条产物。
//
// 用来找反向漏单：账上扣了钱、任务也跑了，却一条产物都没有。
func (r *Repository) ResourceCountsByTaskIDs(taskIDs []string) (map[string]int64, error) {
	counts := make(map[string]int64, len(taskIDs))
	if len(taskIDs) == 0 {
		return counts, nil
	}
	type row struct {
		TaskID string
		Total  int64
	}
	rows := make([]row, 0, len(taskIDs))
	if err := r.db.Model(&model.Resource{}).
		Select("task_id AS task_id, COUNT(*) AS total").
		Where("task_id IN ?", taskIDs).
		Group("task_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, item := range rows {
		counts[item.TaskID] = item.Total
	}
	return counts, nil
}
