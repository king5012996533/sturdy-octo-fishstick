package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"infinite-canvas/backend/internal/model"
)

// 上游对账是"零成本巡检"：只读上游模型目录，不发起生成。并发与超时都要收紧，
// 一条渠道卡住不能拖住整次巡检。
const (
	channelReconcileConcurrency = 4
	channelReconcileTimeout     = 20 * time.Second
)

// 对账里单个模型的状态。
const (
	ChannelReconcileModelOK      = "ok"
	ChannelReconcileModelMissing = "missing"
	ChannelReconcileModelPartial = "partial"
	ChannelReconcileModelUnknown = "unknown"
)

// 对账里单条渠道的状态。
const (
	ChannelReconcileOK          = "ok"
	ChannelReconcileDrift       = "drift"
	ChannelReconcileError       = "error"
	ChannelReconcileUnsupported = "unsupported"
	ChannelReconcileEmpty       = "empty"
)

// ChannelReconcileModel 是一个在售模型与上游目录的比对结果。
type ChannelReconcileModel struct {
	ModelKey         string   `json:"modelKey"`
	ProviderModelKey string   `json:"providerModelKey"`
	DisplayName      string   `json:"displayName"`
	Status           string   `json:"status"`
	UpstreamKeys     []string `json:"upstreamKeys"`
	MissingKeys      []string `json:"missingKeys"`
}

// ChannelReconcileRow 是一条渠道的对账结果。
type ChannelReconcileRow struct {
	ChannelID     string                  `json:"channelId"`
	ChannelName   string                  `json:"channelName"`
	BaseURL       string                  `json:"baseUrl"`
	Enabled       bool                    `json:"enabled"`
	Status        string                  `json:"status"`
	Error         string                  `json:"error,omitempty"`
	UpstreamCount int                     `json:"upstreamCount"`
	MissingCount  int                     `json:"missingCount"`
	Models        []ChannelReconcileModel `json:"models"`
}

// ChannelReconcileSummary 是巡检的汇总读数，页码式展示用。
type ChannelReconcileSummary struct {
	Checked     int `json:"checked"`
	Drifted     int `json:"drifted"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
	Empty       int `json:"empty"`
}

// ChannelReconcileResult 是一次完整巡检的返回。
type ChannelReconcileResult struct {
	CheckedAt time.Time               `json:"checkedAt"`
	Summary   ChannelReconcileSummary `json:"summary"`
	Rows      []ChannelReconcileRow   `json:"rows"`
}

// ReconcileAdminChannelModels 拿每条渠道的上游模型目录与货架逐条比对。
//
// 只检查启用中的模型：停用的型号本来就不卖给用户，它在上游消失与否不影响
// "货架上有没有坏按钮"这个判断。
func (s *Service) ReconcileAdminChannelModels(ctx context.Context, actor *model.User) (*ChannelReconcileResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	channels, err := s.repo.SystemChannels(true)
	if err != nil {
		return nil, err
	}
	rows := make([]ChannelReconcileRow, len(channels))
	sem := make(chan struct{}, channelReconcileConcurrency)
	var wg sync.WaitGroup
	for index := range channels {
		channel := channels[index]
		wg.Add(1)
		go func(index int, channel model.ModelChannel) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[index] = s.reconcileChannelModels(ctx, actor, channel)
		}(index, channel)
	}
	wg.Wait()

	result := &ChannelReconcileResult{CheckedAt: time.Now(), Rows: make([]ChannelReconcileRow, 0, len(rows))}
	for _, row := range rows {
		// 跳过与探测失败的行都留在结果里：运营要能看到"这条渠道为什么没参与对账"，
		// 只给一个计数会让人以为巡检漏了一条。
		switch row.Status {
		case ChannelReconcileEmpty:
			result.Summary.Empty++
		case ChannelReconcileUnsupported:
			result.Summary.Unsupported++
		case ChannelReconcileError:
			result.Summary.Failed++
			result.Summary.Checked++
		case ChannelReconcileDrift:
			result.Summary.Drifted++
			result.Summary.Checked++
		case ChannelReconcileOK:
			result.Summary.Checked++
		}
		result.Rows = append(result.Rows, row)
	}
	// 有问题的渠道排在前面：运营打开这一页时要先看到"哪条货坏了"。
	sort.SliceStable(result.Rows, func(i, j int) bool {
		if reconcileRank(result.Rows[i].Status) != reconcileRank(result.Rows[j].Status) {
			return reconcileRank(result.Rows[i].Status) < reconcileRank(result.Rows[j].Status)
		}
		return result.Rows[i].ChannelName < result.Rows[j].ChannelName
	})
	return result, nil
}

func reconcileRank(status string) int {
	switch status {
	case ChannelReconcileDrift:
		return 0
	case ChannelReconcileError:
		return 1
	case ChannelReconcileOK:
		return 2
	case ChannelReconcileUnsupported:
		return 3
	default:
		return 4
	}
}

func (s *Service) reconcileChannelModels(ctx context.Context, actor *model.User, channel model.ModelChannel) ChannelReconcileRow {
	row := ChannelReconcileRow{
		ChannelID:   channel.ID,
		ChannelName: channel.Name,
		BaseURL:     channel.BaseURL,
		Enabled:     channel.Enabled,
		Models:      []ChannelReconcileModel{},
	}
	items, err := s.repo.ChannelModels(channel.ID, false)
	if err != nil {
		row.Status = ChannelReconcileError
		row.Error = "读取渠道模型失败：" + err.Error()
		return row
	}
	if len(items) == 0 {
		row.Status = ChannelReconcileEmpty
		row.Error = "渠道没有启用中的模型"
		return row
	}
	format := strings.ToLower(strings.TrimSpace(channel.APIFormat))
	if format != "openai" && format != "gemini" {
		row.Status = ChannelReconcileUnsupported
		row.Error = "该接口协议不支持读取上游模型目录"
		return row
	}
	if err := s.decryptSystemChannelSecrets(&channel); err != nil {
		row.Status = ChannelReconcileError
		row.Error = "渠道密钥无法解密：" + err.Error()
		return row
	}
	headers, err := ParseOutboundHeadersJSON(channel.HeadersJSON)
	if err != nil {
		row.Status = ChannelReconcileError
		row.Error = "渠道请求头无法解析：" + err.Error()
		return row
	}
	probeCtx, cancel := context.WithTimeout(ctx, channelReconcileTimeout)
	defer cancel()
	upstream, err := s.FetchChannelModels(probeCtx, actor, ChannelModelsRequest{
		BaseURL:   channel.BaseURL,
		APIKey:    channel.APIKey,
		APIFormat: channel.APIFormat,
		Headers:   headers,
	})
	if err != nil {
		row.Status = ChannelReconcileError
		row.Error = reconcileProbeError(err)
		for _, item := range items {
			keys := channelModelUpstreamKeys(item)
			row.Models = append(row.Models, ChannelReconcileModel{
				ModelKey:         item.ModelKey,
				ProviderModelKey: item.ProviderModelKey,
				DisplayName:      firstNonEmpty(item.DisplayName, item.ModelKey),
				Status:           ChannelReconcileModelUnknown,
				UpstreamKeys:     keys,
			})
		}
		return row
	}
	available := make(map[string]struct{}, len(upstream))
	for _, name := range upstream {
		available[channelModelCatalogKey(name)] = struct{}{}
	}
	row.UpstreamCount = len(upstream)
	missing := 0
	for _, item := range items {
		view := ChannelReconcileModel{
			ModelKey:         item.ModelKey,
			ProviderModelKey: item.ProviderModelKey,
			DisplayName:      firstNonEmpty(item.DisplayName, item.ModelKey),
			UpstreamKeys:     channelModelUpstreamKeys(item),
			MissingKeys:      []string{},
		}
		for _, key := range view.UpstreamKeys {
			if _, ok := available[channelModelCatalogKey(key)]; !ok {
				view.MissingKeys = append(view.MissingKeys, key)
			}
		}
		switch {
		case len(view.UpstreamKeys) == 0:
			// 既没有 providerModelKey 也没有规格映射的模型无法对账，标成未知而不是
			// 假装它一致——这种行本身就是要补配置的信号。
			view.Status = ChannelReconcileModelUnknown
		case len(view.MissingKeys) == 0:
			view.Status = ChannelReconcileModelOK
		case len(view.MissingKeys) == len(view.UpstreamKeys):
			view.Status = ChannelReconcileModelMissing
			missing++
		default:
			view.Status = ChannelReconcileModelPartial
			missing++
		}
		row.Models = append(row.Models, view)
	}
	row.MissingCount = missing
	if missing > 0 {
		row.Status = ChannelReconcileDrift
		row.Error = fmt.Sprintf("%d 个在售模型的上游标识已不在授权目录里", missing)
		return row
	}
	row.Status = ChannelReconcileOK
	return row
}

// channelModelUpstreamKeys 列出一个模型真正会打到上游的标识。
//
// providerModelKey 是主标识；grok 这类按规格拆 SKU 的模型把真实标识放在 variants
// 里，只比 providerModelKey 会把整组变体误判成缺失。
func channelModelUpstreamKeys(item model.ChannelModel) []string {
	keys := make([]string, 0, 1+len(item.Variants))
	seen := make(map[string]struct{}, 1+len(item.Variants))
	add := func(value string) {
		name := strings.TrimSpace(value)
		if name == "" || name == "*" {
			return
		}
		key := channelModelCatalogKey(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, name)
	}
	add(item.ProviderModelKey)
	for _, variant := range item.Variants {
		add(variant.ProviderModelKey)
	}
	if len(keys) == 0 {
		add(item.ModelKey)
	}
	return keys
}

func reconcileProbeError(err error) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "读取上游模型目录失败"
	}
	return "读取上游模型目录失败：" + message
}

// AdminChannelHealthErrorCode 是一个失败错误码的计数。
type AdminChannelHealthErrorCode struct {
	Code       string `json:"code"`
	StatusCode int    `json:"statusCode"`
	Count      int64  `json:"count"`
}

// AdminChannelHealthRow 是看板上"渠道 × 模型"的一行。
type AdminChannelHealthRow struct {
	ChannelID   string                        `json:"channelId"`
	ChannelName string                        `json:"channelName"`
	Model       string                        `json:"model"`
	Total       int64                         `json:"total"`
	Succeeded   int64                         `json:"succeeded"`
	Failed      int64                         `json:"failed"`
	FailureRate float64                       `json:"failureRate"`
	ErrorCodes  []AdminChannelHealthErrorCode `json:"errorCodes"`
	LastError   string                        `json:"lastError,omitempty"`
	LastErrorAt *time.Time                    `json:"lastErrorAt,omitempty"`
}

// AdminChannelHealthTotals 是窗口内的合计。
type AdminChannelHealthTotals struct {
	Total     int64 `json:"total"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
}

// AdminChannelHealthResult 是失败看板的返回。
type AdminChannelHealthResult struct {
	Days        int                      `json:"days"`
	From        time.Time                `json:"from"`
	Totals      AdminChannelHealthTotals `json:"totals"`
	Rows        []AdminChannelHealthRow  `json:"rows"`
	GeneratedAt time.Time                `json:"generatedAt"`
}

// AdminChannelHealth 按渠道与模型汇总近 N 天的提交失败情况。
//
// 数据只取 api_call_logs 的 create 记录：poll 与 download 是同一次提交的后续动作，
// 混进来会让失败率失去意义。
func (s *Service) AdminChannelHealth(actor *model.User, days int) (*AdminChannelHealthResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	days = normalizeOverviewDays(days)
	now := time.Now()
	from := dayStart(now).AddDate(0, 0, -(days - 1))

	stats, err := s.repo.ChannelCallStats(from)
	if err != nil {
		return nil, err
	}
	errorCounts, err := s.repo.ChannelCallErrorCounts(from)
	if err != nil {
		return nil, err
	}
	lastFailures, err := s.repo.ChannelCallLastFailures(from)
	if err != nil {
		return nil, err
	}
	channels, err := s.repo.SystemChannels(true)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(channels))
	for _, channel := range channels {
		names[channel.ID] = channel.Name
	}
	codes := make(map[string][]AdminChannelHealthErrorCode)
	for _, item := range errorCounts {
		key := item.ChannelID + "\x00" + item.Model
		if len(codes[key]) >= 3 {
			continue
		}
		codes[key] = append(codes[key], AdminChannelHealthErrorCode{Code: item.ErrorCode, StatusCode: item.StatusCode, Count: item.Count})
	}
	latest := make(map[string]time.Time)
	latestText := make(map[string]string)
	for _, item := range lastFailures {
		key := item.ChannelID + "\x00" + item.Model
		if existing, ok := latest[key]; !ok || item.CreatedAt.After(existing) {
			latest[key] = item.CreatedAt
			latestText[key] = item.Error
		}
	}

	result := &AdminChannelHealthResult{
		Days:        days,
		From:        from,
		Rows:        make([]AdminChannelHealthRow, 0, len(stats)),
		GeneratedAt: now,
	}
	for _, item := range stats {
		key := item.ChannelID + "\x00" + item.Model
		row := AdminChannelHealthRow{
			ChannelID:   item.ChannelID,
			ChannelName: firstNonEmpty(names[item.ChannelID], item.ChannelID),
			Model:       item.Model,
			Total:       item.Total,
			Succeeded:   item.Succeeded,
			Failed:      item.Failed,
			ErrorCodes:  codes[key],
			LastError:   latestText[key],
		}
		if row.ErrorCodes == nil {
			row.ErrorCodes = []AdminChannelHealthErrorCode{}
		}
		if item.Total > 0 {
			row.FailureRate = float64(item.Failed) / float64(item.Total)
		}
		if at, ok := latest[key]; ok {
			stamp := at
			row.LastErrorAt = &stamp
		}
		result.Totals.Total += row.Total
		result.Totals.Succeeded += row.Succeeded
		result.Totals.Failed += row.Failed
		result.Rows = append(result.Rows, row)
	}
	sort.SliceStable(result.Rows, func(i, j int) bool {
		if result.Rows[i].Failed != result.Rows[j].Failed {
			return result.Rows[i].Failed > result.Rows[j].Failed
		}
		return result.Rows[i].Total > result.Rows[j].Total
	})
	return result, nil
}
