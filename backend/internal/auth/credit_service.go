package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"
)

// 积分域的服务层：把存储层的一致性保证翻译成带 HTTP 语义的结果。
//
// 这一层只做三件事——校验一笔变动是否成立、生成幂等键、把"余额不足"翻译成 402。
// 它不决定"什么时候该扣钱"：任务提交扣、失败退回、支付回调到账分别属于各自域的
// 编排，这里只提供不会算错账的原语。

// CreditMutation 是一次积分变动请求。
//
// Amount 带符号：正数入账、负数出账。不拆成"方向 + 绝对值"两个字段，是因为两者一旦
// 可以互相矛盾（方向=出、金额=正），每个读取点都得自己裁决该信哪一个。
type CreditMutation struct {
	UserID  string
	Kind    string
	Amount  int64
	RefType string
	RefID   string
	Note    string
}

// insufficientCredits 是余额不够时的对外错误。
//
// 用 402 而不是 403：这是"钱不够"不是"没权限"，前端据此引导充值而不是提示联系客服。
// Reason 单独给出机器可读值，前端不必解析中文文案来判断该弹哪个框。
func insufficientCredits(message string) *Error {
	return &Error{
		Status:  http.StatusPaymentRequired,
		Code:    kernel.CodeInsufficientCredits,
		Reason:  kernel.ReasonInsufficientCredits,
		Message: message,
	}
}

// ApplyCreditMutations 落一组积分变动，返回流水视图与"是否新建"。
//
// created 与入参一一对应：false 表示该笔早已记过，调用方应按成功处理并跳过后续副作用
// （例如支付回调重放时不要再发一次货）。
func (s *Service) ApplyCreditMutations(mutations []CreditMutation) ([]CreditLedgerEntryView, []bool, error) {
	entries := make([]CreditLedgerEntry, 0, len(mutations))
	for _, mutation := range mutations {
		entry, err := creditEntryFrom(mutation)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, entry)
	}

	stored, created, err := s.store.AppendCreditEntries(entries)
	if err != nil {
		if errors.Is(err, errCreditInsufficient) {
			return nil, nil, insufficientCredits("积分不足，请先充值后再试")
		}
		return nil, nil, internalFailure(err)
	}
	return CreditLedgerEntryViewsOf(stored), created, nil
}

// CreditWallet 读一个账号的积分账户。
//
// 账户不存在时返回零余额视图而不是 404：没充过钱的账号就是零，把它做成错误会让
// 积分卡片在"新注册用户"这个最常见的情况下显示成加载失败。
func (s *Service) CreditWallet(userID string) (*CreditWalletView, error) {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	account, err := s.store.CreditAccountFor(trimmed)
	if err != nil {
		return nil, internalFailure(err)
	}
	view := CreditWalletViewOf(account, trimmed)
	return &view, nil
}

// CreditLedger 分页读流水。
func (s *Service) CreditLedger(filter CreditLedgerFilter) ([]CreditLedgerEntryView, int64, error) {
	filter.UserID = strings.TrimSpace(filter.UserID)
	if filter.UserID == "" {
		return nil, 0, invalidArgument("缺少账号标识")
	}
	// 筛选种类同样走白名单：拼错的种类会让列表永远为空，而"查不到记录"和"查错了"
	// 在用户眼里长得一模一样。
	if kind := normalizeCreditKind(filter.Kind); kind != "" {
		if !validCreditKind(kind) {
			return nil, 0, invalidArgument("积分流水种类无效")
		}
		filter.Kind = kind
	}
	entries, total, err := s.store.CreditLedgerEntries(filter)
	if err != nil {
		return nil, 0, internalFailure(err)
	}
	return CreditLedgerEntryViewsOf(entries), total, nil
}

// ChargeTaskCredits 预扣一次任务消耗。
//
// 预扣而不是"跑完再扣"：生成任务动辄几十秒到几十分钟，先跑后扣意味着用户可以在这段
// 窗口里把余额花光，平台替他垫付。代价是失败要退——退回由 RefundTaskCredits 负责，
// 与这里的 refID 用同一个任务 ID，流水因此能一眼看出"这笔扣了又退了"。
func (s *Service) ChargeTaskCredits(userID string, taskID string, credits int64, note string) (*CreditLedgerEntryView, bool, error) {
	views, created, err := s.ApplyCreditMutations([]CreditMutation{{
		UserID:  userID,
		Kind:    CreditKindCharge,
		Amount:  -credits,
		RefType: CreditRefTask,
		RefID:   taskID,
		Note:    note,
	}})
	if err != nil {
		return nil, false, err
	}
	return &views[0], created[0], nil
}

// RefundTaskCredits 退回一次任务预扣。
//
// 幂等键与扣费同源（任务 ID）但种类不同，因此"扣一次、退一次"各自只可能成立一次：
// 重复的失败事件不会把同一笔钱退两遍。
func (s *Service) RefundTaskCredits(userID string, taskID string, credits int64, note string) (*CreditLedgerEntryView, bool, error) {
	if credits <= 0 {
		return nil, false, nil
	}
	views, created, err := s.ApplyCreditMutations([]CreditMutation{{
		UserID:  userID,
		Kind:    CreditKindRefund,
		Amount:  credits,
		RefType: CreditRefTask,
		RefID:   taskID,
		Note:    note,
	}})
	if err != nil {
		return nil, false, err
	}
	return &views[0], created[0], nil
}

// GrantTopUpCredits 记一次充值到账，本金与赠送在同一事务里落地。
//
// 两者分成两条流水而不是合并成一条，是为了让"我买了多少"与"平台送我多少"在账单里
// 可分别统计：合并之后退款只退本金时，就没有任何依据知道该退多少。
func (s *Service) GrantTopUpCredits(userID string, orderID string, credits int64, giftCredits int64, note string) ([]CreditLedgerEntryView, []bool, error) {
	mutations := make([]CreditMutation, 0, 2)
	if credits > 0 {
		mutations = append(mutations, CreditMutation{
			UserID:  userID,
			Kind:    CreditKindTopUp,
			Amount:  credits,
			RefType: CreditRefOrder,
			RefID:   orderID,
			Note:    note,
		})
	}
	if giftCredits > 0 {
		mutations = append(mutations, CreditMutation{
			UserID:  userID,
			Kind:    CreditKindGift,
			Amount:  giftCredits,
			RefType: CreditRefOrder,
			RefID:   orderID,
			Note:    note,
		})
	}
	if len(mutations) == 0 {
		return nil, nil, invalidArgument("充值积分必须大于 0")
	}
	return s.ApplyCreditMutations(mutations)
}

// AdjustCredits 是后台的手工调整：补发、纠错、客诉补偿都走它。
//
// 没有业务引用对象，因此幂等键退化成条目自身（见 creditEntryFrom）：管理员点两次
// "补偿 100" 就是两次真实的补偿，不该被误判成重放。
func (s *Service) AdjustCredits(userID string, amount int64, note string) (*CreditLedgerEntryView, error) {
	views, _, err := s.ApplyCreditMutations([]CreditMutation{{
		UserID: userID,
		Kind:   CreditKindAdmin,
		Amount: amount,
		Note:   note,
	}})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// creditEntryFrom 校验并补全一条变动，产出可直接落库的流水。

// authOptionalText 解引用可空文本列，nil 与空串一视同仁。
//
// 账号表把资料列建成可空（第三方登录可能只有其中一项），越界读取会 panic，
// 而"这一列没有值"与"这一列是空串"在展示上没有区别，统一压成空串。
func authOptionalText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// AdminCreditAccounts 是后台的积分账户列表，按余额降序。
//
// 账号资料与账户读数在这里合并，而不是让调用方拿着 userId 再去查一遍账号：
// 一次列表 20 行就是 20 次额外查询，而"这个人叫什么"恰恰是这一页最需要的字段。
func (s *Service) AdminCreditAccounts(filter CreditAccountFilter) ([]CreditAccountRowView, int64, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 20
	}
	accounts, total, err := s.store.CreditAccountPage(filter)
	if err != nil {
		return nil, 0, internalFailure(err)
	}
	views := make([]CreditAccountRowView, 0, len(accounts))
	if len(accounts) == 0 {
		return views, total, nil
	}
	ids := make([]string, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.UserID)
	}
	users, err := s.store.UsersByIDs(ids)
	if err != nil {
		return nil, 0, internalFailure(err)
	}
	directory := make(map[string]User, len(users))
	for _, user := range users {
		directory[user.ID] = user
	}
	for _, account := range accounts {
		view := CreditAccountRowView{
			UserID:      account.UserID,
			Balance:     account.Balance,
			LifetimeIn:  account.LifetimeIn,
			LifetimeOut: account.LifetimeOut,
		}
		if !account.UpdatedAt.IsZero() {
			view.UpdatedAt = account.UpdatedAt.Format(time.RFC3339)
		}
		if user, ok := directory[account.UserID]; ok {
			view.Name = user.DisplayName()
			view.Username = authOptionalText(user.Username)
			view.Email = authOptionalText(user.Email)
			view.Phone = authOptionalText(user.Phone)
		}
		views = append(views, view)
	}
	return views, total, nil
}

func creditEntryFrom(mutation CreditMutation) (CreditLedgerEntry, error) {
	userID := strings.TrimSpace(mutation.UserID)
	if userID == "" {
		return CreditLedgerEntry{}, invalidArgument("积分变动缺少账号")
	}
	kind := normalizeCreditKind(mutation.Kind)
	if !validCreditKind(kind) {
		return CreditLedgerEntry{}, invalidArgument("积分变动种类无效")
	}
	// 零额变动一律拒绝：它不改变余额，却会在流水里留下一行无意义的记录，
	// 而且会让"余额 + 金额 = 变动后余额"这条自查规则多出一个空洞。
	if mutation.Amount == 0 {
		return CreditLedgerEntry{}, invalidArgument("积分变动金额不能为 0")
	}
	id, err := newBillingID()
	if err != nil {
		return CreditLedgerEntry{}, internalFailure(err)
	}
	refType := strings.ToUpper(strings.TrimSpace(mutation.RefType))
	refID := strings.TrimSpace(mutation.RefID)
	if refID == "" {
		// 没有业务引用时用条目自身当引用：唯一索引于是退化成主键唯一，恒成立，
		// 而不是把"这条要不要幂等"留给每个调用点各自决定。
		refType = CreditRefSelf
		refID = id
	}
	if refType == "" {
		refType = CreditRefSelf
	}
	return CreditLedgerEntry{
		ID:      id,
		UserID:  userID,
		Kind:    kind,
		RefType: refType,
		RefID:   refID,
		Amount:  mutation.Amount,
		Note:    creditTruncateNote(mutation.Note),
	}, nil
}
