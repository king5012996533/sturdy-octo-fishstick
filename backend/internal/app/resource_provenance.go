package app

import (
	"encoding/json"
	"strings"

	"infinite-canvas/backend/internal/repository"
)

// 产物溯源的历史回填。
//
// 为什么必须回填而不是"只看新数据"：对账要看的是过去这几天的账，历史产物没有 task_id，
// 后台只能把它们全标成"未关联任务"，等于把 88 条能对上的也一起扔进噪音里。回填的价值
// 不是补数据本身，而是把"对不上"从一片变成一小撮，剩下的才是真要人工看的。

// ResourceProvenanceBackfillResult 是一次回填的结果读数。
type ResourceProvenanceBackfillResult struct {
	// TasksScanned 是实际扫过结果的任务数，ResourcesMissing 是待关联的产物数。
	TasksScanned     int `json:"tasksScanned"`
	ResourcesMissing int `json:"resourcesMissing"`
	// Linked 是这次能确定来源并已写入（或演练中会写入）的产物数。
	Linked int `json:"linked"`
	// Unmatched 是扫完仍然对不上任何任务的产物数：上传的素材、任务记录已清理的产物，
	// 以及结果里压根没写 resourceId 的旧数据都会落在这里。
	Unmatched int `json:"unmatched"`
	// Applied 为 false 表示这是一次演练，没有写库。
	Applied bool `json:"applied"`
}

// BackfillResourceProvenance 把历史产物关联回它们的生成任务。
//
// 只在 task_id 为空的记录上写，因此可以重复执行：第二次跑只会把第一次还没来得及
// 覆盖到的补上，不会改写已经确认的关联。
func (s *Service) BackfillResourceProvenance(dryRun bool) (ResourceProvenanceBackfillResult, error) {
	result := ResourceProvenanceBackfillResult{}
	missing, err := s.repo.ResourcesMissingTaskID()
	if err != nil {
		return result, err
	}
	result.ResourcesMissing = len(missing)
	pending := make(map[string]struct{}, len(missing))
	for _, resource := range missing {
		pending[resource.ID] = struct{}{}
	}

	tasks, err := s.repo.TasksWithResultJSON(0)
	if err != nil {
		return result, err
	}
	result.TasksScanned = len(tasks)

	// 任务按创建时间倒序返回：同一条产物若被两次任务结果引用（重试、人工恢复），
	// 先遇到的是较新的那次，而用户看到的就是最新一次的结果。
	updates := make([]repository.ResourceProvenanceUpdate, 0, len(pending))
	claimed := make(map[string]struct{}, len(pending))
	for _, task := range tasks {
		for _, resourceID := range resourceIDsInResultJSON(task.ResultJSON, pending) {
			if _, done := claimed[resourceID]; done {
				continue
			}
			claimed[resourceID] = struct{}{}
			updates = append(updates, repository.ResourceProvenanceUpdate{
				ResourceID: resourceID, TaskID: task.ID, Source: resourceSourceGeneration,
			})
		}
	}
	result.Linked = len(updates)
	result.Unmatched = len(missing) - len(updates)
	result.Applied = !dryRun
	if dryRun || len(updates) == 0 {
		return result, nil
	}
	applied, err := s.repo.ApplyResourceProvenance(updates)
	if err != nil {
		return result, err
	}
	result.Linked = int(applied)
	result.Unmatched = len(missing) - int(applied)
	return result, nil
}

// resourceIDsInResultJSON 从任务结果里挑出产物 ID。
//
// 解析 JSON 而不是对整段文本做子串匹配：result_json 里还有 prompt、模型名等自由文本，
// 文本匹配会把恰好长得像资源 ID 的片段也算进来。这里只认两个真来源——上游结果落库时
// 写入的 resourceId 字段，以及同一处写下的 storageKey（resource:<id>）。
//
// 只看解析结果中命中 pending 的 ID：回填面对的可能是一次全量扫描，把每个任务引用的
// 所有 ID 都收进内存没有意义。
func resourceIDsInResultJSON(raw string, pending map[string]struct{}) []string {
	if strings.TrimSpace(raw) == "" || len(pending) == 0 {
		return nil
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	found := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	var walk func(value interface{})
	walk = func(value interface{}) {
		switch item := value.(type) {
		case map[string]interface{}:
			for key, child := range item {
				if text, ok := child.(string); ok {
					if id := resourceIDFromResultField(key, text); id != "" {
						if _, hit := pending[id]; hit {
							if _, dup := seen[id]; !dup {
								seen[id] = struct{}{}
								found = append(found, id)
							}
						}
					}
				}
				walk(child)
			}
		case []interface{}:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(decoded)
	return found
}

func resourceIDFromResultField(key string, value string) string {
	switch key {
	case "resourceId":
		return strings.TrimSpace(value)
	case "storageKey":
		return strings.TrimPrefix(strings.TrimSpace(value), "resource:")
	default:
		return ""
	}
}
