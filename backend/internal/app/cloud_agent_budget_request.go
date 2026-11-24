package app

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
)

// RequestCloudAgentBudget 记录 Agent 的加额申请。
//
// 这里只写申请，不改 Grant、不改 Run 的额度。真正的批准入口只挂在用户会话上，
// MCP 令牌访问不到；Agent 因此无法通过工具调用给自己加钱。
func (s *Service) RequestCloudAgentBudget(userID, runID string, requestedCredits int64, reason string) (*model.AgentBudgetRequest, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, BadAuthRequest("runId 不能为空")
	}
	if requestedCredits <= 0 || requestedCredits > cloudAgentMaxCreditsLimit {
		return nil, BadAuthRequest("申请额度需要在 1–10,000,000 积分之间")
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 500 {
		reason = string([]rune(reason)[:500])
	}
	execution, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return nil, NotFound("Agent 运行不存在")
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		return nil, BadAuthRequest("Agent 运行状态不完整，无法申请额度")
	}
	grantID := state.Credit.GrantID
	if grantID == "" {
		return nil, BadAuthRequest("当前运行没有可调整的授权；请让用户在连接设置中重新授权")
	}
	grant, err := s.repo.AgentGrant(userID, grantID)
	if err != nil {
		return nil, NotFound("Agent 授权不存在")
	}
	if grant.Status != model.AgentGrantStatusActive {
		return nil, Forbidden("Agent 授权已撤销，无法申请加额")
	}
	return s.repo.CreateAgentBudgetRequest(model.AgentBudgetRequest{
		UserID:           userID,
		GrantID:          grantID,
		RunID:            runID,
		RequestedCredits: requestedCredits,
		Reason:           reason,
	})
}

// ApproveCloudAgentBudget 由用户在会话界面批准加额。
func (s *Service) ApproveCloudAgentBudget(userID, requestID string) (*model.AgentGrant, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, BadAuthRequest("申请 ID 不能为空")
	}
	grant, err := s.repo.ApproveAgentBudgetRequest(userID, requestID)
	if err != nil {
		return nil, WrapAppError(409, "加额申请无法批准：已处理、已撤销或额度不合法", err)
	}
	return grant, nil
}

// CloudAgentBudgetRequests 返回用户最近的加额申请，供设置页展示与批准。
func (s *Service) CloudAgentBudgetRequests(userID string, limit int) ([]model.AgentBudgetRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	requests, err := s.repo.AgentBudgetRequests(userID, limit)
	if err != nil {
		return nil, err
	}
	return requests, nil
}

// AgentBudgetRequestView 是可安全返回给前端的申请视图。
type AgentBudgetRequestView struct {
	ID               string     `json:"id"`
	GrantID          string     `json:"grantId"`
	RunID            string     `json:"runId"`
	RequestedCredits int64      `json:"requestedCredits"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	DecidedAt        *time.Time `json:"decidedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}
