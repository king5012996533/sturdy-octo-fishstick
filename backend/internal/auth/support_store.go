package auth

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 工单域的读写层。
//
// 只做「查 / 写 + 事务边界」，不含状态机与校验：已关闭能不能回复、状态怎么流转
// 属于服务层判断，写在这里会让用户端与后台各绕开一遍。

// CreateSupportTicket 落一条工单。
//
// ticket_no 撞唯一索引时返回 ok=false 而不是错误：撞号是可预期的（随机尾号 + 全局
// 唯一），由服务层换一个号重试，而不是把一次本可成功的提交变成 500。
func (s *Store) CreateSupportTicket(ticket *SupportTicket) (bool, error) {
	if ticket == nil {
		return false, errors.New("auth: 工单为空")
	}
	now := s.clock()
	if ticket.ID == "" {
		ticket.ID = newSupportID()
	}
	if ticket.CreatedAt.IsZero() {
		ticket.CreatedAt = now
	}
	ticket.UpdatedAt = now
	result := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "ticket_no"}},
		DoNothing: true,
	}).Create(ticket)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// SupportTicketByID 按主键读取工单。
func (s *Store) SupportTicketByID(id string) (*SupportTicket, error) {
	var ticket SupportTicket
	err := s.db.Where("id = ?", strings.TrimSpace(id)).First(&ticket).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &ticket, nil
}

// SupportTicketsByUser 分页返回某个账号自己的工单。
//
// 按 updated_at 倒序：刚被回复过的工单应当浮到最上面，用户不必翻页去找客服的最新答复。
func (s *Store) SupportTicketsByUser(userID string, page int, pageSize int) ([]SupportTicket, int64, error) {
	page, pageSize = normalizeSupportPage(page, pageSize)
	query := s.db.Model(&SupportTicket{}).Where("user_id = ?", strings.TrimSpace(userID))
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var tickets []SupportTicket
	if err := query.Order("updated_at DESC, id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).Find(&tickets).Error; err != nil {
		return nil, 0, err
	}
	return tickets, total, nil
}

// AdminSupportTickets 分页返回后台工单列表。
func (s *Store) AdminSupportTickets(filter SupportTicketFilter) ([]SupportTicket, int64, error) {
	page, pageSize := normalizeSupportPage(filter.Page, filter.PageSize)
	query := s.adminSupportTicketQuery(filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var tickets []SupportTicket
	// 显式选 support_tickets.* ：命中关键字时会 LEFT JOIN app_users，两表都有
	// id/created_at/updated_at，用 * 会取到账号表的列并扫描进错误的字段。
	if err := query.Select("support_tickets.*").
		Order("support_tickets.updated_at DESC, support_tickets.id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).Find(&tickets).Error; err != nil {
		return nil, 0, err
	}
	return tickets, total, nil
}

func (s *Store) adminSupportTicketQuery(filter SupportTicketFilter) *gorm.DB {
	query := s.db.Model(&SupportTicket{})
	if status := strings.ToUpper(strings.TrimSpace(filter.Status)); status != "" {
		query = query.Where("support_tickets.status = ?", status)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Joins("LEFT JOIN app_users AS users ON users.id = support_tickets.user_id").
			Where("support_tickets.ticket_no LIKE ? OR support_tickets.title LIKE ? OR users.email LIKE ? OR users.phone LIKE ?",
				like, like, like, like)
	}
	return query
}

// SupportTicketStatusCounts 汇总各状态工单数，供后台指标卡使用。
func (s *Store) SupportTicketStatusCounts() (SupportTicketCounts, error) {
	var rows []struct {
		Status string `gorm:"column:status"`
		Count  int64  `gorm:"column:count"`
	}
	if err := s.db.Model(&SupportTicket{}).
		Select("status, count(*) AS count").Group("status").Scan(&rows).Error; err != nil {
		return SupportTicketCounts{}, err
	}
	var counts SupportTicketCounts
	for _, row := range rows {
		counts.Total += row.Count
		switch strings.ToUpper(strings.TrimSpace(row.Status)) {
		case SupportStatusOpen:
			counts.Open += row.Count
		case SupportStatusProcessing:
			counts.Processing += row.Count
		case SupportStatusResolved:
			counts.Resolved += row.Count
		case SupportStatusClosed:
			counts.Closed += row.Count
		}
	}
	return counts, nil
}

// SupportTicketReplies 返回一条工单的全部回复，按时间正序。
func (s *Store) SupportTicketReplies(ticketID string) ([]SupportTicketReply, error) {
	var replies []SupportTicketReply
	if err := s.db.Where("ticket_id = ?", strings.TrimSpace(ticketID)).
		Order("created_at ASC, id ASC").Find(&replies).Error; err != nil {
		return nil, err
	}
	return replies, nil
}

// CreateSupportTicketReply 追加一条回复，并顺带刷新工单的 updated_at。
//
// 两次写在同一个事务里：回复落库却没有把工单推到列表前面，用户会在"最新的答复"
// 下面看到一条更早的工单，误以为客服没有回。
func (s *Store) CreateSupportTicketReply(reply *SupportTicketReply) error {
	if reply == nil {
		return errors.New("auth: 工单回复为空")
	}
	now := s.clock()
	if reply.ID == "" {
		reply.ID = newSupportID()
	}
	if reply.CreatedAt.IsZero() {
		reply.CreatedAt = now
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(reply).Error; err != nil {
			return err
		}
		return tx.Model(&SupportTicket{}).
			Where("id = ?", reply.TicketID).
			Update("updated_at", now).Error
	})
}

// UpdateSupportTicketStatus 更新工单状态与关闭时间。
//
// 用 map 而不是结构体更新：closed_at 要能被显式写回 NULL（重新打开工单），而
// Updates 用结构体会忽略零值字段，关掉的工单就再也打不开了。
func (s *Store) UpdateSupportTicketStatus(id string, status string, closedAt *time.Time, updatedAt time.Time) error {
	return s.db.Model(&SupportTicket{}).
		Where("id = ?", strings.TrimSpace(id)).
		Updates(map[string]any{
			"status":     status,
			"closed_at":  closedAt,
			"updated_at": updatedAt,
		}).Error
}

// normalizeSupportPage 与计费域的分页口径保持一致（默认 20，上限 200）。
func normalizeSupportPage(page int, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	return page, pageSize
}
