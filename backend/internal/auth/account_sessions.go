package auth

import (
	"strings"
	"time"
)

// 登录设备域：列出当前账号的活跃会话，并允许远程下线。
//
// app_sessions 里 ip_address / user_agent / last_active_at / revoked_at 四个字段一直
// 都在，缺的只是出口。这一域不做任何策略判断（不封 IP、不限制设备数），只回答两件事：
// 「我的账号现在在哪几台设备上登着」、「把我不认识的那台踢下去」。
//
// 为什么这条比改密码还紧要：账号被盗时，改密码只影响"下次登录"，攻击者手里的会话
// 仍然有效；只有下线会话才能真正把他请出去。

// sessionListLimit 是单次返回的会话条数上限。
//
// 不翻页：真实用户手上不会有几十台活跃设备，超过这个数通常意味着会话表被刷了，
// 此时给用户看 100 条和看 20 条没有区别，但多出来的行会明显拖慢这一页。
const sessionListLimit = 100

// SessionView 是「登录设备」列表里的一项。
type SessionView struct {
	ID         string `json:"id"`
	Current    bool   `json:"current"`
	MethodType string `json:"methodType"`
	// Identifier 是登录时用的标识快照（脱敏后的邮箱/手机号）。
	Identifier string `json:"identifier"`
	IPAddress  string `json:"ipAddress"`
	UserAgent  string `json:"userAgent"`
	// Device 是从 UA 归纳出的可读设备描述，认不出来时回落到"未知设备"。
	Device       string     `json:"device"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastActiveAt *time.Time `json:"lastActiveAt,omitempty"`
	ExpiresAt    time.Time  `json:"expiresAt"`
}

// ActiveSessionsByUser 取该账号仍然有效的会话，最近活跃的排在前面。
func (s *Store) ActiveSessionsByUser(userID string, now time.Time, limit int) ([]Session, error) {
	if limit <= 0 || limit > sessionListLimit {
		limit = sessionListLimit
	}
	var sessions []Session
	err := s.db.
		Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", strings.TrimSpace(userID), now).
		Order("last_active_at desc, created_at desc").
		Limit(limit).
		Find(&sessions).Error
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// RevokeSessionForUser 下线一条属于该账号的会话。
//
// 条件里必须带 user_id：会话 ID 是随机的，但"随机"不是授权，会话列表里的 ID 会出现在
// 前端与日志中，少一个归属条件就变成"拿到任意一个会话 ID 即可踢人下线"。
func (s *Store) RevokeSessionForUser(userID string, sessionID string, at time.Time) (bool, error) {
	result := s.db.Model(&Session{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", strings.TrimSpace(sessionID), strings.TrimSpace(userID)).
		Update("revoked_at", at)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// RevokeOtherSessions 下线该账号除当前设备外的全部会话，返回实际影响行数。
//
// keepTokenHash 为空时退化成"全部下线"：调用方（改密、换绑）宁可让用户重新登录一次，
// 也不该因为拿不到当前会话标识就放弃吊销。
func (s *Store) RevokeOtherSessions(userID string, keepTokenHash string, at time.Time) (int64, error) {
	query := s.db.Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL", strings.TrimSpace(userID))
	if trimmed := strings.TrimSpace(keepTokenHash); trimmed != "" {
		query = query.Where("token_hash <> ?", trimmed)
	}
	result := query.Update("revoked_at", at)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// ListSessions 返回当前账号的登录设备列表。
func (s *Service) ListSessions(userID string, currentTokenHash string) ([]SessionView, error) {
	user, err := s.accountForSelfService(userID)
	if err != nil {
		return nil, err
	}
	return s.sessionViewsFor(user.ID, currentTokenHash)
}

// RevokeSession 下线一条会话并回读最新列表。
//
// 回读而不是让前端自己从数组里删一项：并发场景下（同一个账号在两台设备上同时操作）
// 前端手里的列表可能已经过期，回读到的才是真实剩余会话。
func (s *Service) RevokeSession(userID string, sessionID string, currentTokenHash string) ([]SessionView, error) {
	user, err := s.accountForSelfService(userID)
	if err != nil {
		return nil, err
	}
	revoked, err := s.store.RevokeSessionForUser(user.ID, sessionID, s.now())
	if err != nil {
		return nil, internalFailure(err)
	}
	if !revoked {
		return nil, notFound("该设备已不在登录状态")
	}
	return s.sessionViewsFor(user.ID, currentTokenHash)
}

// RevokeOtherSessions 下线其余全部设备并回读最新列表。
func (s *Service) RevokeOtherSessions(userID string, currentTokenHash string) ([]SessionView, int64, error) {
	user, err := s.accountForSelfService(userID)
	if err != nil {
		return nil, 0, err
	}
	count, err := s.store.RevokeOtherSessions(user.ID, currentTokenHash, s.now())
	if err != nil {
		return nil, 0, internalFailure(err)
	}
	views, err := s.sessionViewsFor(user.ID, currentTokenHash)
	if err != nil {
		return nil, 0, err
	}
	return views, count, nil
}

func (s *Service) sessionViewsFor(userID string, currentTokenHash string) ([]SessionView, error) {
	sessions, err := s.store.ActiveSessionsByUser(userID, s.now(), sessionListLimit)
	if err != nil {
		return nil, internalFailure(err)
	}
	keep := strings.TrimSpace(currentTokenHash)
	views := make([]SessionView, 0, len(sessions))
	for index := range sessions {
		views = append(views, sessionViewOf(&sessions[index], keep))
	}
	return views, nil
}

func sessionViewOf(session *Session, currentTokenHash string) SessionView {
	if session == nil {
		return SessionView{}
	}
	view := SessionView{
		ID:           session.ID,
		Current:      currentTokenHash != "" && session.TokenHash == currentTokenHash,
		MethodType:   string(session.AuthMethodType),
		IPAddress:    stringOrEmpty(session.IPAddress),
		UserAgent:    stringOrEmpty(session.UserAgent),
		Device:       describeUserAgent(stringOrEmpty(session.UserAgent)),
		CreatedAt:    session.CreatedAt,
		LastActiveAt: session.LastActiveAt,
		ExpiresAt:    session.ExpiresAt,
	}
	view.Identifier = maskIdentifier(stringOrEmpty(session.IdentifierSnapshot))
	return view
}

// describeUserAgent 从 UA 里归纳出一句人话。
//
// 只做关键词匹配，不引入 UA 解析库：这里的用途是让用户能认出"这台是不是我"，
// 认出「Chrome · macOS」就够了，再精确的版本号既不增加辨识度，又会随浏览器更新
// 变成一个需要维护的映射表。认不出来时给"未知设备"，不猜。
func describeUserAgent(userAgent string) string {
	if strings.TrimSpace(userAgent) == "" {
		return "未知设备"
	}
	browser := ""
	switch {
	case strings.Contains(userAgent, "Edg/"):
		browser = "Edge"
	case strings.Contains(userAgent, "OPR/"), strings.Contains(userAgent, "Opera"):
		browser = "Opera"
	case strings.Contains(userAgent, "Chrome/"), strings.Contains(userAgent, "CriOS"):
		browser = "Chrome"
	case strings.Contains(userAgent, "Firefox/"), strings.Contains(userAgent, "FxiOS"):
		browser = "Firefox"
	case strings.Contains(userAgent, "Safari/"):
		browser = "Safari"
	}
	system := ""
	switch {
	case strings.Contains(userAgent, "iPhone"):
		system = "iPhone"
	case strings.Contains(userAgent, "iPad"):
		system = "iPad"
	case strings.Contains(userAgent, "Android"):
		system = "Android"
	case strings.Contains(userAgent, "Mac OS X"), strings.Contains(userAgent, "Macintosh"):
		system = "macOS"
	case strings.Contains(userAgent, "Windows"):
		system = "Windows"
	case strings.Contains(userAgent, "Linux"):
		system = "Linux"
	}
	switch {
	case browser != "" && system != "":
		return browser + " · " + system
	case browser != "":
		return browser
	case system != "":
		return system
	default:
		return "未知设备"
	}
}

// maskIdentifier 给登录标识脱敏。
//
// 会话列表会长期停留在用户自己的浏览器里，也常常被截图发给客服；完整的邮箱和手机号
// 在这里没有用处——用户认得自己的账号，却不需要在设备列表里再复述一遍。
func maskIdentifier(identifier string) string {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		return ""
	}
	if at := strings.Index(trimmed, "@"); at > 0 {
		local := trimmed[:at]
		domain := trimmed[at:]
		if len(local) <= 2 {
			return string(local[0:1]) + "***" + domain
		}
		return local[:2] + "***" + domain
	}
	if len(trimmed) >= 7 {
		return trimmed[:3] + "****" + trimmed[len(trimmed)-4:]
	}
	return trimmed
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
