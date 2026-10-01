package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// 个人资料域：昵称与头像。
//
// 这两个字段构成账号对外可见的全部人设，改动频繁且没有连带影响（不像换邮箱要验码、
// 改密码要吊销会话），因此单独成域：一次校验、一条 UPDATE，不牵扯身份与会话。

const (
	// profileNameMaxLength 取 30 而不是列宽 100：超过 30 个字的昵称在侧栏、评论和
	// 广场卡片里只能截断显示，允许用户填一个永远显示不全的名字，是在请他后面再来改一次。
	profileNameMaxLength = 30
	// profileAvatarMaxLength 按 512 截断：头像存的是资源地址，不是图片本体。
	profileAvatarMaxLength = 512
)

// ProfileView 是用户中心「个人资料」的读模型。
//
// 只回昵称、头像和两个联系方式：角色、状态、注册时间这些在只读的账户总览里已经有了，
// 在两个接口里各回一份，迟早会出现两处读数不一致。
type ProfileView struct {
	UserID      string `json:"userId"`
	Name        string `json:"name"`
	AvatarURL   string `json:"avatarUrl"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	HasPassword bool   `json:"hasPassword"`
}

// ProfileUpdate 是一次资料修改。
type ProfileUpdate struct {
	UserID    string
	Name      string
	AvatarURL string
}

// UpdateUserProfile 写入昵称与头像。
//
// 头像走 optionalString：空串落成 NULL 而不是 ""，与 app_users 上其他可空列一致，
// 也避免前端拿到 "" 与 null 两种"没有头像"的表示。
func (s *Store) UpdateUserProfile(userID string, name string, avatarURL string, at time.Time) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("auth: 更新资料缺少用户标识")
	}
	return s.db.Model(&User{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"name":       name,
			"avatar_url": optionalString(avatarURL),
			"updated_at": at,
		}).Error
}

// Profile 读取当前账号的资料。
func (s *Service) Profile(userID string) (*ProfileView, error) {
	user, err := s.accountForSelfService(userID)
	if err != nil {
		return nil, err
	}
	return profileViewOf(user), nil
}

// UpdateProfile 校验并写入昵称与头像。
func (s *Service) UpdateProfile(in ProfileUpdate) (*ProfileView, error) {
	user, err := s.accountForSelfService(in.UserID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, invalidArgument("昵称不能为空")
	}
	if length := len([]rune(name)); length > profileNameMaxLength {
		return nil, invalidArgument(fmt.Sprintf("昵称最多 %d 个字", profileNameMaxLength))
	}
	avatar, err := normalizeAvatarURL(in.AvatarURL)
	if err != nil {
		return nil, err
	}
	if err := s.store.UpdateUserProfile(user.ID, name, avatar, s.now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, unauthorized("当前未登录或登录已失效")
		}
		return nil, internalFailure(err)
	}
	// 回读一次而不是就地拼视图：更新可能被后续的并发写入覆盖，回读到的才是真实落库值，
	// 前端拿它直接刷新表单，不会出现"界面显示改了、库里其实没改"的分叉。
	updated, err := s.store.UserByID(user.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, unauthorized("当前未登录或登录已失效")
		}
		return nil, internalFailure(err)
	}
	return profileViewOf(updated), nil
}

// normalizeAvatarURL 校验头像地址，空串表示清除头像。
//
// 只放行 http/https：这个字段最终会进 <img src>，javascript: 与 data: 在这里没有任何
// 正当用途，却能把一个资料字段升级成脚本注入点。
func normalizeAvatarURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if length := len([]rune(trimmed)); length > profileAvatarMaxLength {
		return "", invalidArgument("头像地址过长")
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return "", invalidArgument("头像地址不能包含空格")
	}
	lowered := strings.ToLower(trimmed)
	if !strings.HasPrefix(lowered, "http://") && !strings.HasPrefix(lowered, "https://") {
		return "", invalidArgument("头像地址必须是 http 或 https 链接")
	}
	return trimmed, nil
}

func profileViewOf(user *User) *ProfileView {
	if user == nil {
		return &ProfileView{}
	}
	return &ProfileView{
		UserID:      user.ID,
		Name:        displayNameOf(user),
		AvatarURL:   avatarURLOf(user),
		Email:       emailOf(user),
		Phone:       phoneOf(user),
		HasPassword: hasPasswordSet(user),
	}
}

func avatarURLOf(user *User) string {
	if user == nil || user.AvatarURL == nil {
		return ""
	}
	return strings.TrimSpace(*user.AvatarURL)
}
