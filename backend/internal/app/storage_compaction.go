package app

import "encoding/json"

// StorageCompactionSummary 汇总一次内联媒体压缩的结果。坏记录必须显式列出，
// 不能静默跳过：跳过即为历史数据仍然占着数据库空间，运维要看得见。
type StorageCompactionSummary struct {
	Scanned       int                        `json:"scanned"`
	Compacted     int                        `json:"compacted"`
	ReleasedBytes int64                      `json:"releasedBytes"`
	Skipped       []StorageCompactionSkip    `json:"skipped,omitempty"`
	Failed        []StorageCompactionFailure `json:"failed,omitempty"`
}

type StorageCompactionSkip struct {
	TaskID string `json:"taskId"`
	Reason string `json:"reason"`
}

type StorageCompactionFailure struct {
	TaskID string `json:"taskId"`
	Error  string `json:"error"`
}

// CompactInlineTaskMedia 把历史任务结果里内联的生成媒体转存进资源表，结果只保留引用。
//
// 生成主链路已经内联转存（见 task_worker 的 persistGeneratedMediaResult），这里覆盖
// 规则修复前落库、或上游类型异常导致漏转的历史任务。历史修复不重算上传配额：这些字节
// 此前已经计入任务存储配额，转存只是换一个归属，不新增用户容量。
func (s *Service) CompactInlineTaskMedia() (StorageCompactionSummary, error) {
	tasks, err := s.repo.TasksWithInlineResultMedia()
	if err != nil {
		return StorageCompactionSummary{}, err
	}
	summary := StorageCompactionSummary{Scanned: len(tasks)}
	for _, task := range tasks {
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
			summary.Skipped = append(summary.Skipped, StorageCompactionSkip{TaskID: task.ID, Reason: "结果 JSON 无法解析"})
			continue
		}
		stored, err := s.persistLegacyGeneratedMediaResult(task.UserID, result)
		if err != nil {
			summary.Failed = append(summary.Failed, StorageCompactionFailure{TaskID: task.ID, Error: err.Error()})
			continue
		}
		if containsInlineMediaValue(stored) {
			summary.Skipped = append(summary.Skipped, StorageCompactionSkip{TaskID: task.ID, Reason: "结果内联媒体未被识别为可保存资源"})
			continue
		}
		encoded, err := json.Marshal(stored)
		if err != nil {
			summary.Failed = append(summary.Failed, StorageCompactionFailure{TaskID: task.ID, Error: err.Error()})
			continue
		}
		if err := s.repo.UpdateTaskResultJSON(task.ID, string(encoded)); err != nil {
			summary.Failed = append(summary.Failed, StorageCompactionFailure{TaskID: task.ID, Error: err.Error()})
			continue
		}
		summary.Compacted++
		summary.ReleasedBytes += int64(len(task.ResultJSON) - len(encoded))
	}
	return summary, nil
}

func containsInlineMediaValue(value interface{}) bool {
	switch item := value.(type) {
	case []interface{}:
		for _, child := range item {
			if containsInlineMediaValue(child) {
				return true
			}
		}
	case map[string]interface{}:
		if inlineMediaValue(item) != "" {
			return true
		}
		for _, child := range item {
			if containsInlineMediaValue(child) {
				return true
			}
		}
	}
	return false
}
