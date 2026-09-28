package auth

import (
	"errors"
	"strings"
	"time"
)

// AdminUserCounts 是仪表盘的账号口径计数。
type AdminUserCounts struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	Disabled int64 `json:"disabled"`
	Admins   int64 `json:"admins"`
	NewUsers int64 `json:"newUsers"`
}

// AdminUserView 是管理端用户列表的一行。
//
// 只带运营需要的字段：密码哈希、验证码、会话令牌一律不出现在读模型里。
type AdminUserView struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	Phone        string     `json:"phone"`
	AvatarURL    string     `json:"avatarUrl"`
	Role         UserRole   `json:"role"`
	Status       UserStatus `json:"status"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastActiveAt *time.Time `json:"lastActiveAt,omitempty"`
}

// AdminUserPage 是分页后的账号列表。
type AdminUserPage struct {
	Users []AdminUserView `json:"users"`
	Total int64           `json:"total"`
	Page  int             `json:"page"`
	Limit int             `json:"pageSize"`
}

const (
	adminUserDefaultLimit = 20
	adminUserMaxLimit     = 100
)

func toAdminUserView(user User) AdminUserView {
	view := AdminUserView{
		ID:        user.ID,
		Role:      user.Role,
		Status:    user.Status,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
	if user.Username != nil {
		view.Username = *user.Username
	}
	if user.Name != nil {
		view.Name = *user.Name
	}
	if user.Email != nil {
		view.Email = *user.Email
	}
	if user.Phone != nil {
		view.Phone = *user.Phone
	}
	if user.AvatarURL != nil {
		view.AvatarURL = *user.AvatarURL
	}
	return view
}

// AdminUserCounts 汇总账号侧计数。
func (s *Service) AdminUserCounts(since time.Time) (*AdminUserCounts, error) {
	counts, err := s.store.AdminUserCounts(since)
	if err != nil {
		return nil, internalFailure(err)
	}
	return &counts, nil
}

// AdminListUsers 返回带筛选的分页账号列表。
func (s *Service) AdminListUsers(filter AdminUserFilter) (*AdminUserPage, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Limit <= 0 {
		filter.Limit = adminUserDefaultLimit
	}
	if filter.Limit > adminUserMaxLimit {
		filter.Limit = adminUserMaxLimit
	}
	users, total, err := s.store.AdminUserPage(filter)
	if err != nil {
		return nil, internalFailure(err)
	}
	ids := make([]string, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	// 最后活跃来自会话表：一次聚合查询，避免列表逐行触发 N+1。
	lastActive, err := s.store.LastActiveAtByUser(ids)
	if err != nil {
		return nil, internalFailure(err)
	}
	views := make([]AdminUserView, 0, len(users))
	for _, user := range users {
		view := toAdminUserView(user)
		if at, ok := lastActive[user.ID]; ok {
			at := at
			view.LastActiveAt = &at
		}
		views = append(views, view)
	}
	return &AdminUserPage{Users: views, Total: total, Page: filter.Page, Limit: filter.Limit}, nil
}

// AdminUserByID 返回单个账号的管理端读模型。
func (s *Service) AdminUserByID(userID string) (*AdminUserView, error) {
	user, err := s.store.UserByID(strings.TrimSpace(userID))
	if errors.Is(err, ErrNotFound) {
		return nil, notFound("账号不存在")
	}
	if err != nil {
		return nil, internalFailure(err)
	}
	view := toAdminUserView(*user)
	return &view, nil
}

// AdminSetUserStatus 封禁或解封账号。
//
// 封禁会同时吊销全部会话：只改状态虽然也会让下一次请求失败，但把会话留着等于
// 让被封的浏览器继续持有一个"看起来还有效"的 Cookie。
func (s *Service) AdminSetUserStatus(actorID string, userID string, status UserStatus) error {
	normalized := UserStatus(strings.ToUpper(strings.TrimSpace(string(status))))
	if normalized != StatusActive && normalized != StatusDisabled {
		return invalidArgument("账号状态只能是 ACTIVE 或 DISABLED")
	}
	target := strings.TrimSpace(userID)
	if target == "" {
		return invalidArgument("请选择要操作的账号")
	}
	if target == strings.TrimSpace(actorID) {
		return forbidden("不能修改自己的账号状态")
	}
	now := s.now()
	if err := s.store.UpdateUserStatus(target, normalized, now); err != nil {
		if errors.Is(err, ErrNotFound) {
			return notFound("账号不存在")
		}
		return internalFailure(err)
	}
	if normalized == StatusDisabled {
		if _, err := s.store.RevokeAllSessions(target, now); err != nil {
			return internalFailure(err)
		}
	}
	return nil
}

// AdminSetUserRole 授予或取消管理员。
func (s *Service) AdminSetUserRole(actorID string, userID string, role UserRole) error {
	normalized := UserRole(strings.ToUpper(strings.TrimSpace(string(role))))
	if normalized != RoleUser && normalized != RoleAdmin {
		return invalidArgument("角色只能是 USER 或 ADMIN")
	}
	target := strings.TrimSpace(userID)
	if target == "" {
		return invalidArgument("请选择要操作的账号")
	}
	if target == strings.TrimSpace(actorID) {
		return forbidden("不能修改自己的角色")
	}
	if err := s.store.UpdateUserRole(target, normalized, s.now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return notFound("账号不存在")
		}
		return internalFailure(err)
	}
	return nil
}

// AdminResetPassword 覆盖账号密码。
//
// 重置后强制下线：旧会话可能正是"忘记密码"要处理的那个风险点。
func (s *Service) AdminResetPassword(userID string, password string) error {
	target := strings.TrimSpace(userID)
	if target == "" {
		return invalidArgument("请选择要操作的账号")
	}
	if len(password) < 8 || len(password) > 64 {
		return invalidArgument("请输入 8-64 位新密码")
	}
	hashed, err := HashPassword(password)
	if err != nil {
		return internalFailure(err)
	}
	now := s.now()
	if err := s.store.UpdateUserPassword(target, hashed, now); err != nil {
		if errors.Is(err, ErrNotFound) {
			return notFound("账号不存在")
		}
		return internalFailure(err)
	}
	if _, err := s.store.RevokeAllSessions(target, now); err != nil {
		return internalFailure(err)
	}
	return nil
}

// AdminForceLogout 吊销账号的全部会话，返回踢掉的会话数。
func (s *Service) AdminForceLogout(userID string) (int64, error) {
	target := strings.TrimSpace(userID)
	if target == "" {
		return 0, invalidArgument("请选择要操作的账号")
	}
	if _, err := s.store.UserByID(target); err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, notFound("账号不存在")
		}
		return 0, internalFailure(err)
	}
	revoked, err := s.store.RevokeAllSessions(target, s.now())
	if err != nil {
		return 0, internalFailure(err)
	}
	return revoked, nil
}

// AccountAgreementView 是账户页展示的一条协议签署记录。
type AccountAgreementView struct {
	AgreementType string    `json:"agreementType"`
	Version       string    `json:"version"`
	AcceptedAt    time.Time `json:"acceptedAt"`
}

// AccountAgreements 返回当前账号的协议签署记录。
func (s *Service) AccountAgreements(userID string) ([]AccountAgreementView, error) {
	target := strings.TrimSpace(userID)
	if target == "" {
		return nil, unauthorized("请先登录")
	}
	records, err := s.store.UserAgreements(target, 20)
	if err != nil {
		return nil, internalFailure(err)
	}
	views := make([]AccountAgreementView, 0, len(records))
	for _, record := range records {
		views = append(views, AccountAgreementView{
			AgreementType: record.AgreementType, Version: record.Version, AcceptedAt: record.AcceptedAt,
		})
	}
	return views, nil
}

// AdminUsersByIDs 批量返回账号读模型，键为账号 ID。
//
// 列表页要显示"所有者"：画布、调用记录里只有 owner id，逐个查询会退化成 N+1。
func (s *Service) AdminUsersByIDs(ids []string) (map[string]AdminUserView, error) {
	users, err := s.store.UsersByIDs(ids)
	if err != nil {
		return nil, internalFailure(err)
	}
	result := make(map[string]AdminUserView, len(users))
	for _, user := range users {
		result[user.ID] = toAdminUserView(user)
	}
	return result, nil
}
