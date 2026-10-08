package app

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const (
	adminResourceDefaultPageSize = 20
	adminResourceMaxPageSize     = 100
	// adminResourcePreviewTTL 是后台预览地址的有效期：够管理员翻完几页对账，又不至于
	// 让一个从后台抄出来的地址长期能匿名下载用户的产物。
	adminResourcePreviewTTL = 12 * time.Hour
)

// AdminResourceView 是管理端「生成产物」列表与详情共用的一行。
type AdminResourceView struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Provider string `json:"provider"`
	// ObjectKey 是存储里的相对路径，对账时要能跟磁盘目录一一对上。
	ObjectKey      string `json:"objectKey"`
	MimeType       string `json:"mimeType"`
	Size           int64  `json:"size"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	DurationMs     int64  `json:"durationMs"`
	PlaybackStatus string `json:"playbackStatus"`
	// Referenced 表示这条产物被用户侧引用过（进了素材库或画布）。为 false 的就是
	// 「上游出了结果、用户那边没拿到」的那批，也是对账最该先看的一批。
	Referenced bool `json:"referenced"`
	// PreviewURL 是现场签发的只读地址，超时自动失效；签不出来时留空，不让整页挂掉。
	PreviewURL string `json:"previewUrl"`
	// TaskID 及其下游字段由托管层补齐：任务在画布库、扣费在账号库，只有同时持有
	// 两套库的那一层才拼得出完整的对账视图。
	TaskID            string `json:"taskId,omitempty"`
	Source            string `json:"source,omitempty"`
	TaskType          string `json:"taskType,omitempty"`
	TaskStatus        string `json:"taskStatus,omitempty"`
	TaskModel         string `json:"taskModel,omitempty"`
	ProviderRequestID string `json:"providerRequestId,omitempty"`
	// ChargedCredits 是该任务的净扣费（扣费减退回），0 表示没扣或已全额退回。
	ChargedCredits int64 `json:"chargedCredits"`
	// ChargeState 见 resourceCharge* 常量，前端据此标出漏单与上线前的历史数据。
	ChargeState string    `json:"chargeState"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// 产物的扣费状态。写成字符串而不是布尔：后台要区分"没关联任务"与"关联了但没扣费"，
// 前者要先回填或排查上报链路，后者才是真的漏单。导出是因为托管层要跨两套库来判定。
const (
	ResourceChargeStateUntracked  = "untracked"
	ResourceChargeStatePrebilling = "prebilling"
	ResourceChargeStateCharged    = "charged"
	ResourceChargeStateUncharged  = "uncharged"
)

// AdminTaskChargeView 是"扣了费但没有任何产物"的一条任务。
type AdminTaskChargeView struct {
	TaskID     string    `json:"taskId"`
	UserID     string    `json:"userId"`
	UserName   string    `json:"userName"`
	Net        int64     `json:"net"`
	ChargedAt  time.Time `json:"chargedAt"`
	TaskType   string    `json:"taskType,omitempty"`
	TaskStatus string    `json:"taskStatus,omitempty"`
	TaskError  string    `json:"taskError,omitempty"`
}

// AdminResourceReconciliationView 是产物对账页顶部的异常汇总。
//
// 两个方向都要有：产物无扣费是平台在替上游垫钱，扣费无产物是用户付了钱没拿到东西。
// 列表都截断到几十条并单独给出总数，超过的部分留给运维按条件再查，而不是把整页
// 响应撑成一份导出文件。
type AdminResourceReconciliationView struct {
	// BillingStart 为空的字符串表示账号库里还没有任何扣费，此时不做漏单判定。
	BillingStart string `json:"billingStart,omitempty"`
	// Untracked 是没有关联任务的产物数（含用户上传——上传本来就没有任务）。
	Untracked int64 `json:"untracked"`
	// Uncharged 是计费上线后创建、却没有任何扣费的产物数，也就是平台的漏单。
	Uncharged int64 `json:"uncharged"`
	// ChargedWithoutResource 是扣了费、却一条产物都没有的任务数。
	ChargedWithoutResource int64                 `json:"chargedWithoutResource"`
	UnchargedResources     []AdminResourceView   `json:"unchargedResources"`
	ChargedTasks           []AdminTaskChargeView `json:"chargedTasks"`
	// Uncollected 是文本按用量结算时收不回来的积分总额，SettleGaps 是明细。
	//
	// 与上面两项并列而不是塞进"扣费无产物"：那两类描述的是"产物与扣费对不上"，
	// 这一项描述的是"该收的钱没收上来"，处置动作是催收或核销，不是补产物。
	Uncollected int64                `json:"uncollected"`
	SettleGaps  []AdminSettleGapView `json:"settleGaps"`
}

// AdminSettleGapView 是一条收不回来的文本结算差额。
type AdminSettleGapView struct {
	TaskID   string `json:"taskId"`
	UserID   string `json:"userId"`
	UserName string `json:"userName,omitempty"`
	ModelKey string `json:"modelKey,omitempty"`
	// Uncollected 是这次没收上来的积分。
	Uncollected int64     `json:"uncollected"`
	CreatedAt   time.Time `json:"createdAt"`
}

// AdminResourceTotalsView 是产物管理的顶部读数，口径恒为全量，不随筛选变化。
type AdminResourceTotalsView struct {
	Total        int64 `json:"total"`
	Unreferenced int64 `json:"unreferenced"`
	// Untracked 是没有 task_id 的产物数：上传的素材与回填后仍对不上的历史产物。
	Untracked  int64 `json:"untracked"`
	TotalBytes int64 `json:"totalBytes"`
	Users      int64 `json:"users"`
}

// AdminResourcePageView 是分页后的产物列表与顶部读数。
type AdminResourcePageView struct {
	Resources []AdminResourceView     `json:"resources"`
	Total     int64                   `json:"total"`
	Page      int                     `json:"page"`
	PageSize  int                     `json:"pageSize"`
	Totals    AdminResourceTotalsView `json:"totals"`
	// Reconciliation 由托管层补齐；纯画布形态下为 nil，前端按缺失处理。
	Reconciliation *AdminResourceReconciliationView `json:"reconciliation,omitempty"`
}

// AdminResourcePage 返回管理端的产物列表，并补上现场签发的预览地址。
func (s *Service) AdminResourcePage(filter repository.AdminResourceFilter) (*AdminResourcePageView, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = adminResourceDefaultPageSize
	}
	if filter.PageSize > adminResourceMaxPageSize {
		filter.PageSize = adminResourceMaxPageSize
	}
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Kind = strings.TrimSpace(filter.Kind)
	filter.UserID = strings.TrimSpace(filter.UserID)

	rows, total, err := s.repo.AdminResourcePage(filter)
	if err != nil {
		return nil, err
	}
	totals, err := s.repo.AdminResourceTotals()
	if err != nil {
		return nil, err
	}
	resources := make([]AdminResourceView, 0, len(rows))
	expiresAt := time.Now().Add(adminResourcePreviewTTL)
	for _, row := range rows {
		resources = append(resources, s.adminResourceRowView(row, expiresAt))
	}
	return &AdminResourcePageView{
		Resources: resources, Total: total, Page: filter.Page, PageSize: filter.PageSize,
		Totals: AdminResourceTotalsView{
			Total: totals.Total, Unreferenced: totals.Unreferenced, Untracked: totals.Untracked,
			TotalBytes: totals.TotalBytes, Users: totals.Users,
		},
	}, nil
}

// AdminTasksByIDs 批量读任务，供托管层把产物关联到生成它的那次调用。
func (s *Service) AdminTasksByIDs(ids []string) (map[string]model.Task, error) {
	return s.repo.AdminTasksByIDs(ids)
}

// ResourceCountsByTaskIDs 统计每个任务产出了多少条产物，用于反向对账。
func (s *Service) ResourceCountsByTaskIDs(taskIDs []string) (map[string]int64, error) {
	return s.repo.ResourceCountsByTaskIDs(taskIDs)
}

// AdminResourceChargeCandidates 返回计费上线后创建、且关联了任务的产物。
//
// 这批是"可能漏扣费"的候选：能不能确定是漏单，取决于账号库里有没有对应的流水，
// 而流水只存在于托管层，所以这里只负责把候选捞全，判定交给调用方。
func (s *Service) AdminResourceChargeCandidates(since time.Time, limit int) ([]AdminResourceView, error) {
	rows, err := s.repo.AdminResourceChargeCandidates(since, limit)
	if err != nil {
		return nil, err
	}
	views := make([]AdminResourceView, 0, len(rows))
	expiresAt := time.Now().Add(adminResourcePreviewTTL)
	for _, resource := range rows {
		views = append(views, s.adminResourceRowView(repository.AdminResourceRow{Resource: resource}, expiresAt))
	}
	return views, nil
}

// adminResourceRowView 把仓储行翻成对外视图。
//
// 预览地址统一用同一个到期时间：列表里每行各算一次 Now() 会让同一页的地址有效期参差，
// 排查时容易误判成「这条链接坏了」。
func (s *Service) adminResourceRowView(row repository.AdminResourceRow, expiresAt time.Time) AdminResourceView {
	resource := row.Resource
	view := AdminResourceView{
		ID: resource.ID, UserID: resource.UserID, UserName: row.UserName,
		Kind: resource.Kind, Status: string(resource.Status), Provider: resource.Provider,
		ObjectKey: resource.ObjectKey, MimeType: resource.MimeType, Size: resource.Size,
		Width: resource.Width, Height: resource.Height, DurationMs: resource.DurationMs,
		PlaybackStatus: resource.PlaybackStatus, Referenced: row.Referenced,
		TaskID: resource.TaskID, Source: resource.Source,
		Error: resource.Error, CreatedAt: resource.CreatedAt, UpdatedAt: resource.UpdatedAt,
	}
	view.ChargeState = ResourceChargeStateUntracked
	if strings.TrimSpace(resource.TaskID) != "" {
		// 关联了任务但还没算过账：托管层会用账号库里的流水把它落成 charged / uncharged。
		view.ChargeState = ""
	}
	if resource.Status == model.ResourceStatusReady && resource.Provider == "local" {
		if signed, err := s.signedPublicResourceURL(&resource, expiresAt); err == nil {
			view.PreviewURL = signed
		}
	}
	return view
}
