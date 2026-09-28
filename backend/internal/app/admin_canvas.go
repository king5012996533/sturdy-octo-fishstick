package app

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

const (
	adminCanvasDefaultLimit = 20
	adminCanvasMaxLimit     = 100
	// canvasPreviewNodeLimit 限制详情里回显的节点数：审核需要看内容，不需要把整块
	// 画布搬进浏览器。
	canvasPreviewNodeLimit = 200
	canvasPreviewTextLimit = 160
)

// AdminCanvasQuery 是管理端画布列表的筛选条件。
type AdminCanvasQuery struct {
	Keyword string
	UserID  string
	Status  string
	Page    int
	Limit   int
}

// AdminCanvasListItem 是管理端画布列表的一行。
type AdminCanvasListItem struct {
	ID               string                       `json:"id"`
	UserID           string                       `json:"userId"`
	ProjectID        string                       `json:"projectId,omitempty"`
	Title            string                       `json:"title"`
	Revision         int64                        `json:"revision"`
	PayloadBytes     int64                        `json:"payloadBytes"`
	CreatedAt        time.Time                    `json:"createdAt"`
	UpdatedAt        time.Time                    `json:"updatedAt"`
	ModerationStatus model.CanvasModerationStatus `json:"moderationStatus"`
	ModerationReason string                       `json:"moderationReason,omitempty"`
	ModeratedAt      *time.Time                   `json:"moderatedAt,omitempty"`
	ModeratedBy      string                       `json:"moderatedBy,omitempty"`
}

// AdminCanvasPage 是分页后的画布列表。
type AdminCanvasPage struct {
	Canvases []AdminCanvasListItem `json:"canvases"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"pageSize"`
}

// AdminCanvasNodeBrief 是详情里一个节点的摘要。
type AdminCanvasNodeBrief struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	Content string `json:"content,omitempty"`
}

// AdminCanvasDetail 是单块画布的只读详情。
type AdminCanvasDetail struct {
	AdminCanvasListItem
	NodeCount       int                    `json:"nodeCount"`
	ConnectionCount int                    `json:"connectionCount"`
	NodeKinds       map[string]int         `json:"nodeKinds"`
	Nodes           []AdminCanvasNodeBrief `json:"nodes"`
	NodesTruncated  bool                   `json:"nodesTruncated"`
}

// EnableCanvasModeration 由托管装配显式打开内容审核。
//
// 默认关闭：桌面/本地形态的库结构里根本没有 canvas_moderation 表，每次存画布都
// 去查它会直接把本地编辑打挂。开关只在服务端入口打开一次，运行期不再变化。
func (s *Service) EnableCanvasModeration(enabled bool) {
	if s == nil {
		return
	}
	s.canvasModeration.Store(enabled)
}

func (s *Service) canvasModerationEnabled() bool {
	return s != nil && s.canvasModeration.Load()
}

// canvasModerationBlock 判断一块画布是否已被平台下架。
//
// 读不到审核行、或状态为正常，都表示可以继续。
func (s *Service) canvasModerationBlock(canvasID string) error {
	if !s.canvasModerationEnabled() {
		return nil
	}
	id := strings.TrimSpace(canvasID)
	if id == "" {
		return nil
	}
	record, err := s.repo.CanvasModeration(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.Status == model.CanvasModerationNormal {
		return nil
	}
	message := "该画布已被平台下架，如需申诉请联系客服"
	if reason := strings.TrimSpace(record.Reason); reason != "" {
		message += "（" + reason + "）"
	}
	return Forbidden(message)
}

// canvasModerationState 返回画布当前的审核状态，未记录时按正常处理。
func (s *Service) canvasModerationState(canvasID string) (model.CanvasModerationStatus, string, *time.Time, string) {
	if !s.canvasModerationEnabled() {
		return model.CanvasModerationNormal, "", nil, ""
	}
	record, err := s.repo.CanvasModeration(canvasID)
	if err != nil || record == nil {
		return model.CanvasModerationNormal, "", nil, ""
	}
	at := record.UpdatedAt
	return record.Status, record.Reason, &at, record.ActorUserID
}

// AdminCanvasListView 返回管理端的画布列表。
func (s *Service) AdminCanvasListView(actor *model.User, query AdminCanvasQuery) (*AdminCanvasPage, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.Limit <= 0 {
		query.Limit = adminCanvasDefaultLimit
	}
	if query.Limit > adminCanvasMaxLimit {
		query.Limit = adminCanvasMaxLimit
	}
	status := strings.ToUpper(strings.TrimSpace(query.Status))
	if status != "" && status != string(model.CanvasModerationNormal) && status != string(model.CanvasModerationHidden) && status != string(model.CanvasModerationRemoved) {
		return nil, BadAuthRequest("内容状态取值无效")
	}

	// 状态筛选不能下推到画布库：canvas_moderation 是托管专属表，写进 SQL 会让这条
	// 查询在桌面形态的库上直接报"表不存在"。先把符合条件的画布 ID 取出来再过滤。
	// 被处置的画布是少数，这个集合天然很小。
	var restrictIDs []string
	var excludeIDs []string
	if status != "" {
		targets, err := s.repo.CanvasIDsByModerationStatus(status)
		if err != nil {
			return nil, err
		}
		if status == string(model.CanvasModerationNormal) {
			// "正常"包含两类：没有审核行的画布，以及被恢复过的画布。因此这里取出
			// 所有非正常状态的画布做排除，而不是把全库画布 ID 先收进来。
			excludeIDs = targets
		} else {
			if len(targets) == 0 {
				return &AdminCanvasPage{Canvases: []AdminCanvasListItem{}, Total: 0, Page: query.Page, PageSize: query.Limit}, nil
			}
			restrictIDs = targets
		}
	}

	rows, total, err := s.repo.AdminCanvasPageFiltered(repositoryFilter(query, restrictIDs, excludeIDs))
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	moderations, err := s.repo.CanvasModerationsByIDs(ids)
	if err != nil {
		return nil, err
	}
	items := make([]AdminCanvasListItem, 0, len(rows))
	for _, row := range rows {
		item := AdminCanvasListItem{
			ID: row.ID, UserID: row.UserID, ProjectID: row.ProjectID, Title: row.Title,
			Revision: row.Revision, PayloadBytes: row.PayloadBytes, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			ModerationStatus: model.CanvasModerationNormal,
		}
		if record, ok := moderations[row.ID]; ok {
			at := record.UpdatedAt
			item.ModerationStatus = record.Status
			item.ModerationReason = record.Reason
			item.ModeratedAt = &at
			item.ModeratedBy = record.ActorUserID
		}
		items = append(items, item)
	}
	return &AdminCanvasPage{Canvases: items, Total: total, Page: query.Page, PageSize: query.Limit}, nil
}

// AdminCanvasDetailView 返回单块画布的只读详情（含节点摘要）。
func (s *Service) AdminCanvasDetailView(actor *model.User, canvasID string) (*AdminCanvasDetail, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	project, err := s.repo.AdminCanvasProject(canvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("画布不存在")
	}
	if err != nil {
		return nil, err
	}
	status, reason, moderatedAt, moderatedBy := s.canvasModerationState(project.ID)
	detail := &AdminCanvasDetail{
		AdminCanvasListItem: AdminCanvasListItem{
			ID: project.ID, UserID: project.UserID, ProjectID: project.ProjectID, Title: project.Title,
			Revision: project.Revision, PayloadBytes: int64(len(project.PayloadJSON)),
			CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt,
			ModerationStatus: status, ModerationReason: reason, ModeratedAt: moderatedAt, ModeratedBy: moderatedBy,
		},
		NodeKinds: map[string]int{},
		Nodes:     []AdminCanvasNodeBrief{},
	}
	if err := parseCanvasPreview(project.PayloadJSON, detail); err != nil {
		// 画布正文是用户数据，脏了不该让审核页整页失败：结构统计给不出来就先只回
		// 标题与大小，由审核员决定是否进一步处理。
		detail.NodesTruncated = false
	}
	return detail, nil
}

// AdminSetCanvasModeration 记录一次内容处置。
func (s *Service) AdminSetCanvasModeration(actor *model.User, canvasID string, status model.CanvasModerationStatus, reason string) (*AdminCanvasListItem, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	normalized := model.CanvasModerationStatus(strings.ToUpper(strings.TrimSpace(string(status))))
	if normalized != model.CanvasModerationNormal && normalized != model.CanvasModerationHidden && normalized != model.CanvasModerationRemoved {
		return nil, BadAuthRequest("内容状态取值无效")
	}
	project, err := s.repo.AdminCanvasProject(canvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("画布不存在")
	}
	if err != nil {
		return nil, err
	}
	trimmedReason := truncateRunes(strings.TrimSpace(reason), 500)
	if normalized != model.CanvasModerationNormal && trimmedReason == "" {
		return nil, BadAuthRequest("下架或移除必须填写理由，留痕需要能解释当时的判断")
	}
	record, err := s.repo.CanvasModeration(project.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		record = nil
	} else if err != nil {
		return nil, err
	}
	now := time.Now()
	next := model.CanvasModeration{
		CanvasID: project.ID, UserID: project.UserID, Status: normalized, Reason: trimmedReason,
		ActorUserID: actor.ID, CreatedAt: now, UpdatedAt: now,
	}
	if record != nil {
		next.CreatedAt = record.CreatedAt
	}
	if err := s.repo.UpsertCanvasModeration(&next); err != nil {
		return nil, err
	}
	summary := map[model.CanvasModerationStatus]string{
		model.CanvasModerationNormal:  "恢复画布内容",
		model.CanvasModerationHidden:  "下架画布内容",
		model.CanvasModerationRemoved: "移除画布内容",
	}[normalized]
	if err := s.appendAdminAudit(actor, "canvas.moderation", "canvas", project.ID, summary, map[string]any{
		"status": normalized, "reason": trimmedReason, "ownerUserId": project.UserID,
	}); err != nil {
		return nil, err
	}
	at := next.UpdatedAt
	return &AdminCanvasListItem{
		ID: project.ID, UserID: project.UserID, ProjectID: project.ProjectID, Title: project.Title,
		Revision: project.Revision, PayloadBytes: int64(len(project.PayloadJSON)),
		CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt,
		ModerationStatus: normalized, ModerationReason: trimmedReason, ModeratedAt: &at, ModeratedBy: actor.ID,
	}, nil
}

func repositoryFilter(query AdminCanvasQuery, restrictIDs []string, excludeIDs []string) repository.AdminCanvasFilter {
	return repository.AdminCanvasFilter{
		Keyword: query.Keyword, UserID: query.UserID,
		RestrictIDs: restrictIDs, ExcludeIDs: excludeIDs,
		Page: query.Page, Limit: query.Limit,
	}
}

// parseCanvasPreview 从画布正文里抽出节点摘要。
func parseCanvasPreview(payload string, detail *AdminCanvasDetail) error {
	var document struct {
		Nodes []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			Metadata struct {
				Content string `json:"content"`
			} `json:"metadata"`
		} `json:"nodes"`
		Connections []json.RawMessage `json:"connections"`
	}
	if err := json.Unmarshal([]byte(payload), &document); err != nil {
		return err
	}
	detail.NodeCount = len(document.Nodes)
	detail.ConnectionCount = len(document.Connections)
	for _, node := range document.Nodes {
		kind := strings.TrimSpace(node.Type)
		if kind == "" {
			kind = "unknown"
		}
		detail.NodeKinds[kind]++
		if len(detail.Nodes) >= canvasPreviewNodeLimit {
			detail.NodesTruncated = true
			continue
		}
		detail.Nodes = append(detail.Nodes, AdminCanvasNodeBrief{
			ID: node.ID, Type: kind, Title: node.Title,
			Content: truncateRunes(strings.TrimSpace(node.Metadata.Content), canvasPreviewTextLimit),
		})
	}
	return nil
}

// OwnCanvasModeration 是用户自己看到的"被下架画布"清单。
type OwnCanvasModeration struct {
	CanvasID  string                       `json:"canvasId"`
	Status    model.CanvasModerationStatus `json:"status"`
	Reason    string                       `json:"reason,omitempty"`
	UpdatedAt time.Time                    `json:"updatedAt"`
}

// OwnCanvasModeration 返回当前账号名下被下架的画布。
//
// 只给结论不给审核员身份：用户需要知道"我的东西被处理了、为什么"，不需要知道
// 是哪位审核员做的——那是内部留痕。
func (s *Service) OwnCanvasModeration(actor *model.User) ([]OwnCanvasModeration, error) {
	if actor == nil || actor.ID == "" {
		return nil, Unauthorized("请先登录")
	}
	if !s.canvasModerationEnabled() {
		return []OwnCanvasModeration{}, nil
	}
	records, err := s.repo.CanvasModerationsByUser(actor.ID)
	if err != nil {
		return nil, err
	}
	items := make([]OwnCanvasModeration, 0, len(records))
	for _, record := range records {
		items = append(items, OwnCanvasModeration{
			CanvasID: record.CanvasID, Status: record.Status, Reason: record.Reason, UpdatedAt: record.UpdatedAt,
		})
	}
	return items, nil
}
