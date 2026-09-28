package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

// 工单与反馈域。
//
// 工单放在账号库而不是画布库：一条工单天然绑定一个账号，客服回查"这个人提过什么"、
// "这个账号为什么被封"时，账号与工单必须在同一个库里直接关联；放到画布库会让一次
// 客服会话变成对两套库的交叉查询。
//
// 表结构的生产版本同样由 CanvasMind 的 Prisma 迁移负责，这里的模型只做读写投影，
// 开发库的建表见 EnsureSupportSchema。

// 工单状态（冻结取值）。
const (
	SupportStatusOpen       = "OPEN"
	SupportStatusProcessing = "PROCESSING"
	SupportStatusResolved   = "RESOLVED"
	SupportStatusClosed     = "CLOSED"
)

// 工单分类（冻结取值）。
const (
	SupportCategoryBug     = "BUG"
	SupportCategoryBilling = "BILLING"
	SupportCategoryFeature = "FEATURE"
	SupportCategoryOther   = "OTHER"
)

// 回复作者角色（冻结取值）。
const (
	SupportAuthorUser  = "USER"
	SupportAuthorStaff = "STAFF"
)

const (
	supportTicketTitleMaxRunes   = 80
	supportTicketBodyMaxRunes    = 2000
	supportTicketContactMaxRunes = 120
	supportReplyBodyMaxRunes     = 2000
	// supportTicketNoAttempts 是工单号撞唯一索引后的总尝试次数（首次 + 重试 2 次）。
	supportTicketNoAttempts = 3
)

// SupportTicket 映射 support_tickets。
type SupportTicket struct {
	ID         string     `gorm:"column:id;primaryKey;size:36"`
	TicketNo   string     `gorm:"column:ticket_no;size:32"`
	UserID     string     `gorm:"column:user_id;size:36"`
	Contact    string     `gorm:"column:contact;size:120"`
	Category   string     `gorm:"column:category;size:16"`
	Title      string     `gorm:"column:title;size:80"`
	Body       string     `gorm:"column:body"`
	Status     string     `gorm:"column:status;size:16"`
	AssigneeID string     `gorm:"column:assignee_id;size:36"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
	ClosedAt   *time.Time `gorm:"column:closed_at"`
}

func (SupportTicket) TableName() string { return "support_tickets" }

// SupportTicketReply 映射 support_ticket_replies。
//
// AuthorRole 区分是用户还是客服：同一段对话里两种身份的展示与后续判责完全不同，
// 靠 author_id 反查角色在账号被删/改角色之后就不再可靠。
type SupportTicketReply struct {
	ID         string    `gorm:"column:id;primaryKey;size:36"`
	TicketID   string    `gorm:"column:ticket_id;size:36"`
	AuthorID   string    `gorm:"column:author_id;size:36"`
	AuthorRole string    `gorm:"column:author_role;size:16"`
	Body       string    `gorm:"column:body"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

func (SupportTicketReply) TableName() string { return "support_ticket_replies" }

// SupportModels 返回工单域的全部模型，供上层注册建表。
func SupportModels() []any {
	return []any{
		&SupportTicket{},
		&SupportTicketReply{},
	}
}

// EnsureSupportSchema 仅在本地 SQLite 上建工单表。
//
// 与 EnsureDevSchema 一致：用驱动白名单而不是 TryAutoMigrate——生产库的表结构由
// CanvasMind 的 Prisma 迁移管理，宿主无权按 GORM 的类型推断改写共享的库结构。
func EnsureSupportSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("auth: 数据库连接为空")
	}
	if name := db.Dialector.Name(); name != "sqlite" {
		return fmt.Errorf("auth: 拒绝为 %s 驱动创建工单表；该库的表结构由 Prisma 迁移管理", name)
	}
	if err := db.AutoMigrate(SupportModels()...); err != nil {
		return err
	}
	// AutoMigrate 只按结构体标签建索引，而这里刻意不给模型挂唯一标签：这些表归
	// Prisma 迁移所有，标签一旦与真实 DDL 漂移，宿主就会误以为结构由自己控制。
	// 工单号必须建唯一索引——它决定"撞号重试"在开发环境的行为，与生产一致才能暴露真实竞态。
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_support_tickets_ticket_no ON support_tickets (ticket_no)`,
		`CREATE INDEX IF NOT EXISTS idx_support_tickets_user_updated_at ON support_tickets (user_id, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_support_tickets_status_updated_at ON support_tickets (status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_support_ticket_replies_ticket_created_at ON support_ticket_replies (ticket_id, created_at)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("auth: 创建工单表索引失败（%s）: %w", statement, err)
		}
	}
	return nil
}

// NewSupportTicketNo 生成对外的工单号。
//
// 形态是"T + 日期 + 随机尾号"：日期前缀让客服用工单号就能定位提交时间，随机尾号
// 则让工单号无法被顺序猜测（否则遍历工单号就能读到别人的工单）。
func NewSupportTicketNo(now time.Time) (string, error) {
	buffer := make([]byte, 3)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("auth: 生成工单号失败: %w", err)
	}
	return fmt.Sprintf("T%s%s", now.Format("060102"), strings.ToUpper(hex.EncodeToString(buffer))), nil
}

// supportTicketNoGenerator 生成工单号。
//
// 抽成变量是为了让测试注入固定/连续工单号，覆盖"撞唯一索引后换号重试"这条只靠
// 随机数无法稳定复现的路径。生产恒为 NewSupportTicketNo。
var supportTicketNoGenerator = NewSupportTicketNo

// IsSupportTicketStatus 判断取值是否为冻结的四种状态之一。
func IsSupportTicketStatus(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case SupportStatusOpen, SupportStatusProcessing, SupportStatusResolved, SupportStatusClosed:
		return true
	default:
		return false
	}
}

// SupportTicketInput 是用户提交工单的入参。
type SupportTicketInput struct {
	Contact  string `json:"contact"`
	Category string `json:"category"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

// SupportTicketFilter 是后台工单列表的筛选条件。
type SupportTicketFilter struct {
	Status string
	// Keyword 命中工单号、标题，或账号邮箱/手机号：客服通常只拿到其中一个。
	Keyword  string
	Page     int
	PageSize int
}

// SupportTicketView 是工单读模型（用户端与后台共用）。
type SupportTicketView struct {
	ID         string                   `json:"id"`
	TicketNo   string                   `json:"ticketNo"`
	UserID     string                   `json:"userId"`
	UserName   string                   `json:"userName"`
	UserEmail  string                   `json:"userEmail"`
	UserPhone  string                   `json:"userPhone"`
	Contact    string                   `json:"contact"`
	Category   string                   `json:"category"`
	Title      string                   `json:"title"`
	Body       string                   `json:"body"`
	Status     string                   `json:"status"`
	AssigneeID string                   `json:"assigneeId"`
	CreatedAt  time.Time                `json:"createdAt"`
	UpdatedAt  time.Time                `json:"updatedAt"`
	ClosedAt   *time.Time               `json:"closedAt"`
	Replies    []SupportTicketReplyView `json:"replies"`
}

// SupportTicketReplyView 是对话流里的一条回复。
type SupportTicketReplyView struct {
	ID         string    `json:"id"`
	AuthorID   string    `json:"authorId"`
	AuthorRole string    `json:"authorRole"`
	AuthorName string    `json:"authorName"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

// SupportTicketCounts 是后台指标卡的全量读数（不受当前筛选影响）。
type SupportTicketCounts struct {
	Total      int64 `json:"total"`
	Open       int64 `json:"open"`
	Processing int64 `json:"processing"`
	Resolved   int64 `json:"resolved"`
	Closed     int64 `json:"closed"`
}

// SupportTicketPageView 同时服务用户端（我的工单）与后台（工单管理）。
type SupportTicketPageView struct {
	Tickets  []SupportTicketView `json:"tickets"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"pageSize"`
	// Counts 只在后台列表里返回全局计数；用户端为 nil，不会出现在响应里。
	Counts *SupportTicketCounts `json:"counts,omitempty"`
}

// newSupportID 生成工单域主键。
//
// 与账号库其余表一致用随机主键：可枚举的自增 ID 会让外部按序猜测工单，正是工单号
// 用随机尾号想避免的事。
func newSupportID() string {
	return kernel.NewID()
}
