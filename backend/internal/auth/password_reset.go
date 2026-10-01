package auth

import (
	"context"
	"errors"
	"strings"
)

// 忘记密码：不依赖旧密码、也不依赖会话的密码重置。
//
// 它和另外两条密码路径的区别就在证明方式：设置密码与修改密码都假定用户要么在会话里、
// 要么手里有旧密码，而"忘记密码"这条路上两样都可能没有，唯一还能用的证明是
// 「能收到发到该标识上的验证码」。这个前提决定了三条必须守住的规则：
//
//  1. 只认邮箱验证码与短信验证码。任何以密码为载体的通道在这里都等于把"忘记密码"
//     变成"绕过密码"，而绕过密码的密码重置不叫重置。
//  2. 验证码走独立场景（passwordResetScene），免得用户刚在登录页点过发送、转头
//     点忘记密码就被同一分钟的冷却挡住。
//  3. 重置成功后吊销该账号的全部会话。换完密码而旧会话继续有效，正是账号被盗之后
//     攻击者最想要的结果；发起重置的那台设备如果本身在会话里，保留它。
//
// 与登录一致，标识不存在时如实告知（登录接口已经在做同一件事），而不是回一句
// "已发送"让用户对着一个永远收不到的验证码等下去。

// RequestPasswordResetCodeInput 是重置验证码的下发入参。
type RequestPasswordResetCodeInput struct {
	MethodType  MethodType
	Target      string
	RequesterIP string
	UserAgent   string
}

// ResetPasswordInput 是重置密码的入参。
type ResetPasswordInput struct {
	MethodType  MethodType
	Target      string
	Code        string
	NewPassword string
	// CurrentTokenHash 是发起重置的会话摘要，登录页上没有这个值（空串即全部吊销）。
	// 非空且属于该账号时保留它——用户是在用户中心里"忘了当前密码"，把他也一起
	// 踢下线，只会让他在同一次操作里被登出两次。
	CurrentTokenHash string
}

// SendPasswordResetCode 给一个已注册的邮箱或手机号下发重置验证码。
func (s *Service) SendPasswordResetCode(ctx context.Context, in RequestPasswordResetCodeInput) (*SendCodeOutput, error) {
	methodType, target, user, err := s.passwordResetTarget(in.MethodType, in.Target)
	if err != nil {
		return nil, err
	}
	if _, err := s.resetTargetAccount(user); err != nil {
		return nil, err
	}
	return s.SendCode(ctx, SendCodeInput{
		MethodType:  methodType,
		Target:      target,
		RequesterIP: in.RequesterIP,
		UserAgent:   in.UserAgent,
		Scene:       passwordResetScene,
	})
}

// ResetPassword 校验验证码后写入新密码。
func (s *Service) ResetPassword(in ResetPasswordInput) (*PasswordUpdateResult, error) {
	methodType, target, user, err := s.passwordResetTarget(in.MethodType, in.Target)
	if err != nil {
		return nil, err
	}
	if _, err := s.resetTargetAccount(user); err != nil {
		return nil, err
	}
	// 6 位验证码在 5 分钟窗口里的猜测空间比密码小得多，但仍然值得节流：这条路径
	// 一旦被猜中就是完整的账号接管，而验证码本身没有"错误次数"的概念。
	// 键按标识走，因为此时还没有会话可依赖。
	keys := []string{"password-reset:" + string(methodType) + ":" + target}
	if err := s.passwords.check(keys...); err != nil {
		return nil, err
	}
	code := strings.TrimSpace(in.Code)
	if code == "" {
		return nil, invalidArgument("请输入验证码")
	}
	record, err := s.store.ConsumeCode(methodType, target, code, passwordResetScene)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.passwords.fail(keys...)
			return nil, invalidArgument("验证码不正确或已过期")
		}
		return nil, internalFailure(err)
	}
	s.passwords.reset(keys...)
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
	// 把验证码记录关联到账号只用于审计：这条验证码是账号级操作的凭证，
	// 出问题时要能从码反查到是谁的账号。关联失败不阻断重置。
	if err := s.store.AttachCodeUser(record.ID, user.ID); err != nil {
		return nil, internalFailure(err)
	}
	revoked, err := s.revokeOtherSessionsFor(user.ID, in.CurrentTokenHash)
	if err != nil {
		return nil, err
	}
	state := passwordStateView(true)
	return &PasswordUpdateResult{State: state, RevokedSessions: revoked}, nil
}

// passwordResetTarget 把一个标识归一成可发码的目标，并确认它属于一个还能用的账号。
//
// 归一规则与登录完全一致（邮箱转小写、手机号去分隔符与 +86），否则同一个号在两条
// 路径上会落到两条不同的验证码记录里，用户在登录页收到的码在重置页永远用不了。
func (s *Service) passwordResetTarget(methodType MethodType, rawTarget string) (MethodType, string, *User, error) {
	switch methodType {
	case MethodEmailCode:
		email := strings.ToLower(strings.TrimSpace(rawTarget))
		if !validEmail(email) {
			return "", "", nil, invalidArgument("请输入正确的邮箱地址")
		}
		user, err := s.store.UserByEmail(email)
		if err != nil {
			return "", "", nil, passwordResetLookupError(err, "该邮箱尚未注册，请先注册")
		}
		return MethodEmailCode, email, user, nil
	case MethodPhoneCode:
		phone := normalizePhone(rawTarget)
		if !mainlandPhonePattern.MatchString(phone) {
			return "", "", nil, invalidArgument("请输入正确的手机号")
		}
		user, err := s.store.UserByPhone(phone)
		if err != nil {
			return "", "", nil, passwordResetLookupError(err, "该手机号尚未注册，请先注册")
		}
		return MethodPhoneCode, phone, user, nil
	default:
		return "", "", nil, invalidArgument("请选择邮箱或手机号接收验证码")
	}
}

func passwordResetLookupError(err error, missingMessage string) error {
	if errors.Is(err, ErrNotFound) {
		return notFound(missingMessage)
	}
	return internalFailure(err)
}

// resetTargetAccount 取出发码对象对应的账号，并确认它还能接收重置。
//
// 注销执行与后台封禁都会把状态改成 DISABLED，此时再允许重置密码等于给一个已经被
// 停用的账号留一条重新拿到凭据的路。已注销的账号在这里得到的是 403 而不是 404：
// 账号确实存在过，只是不再可用。
func (s *Service) resetTargetAccount(user *User) (*User, error) {
	if user == nil {
		return nil, unauthorized("当前未登录或登录已失效")
	}
	return s.accountForSelfService(user.ID)
}
