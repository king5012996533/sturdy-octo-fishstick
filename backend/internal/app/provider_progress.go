package app

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"strconv"
	"strings"
)

var providerProgressKeys = []string{"progress", "progresspercent", "progresspercentage", "percent", "percentage", "completion", "completionpercent"}

var providerProgressContainers = []string{"data", "result", "task", "output", "operation", "response", "meta", "metadata"}

// syncProviderTaskProgress 只同步观测性进度，不参与生成结果提交。
// 因此进度解析或持久化失败只记录日志，不能把已经成功的 Provider 响应改判为生成失败。
func (s *Service) syncProviderTaskProgress(ctx context.Context, taskID string, responseBody []byte) {
	if s == nil || s.repo == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	progress, ok := providerProgressFromResponse(responseBody)
	if !ok {
		return
	}
	stage := providerProgressStage(responseBody, progress, providerPollStale(ctx))
	if err := s.repo.UpdateTaskProviderProgress(taskID, progress, stage); err != nil {
		log.Printf("provider progress sync failed: task_id=%s error=%v", taskID, err)
	}
}

// providerProgressStage 给运行中的任务挑阶段文案。
//
// 上游报满进度时它通常只是"自己生成完了"，结果还要等我们发现、下载、落库；
// 这段时间继续写"上游生成中"会让界面停在最大进度上，用户只能理解为卡死。
// 只有响应里确实带了结果地址，才敢说"正在取回结果"。
//
// stale 表示这一轮轮询已经等过了耐心上限（见 videoPollStaleAfter）。它优先于"上游生成中"，
// 但不能盖住"正在取回结果"：后者是更具体、更让人放心的说法。
func providerProgressStage(responseBody []byte, progress int, stale bool) string {
	if progress >= 100 && providerResponseCarriesResult(responseBody) {
		return "上游已生成，正在取回结果"
	}
	if stale {
		return videoPollStaleStage
	}
	return "上游生成中"
}

var providerResultLocatorKeys = []string{"downloadurl", "resulturl", "videourl", "outputurl", "mediaurl", "fileurl", "audiourl", "imageurl"}

// providerResponseCarriesResult 判断响应里是否已经给出可下载的结果地址。
func providerResponseCarriesResult(responseBody []byte) bool {
	if len(responseBody) == 0 {
		return false
	}
	var payload any
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return false
	}
	return findProviderResultLocator(payload, 0)
}

func findProviderResultLocator(value any, depth int) bool {
	if depth > 8 {
		return false
	}
	switch current := value.(type) {
	case map[string]any:
		for _, wantedKey := range providerResultLocatorKeys {
			for key, candidate := range current {
				if normalizeProviderProgressKey(key) != wantedKey {
					continue
				}
				if text, ok := candidate.(string); ok && isProviderResultURL(text) {
					return true
				}
			}
		}
		for _, nested := range current {
			if findProviderResultLocator(nested, depth+1) {
				return true
			}
		}
	case []any:
		for _, nested := range current {
			if findProviderResultLocator(nested, depth+1) {
				return true
			}
		}
	}
	return false
}

func isProviderResultURL(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
}

func providerProgressFromResponse(responseBody []byte) (int, bool) {
	if len(responseBody) == 0 {
		return 0, false
	}
	var payload any
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return 0, false
	}
	return findProviderProgress(payload, 0)
}

func findProviderProgress(value any, depth int) (int, bool) {
	if depth > 8 {
		return 0, false
	}
	switch current := value.(type) {
	case map[string]any:
		for _, wantedKey := range providerProgressKeys {
			for key, candidate := range current {
				if normalizeProviderProgressKey(key) != wantedKey {
					continue
				}
				if progress, ok := normalizeProviderProgressValue(candidate); ok {
					return progress, true
				}
			}
		}
		for _, wantedContainer := range providerProgressContainers {
			for key, nested := range current {
				if normalizeProviderProgressKey(key) != wantedContainer {
					continue
				}
				if progress, ok := findProviderProgress(nested, depth+1); ok {
					return progress, true
				}
			}
		}
	case []any:
		for _, nested := range current {
			if progress, ok := findProviderProgress(nested, depth+1); ok {
				return progress, true
			}
		}
	}
	return 0, false
}

func normalizeProviderProgressValue(value any) (int, bool) {
	var number float64
	explicitPercent := false
	switch current := value.(type) {
	case float64:
		number = current
	case string:
		text := strings.TrimSpace(current)
		if text == "" {
			return 0, false
		}
		explicitPercent = strings.HasSuffix(text, "%")
		text = strings.TrimSpace(strings.TrimSuffix(text, "%"))
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 100 {
		return 0, false
	}
	if !explicitPercent && number > 0 && number < 1 {
		number *= 100
	}
	return int(math.Round(number)), true
}

func normalizeProviderProgressKey(value string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
}
