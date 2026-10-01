package auth

import (
	"context"
	"errors"
	"strings"
)

// EmailPattern 用于校验邮箱格式；与 CanvasMind 的校验规则保持等价。
func validEmail(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n") {
		return false
	}
	at := strings.Index(trimmed, "@")
	if at <= 0 || at == len(trimmed)-1 {
		return false
	}
	domain := trimmed[at+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}

// emailCodeStrategy 实现邮箱验证码登录。
func (s *Service) emailCodeStrategy() *Strategy {
	return &Strategy{
		MethodType: MethodEmailCode,
		Category:   CategoryCode,
		SendCode: func(ctx context.Context, in SendCodeInput) (*SendCodeOutput, error) {
			email := strings.ToLower(strings.TrimSpace(in.Target))
			if !validEmail(email) {
				return nil, invalidArgument("请输入正确的邮箱地址")
			}
			sender := s.resolveEmailSender()
			if s.emailSender == nil {
				return nil, internalFailure(nil)
			}
			code, err := generateVerificationCode()
			if err != nil {
				return nil, internalFailure(err)
			}
			expiresAt := s.now().Add(codeTTL(s.codeExpireMinutes))
			scene := sendCodeScene(in.Scene)
			if err := s.store.InvalidateActiveCodes(MethodEmailCode, email, scene); err != nil {
				return nil, internalFailure(err)
			}
			record := &VerificationCode{
				MethodType:  MethodEmailCode,
				Channel:     ChannelEmail,
				Scene:       scene,
				Target:      email,
				Code:        code,
				ExpiresAt:   expiresAt,
				CreatedAt:   s.now(),
				RequesterIP: optionalString(in.RequesterIP),
				UserAgent:   optionalString(truncate(in.UserAgent, 500)),
			}
			if err := s.store.CreateVerificationCode(record); err != nil {
				return nil, internalFailure(err)
			}
			// 投递失败必须显式失败：否则用户拿不到码却看到一个「已发送」。
			if err := s.emailSender.SendLoginCode(ctx, email, code, s.codeExpireMinutes); err != nil {
				return nil, internalFailure(err)
			}
			return &SendCodeOutput{
				Target:    email,
				Channel:   string(ChannelEmail),
				ExpiresAt: expiresAt,
				DevCode:   s.devEcho(code, sender),
			}, nil
		},
		Login: func(ctx context.Context, in LoginInput) (*LoginOutput, error) {
			email := strings.ToLower(strings.TrimSpace(in.Target))
			code := strings.TrimSpace(in.Code)
			if !validEmail(email) {
				return nil, invalidArgument("请输入正确的邮箱地址")
			}
			if len(code) != 6 {
				return nil, invalidArgument("请输入 6 位验证码")
			}
			record, err := s.store.ConsumeCode(MethodEmailCode, email, code, codeScene)
			if err != nil {
				return nil, unauthorized("验证码无效或已过期")
			}
			user, err := s.resolveExistingUser(MethodEmailCode, email)
			if errors.Is(err, ErrNotFound) {
				return nil, notFound("该邮箱尚未注册，请先注册")
			}
			if err != nil {
				return nil, err
			}
			if err := s.store.AttachCodeUser(record.ID, user.ID); err != nil {
				return nil, internalFailure(err)
			}
			return s.issueSession(user, MethodEmailCode, email, in.RequesterIP, in.UserAgent)
		},
		Register: func(ctx context.Context, in RegisterInput) (*LoginOutput, error) {
			email := strings.ToLower(strings.TrimSpace(in.Target))
			code := strings.TrimSpace(in.Code)
			if !validEmail(email) {
				return nil, invalidArgument("请输入正确的邮箱地址")
			}
			if len(code) != 6 {
				return nil, invalidArgument("请输入 6 位验证码")
			}
			// 先判重再消费验证码：邮箱已注册时不该把码烧掉，用户还能直接去登录。
			if _, err := s.store.UserByEmail(email); err == nil {
				return nil, conflict("该邮箱已注册，请直接登录")
			} else if !errors.Is(err, ErrNotFound) {
				return nil, internalFailure(err)
			}
			record, err := s.store.ConsumeCode(MethodEmailCode, email, code, codeScene)
			if err != nil {
				return nil, unauthorized("验证码无效或已过期")
			}
			user, err := s.createUserWithIdentity(ctx, MethodEmailCode, email)
			if err != nil {
				return nil, err
			}
			if err := s.store.AttachCodeUser(record.ID, user.ID); err != nil {
				return nil, internalFailure(err)
			}
			if err := s.recordAgreements(user.ID, in.AgreementVersion, in.RequesterIP, in.UserAgent); err != nil {
				return nil, internalFailure(err)
			}
			return s.issueSession(user, MethodEmailCode, email, in.RequesterIP, in.UserAgent)
		},
	}
}
