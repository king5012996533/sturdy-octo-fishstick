package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// 密码长度下限取 8 而不是 6：6 位纯数字在本地 GPU 上属于秒破，而用户为 6 位密码
// 付出的记忆成本并不比 8 位低。
const (
	passwordMinLength = 8
	passwordMaxLength = 64
)

// passwordCredentialMessage 是密码验证失败时唯一对外的文案。
//
// 账号不存在、账号被停用、密码错误三种情况共用这一句：任何区分都会把「这个手机号
// 注册过没有」变成一次请求就能问出来的事，而那是撞库最有价值的一条信息。
const passwordCredentialMessage = "邮箱/手机号或密码错误"

// passwordTargetKind 描述密码通道的标识形态。
type passwordTargetKind int

const (
	targetUnknown passwordTargetKind = iota
	targetEmail
	targetPhone
)

func (k passwordTargetKind) label() string {
	switch k {
	case targetEmail:
		return "邮箱"
	case targetPhone:
		return "手机号"
	default:
		return "账号"
	}
}

// classifyPasswordTarget 判断标识是邮箱还是手机号，并返回归一后的值。
//
// 归一必须在这里做：同一个人用「+86 138-0013-8000」和「13800138000」登录必须命中
// 同一个账号，用法上大小写的邮箱同理。归一规则与验证码通道保持一致。
func classifyPasswordTarget(value string) (passwordTargetKind, string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return targetUnknown, "", invalidArgument("请输入邮箱或手机号")
	}
	if phone := normalizePhone(raw); mainlandPhonePattern.MatchString(phone) {
		return targetPhone, phone, nil
	}
	if email := strings.ToLower(raw); validEmail(email) {
		return targetEmail, email, nil
	}
	if strings.Contains(raw, "@") {
		return targetUnknown, "", invalidArgument("请输入正确的邮箱地址")
	}
	return targetUnknown, "", invalidArgument("请输入正确的手机号")
}

// validatePassword 校验密码强度。
//
// 只要求长度和「字母 + 数字」，不强制大小写与符号：后者在实测里主要制造找回率，
// 而不是安全性。真正的防线是上面的失败节流。
func validatePassword(password string) error {
	if strings.ContainsAny(password, " \t\r\n") {
		return invalidArgument("密码不能包含空格")
	}
	length := len([]rune(password))
	if length < passwordMinLength || length > passwordMaxLength {
		return invalidArgument(fmt.Sprintf("请输入 %d-%d 位密码", passwordMinLength, passwordMaxLength))
	}
	var hasLetter, hasDigit bool
	for _, char := range password {
		switch {
		case char >= '0' && char <= '9':
			hasDigit = true
		case (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z'):
			hasLetter = true
		}
	}
	if !hasLetter || !hasDigit {
		return invalidArgument("密码需同时包含字母和数字")
	}
	return nil
}

// passwordStrategy 实现普通用户的邮箱/手机号 + 密码通道。
//
// 与验证码通道的关键差别在注册：这里不要求先证明标识归属，因为「能收到验证码」和
// 「会不会忘记密码」是两件事，强制验证码会让短信通道审批没下来时整个注册入口不可用。
// 代价是注册不校验标识真实存在，因此这条通道的滥用防线是失败节流 + 后续的风控，
// 不是验证码。允许注册与否由后台的 allow_sign_up 控制。
func (s *Service) passwordStrategy() *Strategy {
	return &Strategy{
		MethodType: MethodPassword,
		Category:   CategoryPassword,
		Login: func(ctx context.Context, in LoginInput) (*LoginOutput, error) {
			_, identifier, err := classifyPasswordTarget(in.Target)
			if err != nil {
				return nil, err
			}
			keys := passwordThrottleKeys(identifier, in.RequesterIP)
			if err := s.passwords.check(keys...); err != nil {
				return nil, err
			}
			user, err := s.passwordUser(identifier)
			if errors.Is(err, ErrNotFound) {
				// 即使账号不存在也要记一次失败：否则「不存在的账号」可以无限试探，
				// 而它恰好是最容易被用来枚举的那一类请求。
				s.passwords.fail(keys...)
				return nil, unauthorized(passwordCredentialMessage)
			}
			if err != nil {
				return nil, internalFailure(err)
			}
			if user.Status != StatusActive || !VerifyPassword(in.Password, user.PasswordHash) {
				s.passwords.fail(keys...)
				return nil, unauthorized(passwordCredentialMessage)
			}
			s.passwords.reset(keys...)
			return s.issueSession(user, MethodPassword, identifier, in.RequesterIP, in.UserAgent)
		},
		Register: func(ctx context.Context, in RegisterInput) (*LoginOutput, error) {
			kind, identifier, err := classifyPasswordTarget(in.Target)
			if err != nil {
				return nil, err
			}
			if err := validatePassword(in.Password); err != nil {
				return nil, err
			}
			// 先判重再写库：email/phone 列都有唯一索引，让插入失败再翻译成 409 会
			// 把「已注册」和「并发写入」混成同一个错误。
			if _, err := s.passwordUser(identifier); err == nil {
				return nil, conflict(fmt.Sprintf("该%s已注册，请直接登录；忘记密码可先用验证码登录", kind.label()))
			} else if !errors.Is(err, ErrNotFound) {
				return nil, internalFailure(err)
			}
			hash, err := HashPassword(in.Password)
			if err != nil {
				return nil, internalFailure(err)
			}
			user, err := s.createUserWithIdentityCredential(ctx, newUserCredential{
				MethodType:   MethodPassword,
				Identifier:   identifier,
				PasswordHash: hash,
			})
			if err != nil {
				return nil, err
			}
			if err := s.recordAgreements(user.ID, in.AgreementVersion, in.RequesterIP, in.UserAgent); err != nil {
				return nil, internalFailure(err)
			}
			return s.issueSession(user, MethodPassword, identifier, in.RequesterIP, in.UserAgent)
		},
	}
}

// passwordThrottleKeys 生成失败计数键。
//
// 同时按标识和来源 IP 计数：只按标识计数会被分布式撞库打穿（每个 IP 只试一次），
// 只按 IP 计数又会让同一个 NAT 后的正常用户互相拖累。
func passwordThrottleKeys(identifier string, requesterIP string) []string {
	return []string{"password-id:" + identifier, "password-ip:" + strings.TrimSpace(requesterIP)}
}

// passwordUser 按标识只读查找账号。
//
// 刻意不复用 resolveExistingUser：那个函数在找不到身份绑定时会顺手补写一条 identity
// 记录。登录是未认证入口，让它在密码验证之前写库，等于给任何人一个可以无限触发的
// 写放大入口；这里只读，绑定关系由注册入口负责建立。
func (s *Service) passwordUser(identifier string) (*User, error) {
	if identity, err := s.store.IdentityByIdentifier(MethodPassword, identifier); err == nil {
		user, userErr := s.store.UserByID(identity.UserID)
		if userErr == nil {
			return user, nil
		}
		if !errors.Is(userErr, ErrNotFound) {
			return nil, userErr
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// 没有密码身份绑定的存量账号（历史数据只有邮箱/手机号列）回落到按列查找，
	// 否则验证码注册过的老用户永远设不上密码。
	if user, err := s.store.UserByEmail(identifier); err == nil {
		return user, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.store.UserByPhone(identifier)
}
