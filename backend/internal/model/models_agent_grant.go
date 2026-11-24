package model

import "time"

const (
	AgentGrantStatusActive  = "active"
	AgentGrantStatusRevoked = "revoked"

	AgentGrantClientMCP   = "mcp"
	AgentGrantClientWeb   = "web"
	AgentGrantClientCodex = "codex"

	AgentCreditScopeRun   = "run"
	AgentCreditScopeDaily = "daily"

	AgentReservationReserved = "reserved"
	AgentReservationCharged  = "charged"
	AgentReservationReleased = "released"

	AgentBudgetRequestPending = "pending"
	AgentBudgetRequestDenied  = "denied"

	// 新建令牌时的默认授权：一次授权可以连续跑完整轮创作，按日封顶防止失控。
	// 用户可以在授权中心收紧或在批准加额时提高，服务端始终以数据库中的值为准。
	AgentGrantDefaultMaxCreditsPerRun int64 = 1000
	AgentGrantDefaultMaxCreditsPerDay int64 = 2000
)

// agentCreditZone 固定为 UTC+8：额度按中国自然日切分，不能随服务器时区漂移。
var agentCreditZone = time.FixedZone("CST", 8*60*60)

// AgentCreditDay 返回一个时刻所属的额度自然日（YYYY-MM-DD）。
func AgentCreditDay(t time.Time) string {
	return t.In(agentCreditZone).Format("2006-01-02")
}

// AgentGrant 是用户对某个 Agent 客户端的一次授权快照。
//
// Token 只引用 Grant ID；预算、权限模式与审批策略都在这里，而不是散落在
// 每个 Run 的请求体里。Version 随每次修改递增，Run 在开始时冻结当时的值：
// 用户改授权或撤销后，旧 Run 的下一次额度预留会被拒绝，不会继续用过期权限。
type AgentGrant struct {
	ID                 string     `json:"id" gorm:"primaryKey;size:36"`
	UserID             string     `json:"userId" gorm:"size:36;index;not null"`
	Client             string     `json:"client" gorm:"size:40;not null"`
	ScopesJSON         string     `json:"scopesJson" gorm:"type:text;not null"`
	PermissionMode     string     `json:"permissionMode" gorm:"size:24;not null"`
	MaxCreditsPerRun   int64      `json:"maxCreditsPerRun" gorm:"not null"`
	MaxCreditsPerDay   int64      `json:"maxCreditsPerDay" gorm:"not null"`
	AutoRetryEnabled   bool       `json:"autoRetryEnabled" gorm:"not null"`
	ApprovalPolicyJSON string     `json:"approvalPolicyJson" gorm:"type:text"`
	Status             string     `json:"status" gorm:"size:24;index;not null"`
	Version            int64      `json:"version" gorm:"not null"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	RevokedAt          *time.Time `json:"revokedAt,omitempty"`
}

func (AgentGrant) TableName() string { return "agent_grants" }

// AgentCreditReservation 是一次任务提交的额度预留。
//
// TaskID 唯一：同一个任务被重放时不能重复占用额度；RefundTask 与预留释放也
// 靠它做到幂等。ReservedCredits 是已经计入 Run 与当日桶的总额，SettledCredits
// 是真正落账的金额（文本结算会补差额）。
type AgentCreditReservation struct {
	ID              string    `json:"id" gorm:"primaryKey;size:36"`
	GrantID         string    `json:"grantId" gorm:"size:36;index"`
	UserID          string    `json:"userId" gorm:"size:36;index;not null"`
	RunID           string    `json:"runId" gorm:"size:36;index;not null"`
	TaskID          string    `json:"taskId" gorm:"size:36;uniqueIndex;not null"`
	Day             string    `json:"day" gorm:"size:10;index;not null"`
	ReservedCredits int64     `json:"reservedCredits" gorm:"not null"`
	SettledCredits  int64     `json:"settledCredits" gorm:"not null"`
	Status          string    `json:"status" gorm:"size:24;index;not null"`
	ExpiresAt       time.Time `json:"expiresAt"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (AgentCreditReservation) TableName() string { return "agent_credit_reservations" }

// AgentCreditBucket 是原子额度计数器。
//
// release 不会把行删掉：删行与并发预留之间会形成一个"新行从 0 开始"的窗口，
// 相当于把上限重置。保留行，只做带条件的 UPDATE，才能在并发下守住上限。
type AgentCreditBucket struct {
	ID              string    `json:"id" gorm:"primaryKey;size:80"`
	Scope           string    `json:"scope" gorm:"size:16;uniqueIndex:uk_agent_credit_bucket,priority:1;not null"`
	ScopeKey        string    `json:"scopeKey" gorm:"size:80;uniqueIndex:uk_agent_credit_bucket,priority:2;not null"`
	Period          string    `json:"period" gorm:"size:16;uniqueIndex:uk_agent_credit_bucket,priority:3;not null"`
	ReservedCredits int64     `json:"reservedCredits" gorm:"not null"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (AgentCreditBucket) TableName() string { return "agent_credit_buckets" }

// AgentBudgetRequest 是 Agent 主动提出的加额申请。它只记录申请，不改授权；
// 只有用户在可信界面批准后，服务端才会提升 Grant 的额度并递增 Version。
type AgentBudgetRequest struct {
	ID               string     `json:"id" gorm:"primaryKey;size:36"`
	UserID           string     `json:"userId" gorm:"size:36;index;not null"`
	GrantID          string     `json:"grantId" gorm:"size:36;index;not null"`
	RunID            string     `json:"runId" gorm:"size:36;index;not null"`
	RequestedCredits int64      `json:"requestedCredits" gorm:"not null"`
	Reason           string     `json:"reason" gorm:"size:500"`
	Status           string     `json:"status" gorm:"size:24;index;not null"`
	DecidedAt        *time.Time `json:"decidedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

func (AgentBudgetRequest) TableName() string { return "agent_budget_requests" }
