package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var adminUsernamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{3,31}$`)

// adminPasswordStrategy 实现管理员账号密码登录。
//
// 凭据错误一律返回 401，格式错误返回 400：把两者混成 500 会让真实故障和
// 用户打字错误进入同一个监控指标。
func (s *Service) adminPasswordStrategy() *Strategy {
	return &Strategy{
		MethodType: MethodAdminPassword,
		Category:   CategoryPassword,
		Login: func(_ context.Context, in LoginInput) (*LoginOutput, error) {
			username := strings.TrimSpace(in.Target)
			password := in.Password
			if !adminUsernamePattern.MatchString(username) {
				return nil, invalidArgument("请输入 4-32 位管理员账号，只能包含字母、数字、下划线或中划线")
			}
			if len(password) < 8 || len(password) > 64 {
				return nil, invalidArgument("请输入 8-64 位登录密码")
			}
			user, err := s.store.UserByUsername(username)
			if errors.Is(err, ErrNotFound) {
				return nil, unauthorized("管理员账号或密码错误")
			}
			if err != nil {
				return nil, internalFailure(err)
			}
			if user.Role != RoleAdmin || user.Status == StatusDisabled {
				return nil, unauthorized("管理员账号或密码错误")
			}
			if !VerifyPassword(password, user.PasswordHash) {
				return nil, unauthorized("管理员账号或密码错误")
			}
			return s.issueSession(user, MethodAdminPassword, username, in.RequesterIP, in.UserAgent)
		},
	}
}
