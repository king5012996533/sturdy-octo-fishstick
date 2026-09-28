package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// mainlandPhonePattern 匹配中国大陆手机号：11 位，1 开头，第二位 3-9。
var mainlandPhonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)

// normalizePhone 统一手机号的书写形式。
//
// 「+86 138-0013-8000」和「13800138000」必须落到同一个身份标识上，否则同一个号
// 能注册出多个账号，按手机号找回账号也就失去意义。
func normalizePhone(value string) string {
	trimmed := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", "\t", "").Replace(strings.TrimSpace(value))
	trimmed = strings.TrimPrefix(trimmed, "+")
	if strings.HasPrefix(trimmed, "86") && len(trimmed) > 11 {
		trimmed = trimmed[2:]
	}
	return trimmed
}

// phoneCodeStrategy 实现手机号短信验证码登录。
//
// 与邮箱策略同构，差别只在投递通道和标识格式：注册同样必须带上协议版本，登录
// 同样不建号。
func (s *Service) phoneCodeStrategy() *Strategy {
	return &Strategy{
		MethodType: MethodPhoneCode,
		Category:   CategoryCode,
		SendCode: func(ctx context.Context, in SendCodeInput) (*SendCodeOutput, error) {
			phone := normalizePhone(in.Target)
			if !mainlandPhonePattern.MatchString(phone) {
				return nil, invalidArgument("请输入正确的手机号")
			}
			if s.smsSender == nil {
				return nil, internalFailure(errors.New("短信通道未配置"))
			}
			code, err := generateVerificationCode()
			if err != nil {
				return nil, internalFailure(err)
			}
			expiresAt := s.now().Add(codeTTL(s.codeExpireMinutes))
			if err := s.store.InvalidateActiveCodes(MethodPhoneCode, phone, codeScene); err != nil {
				return nil, internalFailure(err)
			}
			record := &VerificationCode{
				MethodType:  MethodPhoneCode,
				Channel:     ChannelPhone,
				Scene:       codeScene,
				Target:      phone,
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
			if err := s.smsSender.SendLoginCode(ctx, phone, code, s.codeExpireMinutes); err != nil {
				return nil, internalFailure(err)
			}
			return &SendCodeOutput{
				Target:    phone,
				Channel:   string(ChannelPhone),
				ExpiresAt: expiresAt,
				DevCode:   s.devEcho(code, s.smsSender),
			}, nil
		},
		Login: func(ctx context.Context, in LoginInput) (*LoginOutput, error) {
			phone := normalizePhone(in.Target)
			code := strings.TrimSpace(in.Code)
			if !mainlandPhonePattern.MatchString(phone) {
				return nil, invalidArgument("请输入正确的手机号")
			}
			if len(code) != 6 {
				return nil, invalidArgument("请输入 6 位验证码")
			}
			record, err := s.store.ConsumeCode(MethodPhoneCode, phone, code, codeScene)
			if err != nil {
				return nil, unauthorized("验证码无效或已过期")
			}
			user, err := s.resolveExistingUser(MethodPhoneCode, phone)
			if errors.Is(err, ErrNotFound) {
				return nil, notFound("该手机号尚未注册，请先注册")
			}
			if err != nil {
				return nil, err
			}
			if err := s.store.AttachCodeUser(record.ID, user.ID); err != nil {
				return nil, internalFailure(err)
			}
			return s.issueSession(user, MethodPhoneCode, phone, in.RequesterIP, in.UserAgent)
		},
		Register: func(ctx context.Context, in RegisterInput) (*LoginOutput, error) {
			phone := normalizePhone(in.Target)
			code := strings.TrimSpace(in.Code)
			if !mainlandPhonePattern.MatchString(phone) {
				return nil, invalidArgument("请输入正确的手机号")
			}
			if len(code) != 6 {
				return nil, invalidArgument("请输入 6 位验证码")
			}
			// 先判重再消费验证码：号已注册时不该把码烧掉，用户还能直接去登录。
			if _, err := s.store.UserByPhone(phone); err == nil {
				return nil, conflict("该手机号已注册，请直接登录")
			} else if !errors.Is(err, ErrNotFound) {
				return nil, internalFailure(err)
			}
			record, err := s.store.ConsumeCode(MethodPhoneCode, phone, code, codeScene)
			if err != nil {
				return nil, unauthorized("验证码无效或已过期")
			}
			user, err := s.createUserWithIdentity(ctx, MethodPhoneCode, phone)
			if err != nil {
				return nil, err
			}
			if err := s.store.AttachCodeUser(record.ID, user.ID); err != nil {
				return nil, internalFailure(err)
			}
			if err := s.recordAgreements(user.ID, in.AgreementVersion, in.RequesterIP, in.UserAgent); err != nil {
				return nil, internalFailure(err)
			}
			return s.issueSession(user, MethodPhoneCode, phone, in.RequesterIP, in.UserAgent)
		},
	}
}
