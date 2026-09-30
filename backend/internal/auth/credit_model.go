package auth

import (
	"strings"
	"time"
)

// 积分域（账号余额 + 流水）。
//
// 商业化口径是"充值换积分、调用模型扣积分"：`billing_*` 回答"这个账号买了什么"，
// 定价域回答"一次调用该收多少钱"，本域只回答"这个账号现在还剩多少、每一分的进出
// 是谁造成的"。
//
// 三条刻意的设计：
//
//   - 积分就是"分"。余额、流水金额、模型售价一律 int64 存分，域内不存在比例换算。
//     换算一旦成为全局常数，日后调整比例会让历史余额的含义跟着漂移；要改"充多少钱
//     得多少积分"，改的是充值商品的价格与到账积分，不是这里的单位。
//   - 余额是账户上的一个数，流水是它的唯一解释。两者在同一次事务里写，`balance_after`
//     记下该次写入后的余额，对不上账时能直接定位到是哪一条造成的。
//   - 幂等靠唯一约束，不靠调用方自觉。任务重试与支付回调重放都会重复触发入账/出账，
//     (user_id, kind, ref_type, ref_id) 唯一索引让第二次写入必然失败，而不是重复扣分。

// 积分流水的种类。落库用大写字符串，中文只发生在展示层。
const (
	// CreditKindCharge 是任务预扣：任务提交时先把钱扣掉，失败或取消再退回。
	CreditKindCharge = "TASK_CHARGE"
	// CreditKindRefund 是任务退回：只由失败/取消触发，金额恒为正数（退回即入账）。
	CreditKindRefund = "TASK_REFUND"
	// CreditKindTopUp 是充值到账，含套餐附赠；赠送另记一条 CreditKindGift，
	// 让"我买了 100 元"和"平台送我 20 元"在流水里能分开看。
	CreditKindTopUp = "TOPUP"
	CreditKindGift  = "TOPUP_GIFT"
	// CreditKindAdmin 是后台手工调整：补发、纠错、客诉补偿都走它。
	CreditKindAdmin = "ADMIN_ADJUST"
)

// 流水引用的业务对象类型。没有对应业务对象时用 CreditRefSelf。
const (
	CreditRefTask     = "TASK"
	CreditRefOrder    = "ORDER"
	CreditRefSelf     = "SELF"
	CreditRefContract = "CONTRACT"
)

// CreditAccount 是账号的积分账户。
//
// 主键就是 user_id：一个账号一个钱包，不存在"用户有多个钱包再挑一个扣"的语义，
// 也就不需要钱包 ID 参与扣费路径。
//
// LifetimeIn / LifetimeOut 是累计入账与累计出账，与 Balance 在同一次事务里累加。
// 不做成"读流水求和"是因为后台用户列表要按累计消耗排序，逐用户聚合会在列表页
// 变成 N 次全表扫描；而同事务写入保证它不会与流水漂移。
type CreditAccount struct {
	UserID      string `gorm:"column:user_id;primaryKey;size:36"`
	Balance     int64  `gorm:"column:balance"`
	LifetimeIn  int64  `gorm:"column:lifetime_in"`
	LifetimeOut int64  `gorm:"column:lifetime_out"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (CreditAccount) TableName() string { return "credit_accounts" }

// CreditLedgerEntry 是一条积分流水，也是余额的唯一解释。
//
// Amount 带符号：正数入账、负数出账。前端只按符号决定"+"还是"-"与配色，
// 不需要再按 Kind 判断一次方向——那样每加一种流水都要同步改前端。
//
// RefID 必须非空。没有业务引用时取本条 ID 本身，唯一索引因此退化成"主键唯一"，
// 恒成立，而不是把"这条要不要幂等"留给每个调用点各自决定。
type CreditLedgerEntry struct {
	ID     string `gorm:"column:id;primaryKey;size:36"`
	UserID string `gorm:"column:user_id;size:36;uniqueIndex:uk_credit_ledger_idempotency,priority:1;index:idx_credit_ledger_user_created,priority:1"`
	Kind   string `gorm:"column:kind;size:24;uniqueIndex:uk_credit_ledger_idempotency,priority:2;index:idx_credit_ledger_user_created,priority:3"`
	// RefType / RefID 指向造成这次变动的东西：一条任务、一笔订单，或它自己。
	RefType      string    `gorm:"column:ref_type;size:24;uniqueIndex:uk_credit_ledger_idempotency,priority:3"`
	RefID        string    `gorm:"column:ref_id;size:64;uniqueIndex:uk_credit_ledger_idempotency,priority:4"`
	Amount       int64     `gorm:"column:amount"`
	BalanceAfter int64     `gorm:"column:balance_after"`
	Note         string    `gorm:"column:note;size:255"`
	CreatedAt    time.Time `gorm:"column:created_at;index:idx_credit_ledger_user_created,priority:2"`
}

func (CreditLedgerEntry) TableName() string { return "credit_ledger_entries" }

// CreditModels 返回积分域的表结构，供开发库建表使用。生产结构归 CanvasMind 的
// Prisma 迁移所有，这里的清单只服务本地 SQLite。
func CreditModels() []any {
	return []any{
		&CreditAccount{},
		&CreditLedgerEntry{},
	}
}

// validCreditKind 白名单：流水种类是前端映射图标与文案的依据，拼错的 Kind 落库后
// 只会表现为"这条流水没有图标"，不会报错，所以必须在写入口就挡住。
func validCreditKind(raw string) bool {
	switch raw {
	case CreditKindCharge, CreditKindRefund, CreditKindTopUp, CreditKindGift, CreditKindAdmin:
		return true
	default:
		return false
	}
}

// ---------- 对外视图 ----------

// CreditWalletView 是积分账户视图。
//
// Balance 用 int64 原样给分，不做"元"换算：前端展示"1200 积分"时用的是同一个数，
// 一旦后端先折成元，前端再乘回去就会在显示层引入一次浮点舍入。
type CreditWalletView struct {
	UserID      string `json:"userId"`
	Balance     int64  `json:"balance"`
	LifetimeIn  int64  `json:"lifetimeIn"`
	LifetimeOut int64  `json:"lifetimeOut"`
	UpdatedAt   string `json:"updatedAt"`
}

// CreditLedgerEntryView 是一条流水的视图。
//
// 时间用 RFC3339 文本：积分流水是纯展示，前端拿到就能直接渲染，不必再解析一次。
type CreditLedgerEntryView struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Amount       int64  `json:"amount"`
	BalanceAfter int64  `json:"balanceAfter"`
	RefType      string `json:"refType"`
	RefID        string `json:"refId"`
	Note         string `json:"note"`
	CreatedAt    string `json:"createdAt"`
}

// CreditLedgerFilter 是流水分页与筛选参数。
type CreditLedgerFilter struct {
	UserID string
	Kind   string
	// RefType/RefID 按业务引用定位流水，用于回答"这一条任务到底扣了多少"。
	// 两者都不做凭据：它们只在当前账号的流水里筛选，跨账号查不到任何东西。
	RefType  string
	RefID    string
	Page     int
	PageSize int
}

// CreditAccountRowView 是后台积分列表的一行：账户读数 + 账号资料。
//
// 资料字段与余额同处一行，是因为后台看这张表只有一个问题——"这个人还有多少钱"；
// 只回 userId 会让列表变成一串看不懂的字符串，运营还得逐个去账号页对照。
// 账号被删除后账户仍在（资金记录不能因为账号消失就消失），此时资料留空而不是报错。
type CreditAccountRowView struct {
	UserID      string `json:"userId"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Balance     int64  `json:"balance"`
	LifetimeIn  int64  `json:"lifetimeIn"`
	LifetimeOut int64  `json:"lifetimeOut"`
	UpdatedAt   string `json:"updatedAt"`
}

// CreditWalletViewOf 把账户投影成视图。账户不存在时给出零余额而不是报错：
// "没充过钱的用户"是正常状态，不该让积分卡片显示成加载失败。
func CreditWalletViewOf(account *CreditAccount, userID string) CreditWalletView {
	view := CreditWalletView{UserID: userID}
	if account == nil {
		return view
	}
	view.UserID = account.UserID
	view.Balance = account.Balance
	view.LifetimeIn = account.LifetimeIn
	view.LifetimeOut = account.LifetimeOut
	if !account.UpdatedAt.IsZero() {
		view.UpdatedAt = account.UpdatedAt.Format(time.RFC3339)
	}
	return view
}

// CreditLedgerEntryViewOf 把流水投影成视图。
func CreditLedgerEntryViewOf(entry CreditLedgerEntry) CreditLedgerEntryView {
	createdAt := ""
	if !entry.CreatedAt.IsZero() {
		createdAt = entry.CreatedAt.Format(time.RFC3339)
	}
	return CreditLedgerEntryView{
		ID:           entry.ID,
		Kind:         entry.Kind,
		Amount:       entry.Amount,
		BalanceAfter: entry.BalanceAfter,
		RefType:      entry.RefType,
		RefID:        entry.RefID,
		Note:         entry.Note,
		CreatedAt:    createdAt,
	}
}

// CreditLedgerEntryViewsOf 批量投影，列表恒为非 nil 切片，前端直接遍历。
func CreditLedgerEntryViewsOf(entries []CreditLedgerEntry) []CreditLedgerEntryView {
	views := make([]CreditLedgerEntryView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, CreditLedgerEntryViewOf(entry))
	}
	return views
}

// normalizeCreditKind 归一流水种类：首尾空格与大小写在这里抹平，白名单只认大写形态。
func normalizeCreditKind(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// creditTruncateNote 截断备注到列宽。
//
// 备注可能来自上游错误信息，长度不受我们控制；截断而不是拒绝入库，是因为"备注太长"
// 不该让一次已经扣成功的扣费整体失败。
func creditTruncateNote(note string) string {
	const maxNoteLength = 255
	trimmed := strings.TrimSpace(note)
	if len([]rune(trimmed)) <= maxNoteLength {
		return trimmed
	}
	return string([]rune(trimmed)[:maxNoteLength])
}
