package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// 工单服务层：提交、查询、回复与状态流转。
//
// 这一层负责三件写库之前就要定下来的事：字段校验、账号归属判定、状态机。归属判定
// 尤其重要——它决定"读别人的工单"和"读不存在的工单"是不是同一种结果（见
// supportTicketNotFoundMessage），两者若给出不同文案，工单号就变成了可枚举的探针。

// supportTicketNotFoundMessage 是"工单不存在"与"越权访问"共用的文案。
//
// 刻意不区分：对当前账号而言，别人的工单本就不该存在。分开写会让攻击者通过文案
// 差异判断某个 id 是否真实存在，进而枚举全站工单。
const supportTicketNotFoundMessage = "工单不存在"

// CreateSupportTicket 提交一条工单。
func (s *Service) CreateSupportTicket(userID string, input SupportTicketInput) (*SupportTicketView, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	title, err := validateSupportText(input.Title, "工单标题", 1, supportTicketTitleMaxRunes)
	if err != nil {
		return nil, err
	}
	body, err := validateSupportText(input.Body, "工单正文", 1, supportTicketBodyMaxRunes)
	if err != nil {
		return nil, err
	}
	category, err := normalizeSupportCategory(input.Category)
	if err != nil {
		return nil, err
	}
	contact, err := validateSupportContact(input.Contact)
	if err != nil {
		return nil, err
	}

	now := s.now()
	ticket := &SupportTicket{
		UserID:   uid,
		Contact:  contact,
		Category: category,
		Title:    title,
		Body:     body,
		Status:   SupportStatusOpen,
	}
	// 工单号是随机的，撞唯一索引时换一个再试：连续 2 次都撞上属于极小概率，到那时
	// 只可能是索引/数据异常，按内部故障处理而不是把一个可重试的提交无限拖住。
	inserted := false
	for attempt := 0; attempt < supportTicketNoAttempts; attempt++ {
		ticketNo, genErr := supportTicketNoGenerator(now)
		if genErr != nil {
			return nil, internalFailure(genErr)
		}
		ticket.ID = newSupportID()
		ticket.TicketNo = ticketNo
		ok, createErr := s.store.CreateSupportTicket(ticket)
		if createErr != nil {
			return nil, internalFailure(createErr)
		}
		if ok {
			inserted = true
			break
		}
	}
	if !inserted {
		return nil, internalFailure(errors.New("auth: 工单号连续冲突，无法生成唯一工单号"))
	}
	view := s.supportTicketView(*ticket, nil)
	return &view, nil
}

// UserSupportTickets 分页返回当前账号的工单。
//
// 列表不展开对话：一页 20 条工单各带一串回复会让响应体随使用时长无限增长，而列表页
// 只需要状态与标题。对话在 SupportTicketForUser 里按需读取。
func (s *Service) UserSupportTickets(userID string, page int, pageSize int) (*SupportTicketPageView, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	page, pageSize = normalizeSupportPage(page, pageSize)
	tickets, total, err := s.store.SupportTicketsByUser(uid, page, pageSize)
	if err != nil {
		return nil, internalFailure(err)
	}
	views, err := s.supportTicketViews(tickets, nil)
	if err != nil {
		return nil, err
	}
	return &SupportTicketPageView{Tickets: views, Total: total, Page: page, PageSize: pageSize}, nil
}

// SupportTicketForUser 读取当前账号的一条工单（含对话流）。
func (s *Service) SupportTicketForUser(userID string, ticketID string) (*SupportTicketView, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	ticket, err := s.loadOwnedSupportTicket(uid, ticketID)
	if err != nil {
		return nil, err
	}
	return s.supportTicketDetail(*ticket)
}

// ReplySupportTicket 以用户身份回复自己的工单。
func (s *Service) ReplySupportTicket(userID string, ticketID string, body string) (*SupportTicketView, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	ticket, err := s.loadOwnedSupportTicket(uid, ticketID)
	if err != nil {
		return nil, err
	}
	// 先判状态再校验正文：工单已关闭时"不能再回复"才是用户需要知道的事，
	// 这时候报"正文不能为空"只会让人以为补几个字就能发出去。
	if ticket.Status == SupportStatusClosed {
		return nil, conflict("工单已关闭")
	}
	content, err := validateSupportText(body, "回复内容", 1, supportReplyBodyMaxRunes)
	if err != nil {
		return nil, err
	}
	reply := &SupportTicketReply{
		TicketID:   ticket.ID,
		AuthorID:   uid,
		AuthorRole: SupportAuthorUser,
		Body:       content,
	}
	if err := s.store.CreateSupportTicketReply(reply); err != nil {
		return nil, internalFailure(err)
	}
	return s.supportTicketDetail(*ticket)
}

// AdminSupportTickets 后台工单列表，附带不受筛选影响的全量计数。
func (s *Service) AdminSupportTickets(filter SupportTicketFilter) (*SupportTicketPageView, error) {
	status := strings.ToUpper(strings.TrimSpace(filter.Status))
	if status != "" && !IsSupportTicketStatus(status) {
		return nil, invalidArgument("工单状态不合法")
	}
	page, pageSize := normalizeSupportPage(filter.Page, filter.PageSize)
	tickets, total, err := s.store.AdminSupportTickets(SupportTicketFilter{
		Status:   status,
		Keyword:  strings.TrimSpace(filter.Keyword),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		return nil, internalFailure(err)
	}
	views, err := s.supportTicketViews(tickets, nil)
	if err != nil {
		return nil, err
	}
	counts, err := s.store.SupportTicketStatusCounts()
	if err != nil {
		return nil, internalFailure(err)
	}
	return &SupportTicketPageView{Tickets: views, Total: total, Page: page, PageSize: pageSize, Counts: &counts}, nil
}

// AdminSupportTicket 后台读取单条工单。
func (s *Service) AdminSupportTicket(ticketID string) (*SupportTicketView, error) {
	ticket, err := s.store.SupportTicketByID(strings.TrimSpace(ticketID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notFound(supportTicketNotFoundMessage)
		}
		return nil, internalFailure(err)
	}
	return s.supportTicketDetail(*ticket)
}

// AdminReplySupportTicket 以客服身份回复工单。
//
// 首次回复会把 OPEN 自动推进到 PROCESSING：一条已经有客服答复的工单还挂在"待处理"
// 会让后台的待办计数永远清理不掉。
func (s *Service) AdminReplySupportTicket(ticketID string, actorID string, body string) (*SupportTicketView, error) {
	id := strings.TrimSpace(ticketID)
	ticket, err := s.store.SupportTicketByID(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notFound(supportTicketNotFoundMessage)
		}
		return nil, internalFailure(err)
	}
	content, err := validateSupportText(body, "回复内容", 1, supportReplyBodyMaxRunes)
	if err != nil {
		return nil, err
	}
	reply := &SupportTicketReply{
		TicketID:   ticket.ID,
		AuthorID:   strings.TrimSpace(actorID),
		AuthorRole: SupportAuthorStaff,
		Body:       content,
	}
	if err := s.store.CreateSupportTicketReply(reply); err != nil {
		return nil, internalFailure(err)
	}
	if ticket.Status == SupportStatusOpen {
		now := s.now()
		if err := s.store.UpdateSupportTicketStatus(ticket.ID, SupportStatusProcessing, ticket.ClosedAt, now); err != nil {
			return nil, internalFailure(err)
		}
		ticket.Status = SupportStatusProcessing
		ticket.UpdatedAt = now
	}
	return s.supportTicketDetail(*ticket)
}

// UpdateSupportTicketStatus 后台流转工单状态。
func (s *Service) UpdateSupportTicketStatus(ticketID string, status string) (*SupportTicketView, error) {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	if !IsSupportTicketStatus(normalized) {
		return nil, invalidArgument("工单状态不合法")
	}
	ticket, err := s.store.SupportTicketByID(strings.TrimSpace(ticketID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notFound(supportTicketNotFoundMessage)
		}
		return nil, internalFailure(err)
	}
	now := s.now()
	var closedAt *time.Time
	switch {
	case normalized == SupportStatusClosed && ticket.ClosedAt != nil:
		// 已关闭的工单重复关闭：保留第一次的关闭时间，它才是"什么时候结束的"。
		closedAt = ticket.ClosedAt
	case normalized == SupportStatusClosed:
		closedAt = &now
	default:
		// 离开 CLOSED（重新打开 / 标记已解决）时要清掉关闭时间，否则详情里会
		// 出现"状态是处理中、却写着关闭时间"的矛盾读数。
		closedAt = nil
	}
	if err := s.store.UpdateSupportTicketStatus(ticket.ID, normalized, closedAt, now); err != nil {
		return nil, internalFailure(err)
	}
	ticket.Status = normalized
	ticket.ClosedAt = closedAt
	ticket.UpdatedAt = now
	return s.supportTicketDetail(*ticket)
}

// loadOwnedSupportTicket 读取工单并校验归属；越权与不存在返回同一文案的 forbidden。
func (s *Service) loadOwnedSupportTicket(userID string, ticketID string) (*SupportTicket, error) {
	id := strings.TrimSpace(ticketID)
	if id == "" {
		return nil, forbidden(supportTicketNotFoundMessage)
	}
	ticket, err := s.store.SupportTicketByID(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, forbidden(supportTicketNotFoundMessage)
		}
		return nil, internalFailure(err)
	}
	if ticket.UserID != userID {
		return nil, forbidden(supportTicketNotFoundMessage)
	}
	return ticket, nil
}

// supportTicketDetail 拼装带对话流的单条视图。
func (s *Service) supportTicketDetail(ticket SupportTicket) (*SupportTicketView, error) {
	replies, err := s.store.SupportTicketReplies(ticket.ID)
	if err != nil {
		return nil, internalFailure(err)
	}
	view := s.supportTicketView(ticket, replies)
	return &view, nil
}

// supportTicketViews 批量拼装列表视图；replies 为空时列表位置仍是空数组。
func (s *Service) supportTicketViews(tickets []SupportTicket, replies []SupportTicketReply) ([]SupportTicketView, error) {
	ids := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		ids = append(ids, ticket.UserID)
	}
	users, err := s.supportUsers(ids)
	if err != nil {
		return nil, err
	}
	views := make([]SupportTicketView, 0, len(tickets))
	for _, ticket := range tickets {
		views = append(views, supportTicketViewOf(ticket, replies, users))
	}
	return views, nil
}

// supportTicketView 拼装单条视图，并按需补上账号与回复作者的展示名。
func (s *Service) supportTicketView(ticket SupportTicket, replies []SupportTicketReply) SupportTicketView {
	ids := make([]string, 0, len(replies)+1)
	ids = append(ids, ticket.UserID)
	for _, reply := range replies {
		ids = append(ids, reply.AuthorID)
	}
	users, err := s.supportUsers(ids)
	if err != nil {
		// 账号查询失败不该让一次已经成功的写操作变成错误：视图里姓名留空，
		// 用户/客服仍能看到工单本身。
		users = map[string]*User{}
	}
	return supportTicketViewOf(ticket, replies, users)
}

// supportUsers 批量读取账号，返回 id -> User 的映射。
func (s *Service) supportUsers(ids []string) (map[string]*User, error) {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	users, err := s.store.UsersByIDs(unique)
	if err != nil {
		return nil, err
	}
	index := make(map[string]*User, len(users))
	for i := range users {
		index[users[i].ID] = &users[i]
	}
	return index, nil
}

func supportTicketViewOf(ticket SupportTicket, replies []SupportTicketReply, users map[string]*User) SupportTicketView {
	view := SupportTicketView{
		ID:         ticket.ID,
		TicketNo:   ticket.TicketNo,
		UserID:     ticket.UserID,
		Contact:    ticket.Contact,
		Category:   ticket.Category,
		Title:      ticket.Title,
		Body:       ticket.Body,
		Status:     ticket.Status,
		AssigneeID: ticket.AssigneeID,
		CreatedAt:  ticket.CreatedAt,
		UpdatedAt:  ticket.UpdatedAt,
		ClosedAt:   ticket.ClosedAt,
		Replies:    make([]SupportTicketReplyView, 0, len(replies)),
	}
	if user := users[ticket.UserID]; user != nil {
		view.UserName = user.DisplayName()
		view.UserEmail = user.EmailValue()
		view.UserPhone = user.PhoneValue()
	}
	for _, reply := range replies {
		authorName := ""
		if user := users[reply.AuthorID]; user != nil {
			authorName = user.DisplayName()
		}
		view.Replies = append(view.Replies, SupportTicketReplyView{
			ID:         reply.ID,
			AuthorID:   reply.AuthorID,
			AuthorRole: reply.AuthorRole,
			AuthorName: authorName,
			Body:       reply.Body,
			CreatedAt:  reply.CreatedAt,
		})
	}
	return view
}

// normalizeSupportCategory 归一分类并校验取值。
func normalizeSupportCategory(raw string) (string, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	switch value {
	case SupportCategoryBug, SupportCategoryBilling, SupportCategoryFeature, SupportCategoryOther:
		return value, nil
	default:
		return "", invalidArgument("请选择有效的工单分类")
	}
}

// validateSupportText 校验必填文本的字数边界。
func validateSupportText(value string, label string, minRunes int, maxRunes int) (string, error) {
	trimmed := strings.TrimSpace(value)
	runes := len([]rune(trimmed))
	if runes < minRunes {
		return "", invalidArgument(label + "不能为空")
	}
	if runes > maxRunes {
		return "", invalidArgument(fmt.Sprintf("%s不能超过 %d 个字", label, maxRunes))
	}
	return trimmed, nil
}

// validateSupportContact 校验可选的联系方式。
func validateSupportContact(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len([]rune(trimmed)) > supportTicketContactMaxRunes {
		return "", invalidArgument(fmt.Sprintf("联系方式不能超过 %d 个字", supportTicketContactMaxRunes))
	}
	return trimmed, nil
}
