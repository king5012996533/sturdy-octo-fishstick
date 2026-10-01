package auth

import (
	"context"
	"strings"
)

// 密码域：设置（本来没有密码的账号）与修改（本来有密码的账号）。
//
// 两条路径的证明方式刻意不同：
//   - 设置密码用验证码，因为"有没有密码"本身就是账号的既有状态，不能被会话改变。
//     托管形态下大量账号是验证码注册的，密码为 NULL。允许仅凭一个会话就设密码，
//     等于把"有一次会话"升级成"永久凭据"——被盗会话的价值会从几天变成永远。
//   - 修改密码用旧密码，因为此时账号已经有一个只有本人知道的秘密，再要一次验证码
//     只是多一层打扰，并不能提供额外保证。
//
// 两条路径都会吊销"其他设备"的会话而保留发起操作的那一台：改密之后旧会话继续有效，
// 正是账号被盗后攻击者最想要的结果。

// PasswordStateView 是密码的当前状态。长度上下限回给前端，避免表单把规则写死在界面上
// 之后与服务端脱钩。
type PasswordStateView struct {
	HasPassword bool `json:"hasPassword"`
	MinLength   int  `json:"minLength"`
	MaxLength   int  `json:"maxLength"`
}

// PasswordUpdateResult 是一次改密的结果。
type PasswordUpdateResult struct {
	State PasswordStateView `json:"state"`
	// RevokedSessions 是本次顺带吊销的其他设备会话数，前台据此告诉用户"已从 N 台设备退出"。
	RevokedSessions int64 `json:"revokedSessions"`
}

// SetPasswordInput 是首次设置密码的入参。
type SetPasswordInput struct {
	UserID      string
	MethodType  MethodType
	Code        string
	NewPassword string
	// CurrentTokenHash 指向发起本次操作的会话：改密后保留它，吊销其余。
	CurrentTokenHash string
}

// ChangePasswordInput 是修改密码的入参。
type ChangePasswordInput struct {
	UserID           string
	CurrentPassword  string
	NewPassword      string
	CurrentTokenHash string
}

// PasswordState 返回当前账号是否已有密码。
func (s *Service) PasswordState(userID string) (*PasswordStateView, error) {
	user, err := s.accountForSelfService(userID)
	if err != nil {
		return nil, err
	}
	view := passwordStateView(hasPasswordSet(user))
	return &view, nil
}

// SetPassword 校验验证码后为账号设置首个密码。
func (s *Service) SetPassword(ctx context.Context, in SetPasswordInput) (*PasswordUpdateResult, error) {
	user, err := s.accountForSelfService(in.UserID)
	if err != nil {
		return nil, err
	}
	if hasPasswordSet(user) {
		return nil, conflict("该账号已经设置过密码，请改用「修改密码」")
	}
	target, err := accountVerificationTarget(user, in.MethodType)
	if err != nil {
		return nil, err
	}
	if err := s.consumeAccountCode(in.MethodType, target, in.Code); err != nil {
		return nil, err
	}
	if err := validatePassword(in.NewPassword); err != nil {
		return nil, err
	}
	hash, err := HashPassword(in.NewPassword)
	if err != nil {
		return nil, internalFailure(err)
	}
	if err := s.store.UpdateUserPassword(user.ID, hash, s.now()); err != nil {
		return nil, internalFailure(err)
	}
	revoked, err := s.revokeOtherSessionsFor(user.ID, in.CurrentTokenHash)
	if err != nil {
		return nil, err
	}
	state := passwordStateView(true)
	return &PasswordUpdateResult{State: state, RevokedSessions: revoked}, nil
}

// ChangePassword 校验旧密码后改密。
func (s *Service) ChangePassword(in ChangePasswordInput) (*PasswordUpdateResult, error) {
	user, err := s.accountForSelfService(in.UserID)
	if err != nil {
		return nil, err
	}
	if !hasPasswordSet(user) {
		return nil, conflict("该账号还没有密码，请先用验证码设置密码")
	}
	// 旧密码在这里同样是可被暴力猜测的固定串，节流复用登录那条通道的计数器。
	// 键按用户 ID 走而不是按邮箱/手机号：改密是已登录路径，账号身份已经确定，
	// 再按标识计数只会让"同一个人改完邮箱就重置了计数"。
	keys := []string{"user-password:" + user.ID}
	if err := s.passwords.check(keys...); err != nil {
		return nil, err
	}
	if !VerifyPassword(in.CurrentPassword, user.PasswordHash) {
		s.passwords.fail(keys...)
		return nil, unauthorized("当前密码不正确")
	}
	s.passwords.reset(keys...)

	if err := validatePassword(in.NewPassword); err != nil {
		return nil, err
	}
	if VerifyPassword(in.NewPassword, user.PasswordHash) {
		return nil, invalidArgument("新密码不能与当前密码相同")
	}
	hash, err := HashPassword(in.NewPassword)
	if err != nil {
		return nil, internalFailure(err)
	}
	if err := s.store.UpdateUserPassword(user.ID, hash, s.now()); err != nil {
		return nil, internalFailure(err)
	}
	revoked, err := s.revokeOtherSessionsFor(user.ID, in.CurrentTokenHash)
	if err != nil {
		return nil, err
	}
	state := passwordStateView(true)
	return &PasswordUpdateResult{State: state, RevokedSessions: revoked}, nil
}

// revokeOtherSessionsFor 吊销除当前设备外的全部会话。
//
// 当前会话的摘要可能为空（例如接口被非浏览器客户端调用），此时退回"全部吊销"——
// 宁可让用户重新登录一次，也不留下一台来路不明的设备继续持有凭据。
func (s *Service) revokeOtherSessionsFor(userID string, currentTokenHash string) (int64, error) {
	revoked, err := s.store.RevokeOtherSessions(strings.TrimSpace(userID), strings.TrimSpace(currentTokenHash), s.now())
	if err != nil {
		return 0, internalFailure(err)
	}
	return revoked, nil
}

func passwordStateView(hasPassword bool) PasswordStateView {
	return PasswordStateView{HasPassword: hasPassword, MinLength: passwordMinLength, MaxLength: passwordMaxLength}
}
