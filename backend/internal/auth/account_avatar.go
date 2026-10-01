package auth

import (
	"errors"
	"strings"
	"time"
)

// 头像域：把「上传好的图片」落到账号上。
//
// 与昵称分开的原因与昵称、头像彼此分开的原因相同：上传是一次独立的动作，成功即应
// 生效，不能挂在一个还没提交的「保存资料」表单上——用户传完图就去关页面，改的应该
// 已经生效。昵称仍然走 UpdateProfile，那条路径收的是文本。
//
// 本模块不知道图片存在哪里。上传与文件读写由调用方（托管层）负责，这里只处理
// 「账号现在用哪张头像」以及它的地址形态。

// UpdateUserAvatar 只写头像地址，不动昵称。
func (s *Store) UpdateUserAvatar(userID string, avatarURL string, at time.Time) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("auth: 更新头像缺少用户标识")
	}
	return s.db.Model(&User{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"avatar_url": optionalString(avatarURL),
			"updated_at": at,
		}).Error
}

// SetAvatarURL 写入头像地址并回读资料视图。
func (s *Service) SetAvatarURL(userID string, avatarURL string) (*ProfileView, error) {
	return s.updateAvatar(userID, avatarURL)
}

// ClearAvatar 清空头像地址，界面回落到昵称首字母。
func (s *Service) ClearAvatar(userID string) (*ProfileView, error) {
	return s.updateAvatar(userID, "")
}

// StoredAvatarURL 读出账号当前的头像地址。
//
// 刻意不复用 Profile：那条路径按「自助操作」语义把停用账号判成未登录，而头像是个
// 公开读——账号被停用不该让它在历史页面、广场卡片里变成一排破图。这里也不区分
// 地址是平台托管的还是第三方外链，由调用方自己判断。
func (s *Service) StoredAvatarURL(userID string) (string, error) {
	if s == nil || s.store == nil {
		return "", internalFailure(errors.New("账号库未装配"))
	}
	user, err := s.store.UserByID(strings.TrimSpace(userID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", notFound("账号不存在")
		}
		return "", internalFailure(err)
	}
	return avatarURLOf(user), nil
}

func (s *Service) updateAvatar(userID string, avatarURL string) (*ProfileView, error) {
	user, err := s.accountForSelfService(userID)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeAvatarURL(avatarURL)
	if err != nil {
		return nil, err
	}
	if err := s.store.UpdateUserAvatar(user.ID, normalized, s.now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, unauthorized("当前未登录或登录已失效")
		}
		return nil, internalFailure(err)
	}
	// 回读而不是就地拼视图：并发写入（另一个页面同时改了昵称）之后，回读到的才是
	// 真实落库值。
	updated, err := s.store.UserByID(user.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, unauthorized("当前未登录或登录已失效")
		}
		return nil, internalFailure(err)
	}
	return profileViewOf(updated), nil
}
