package auth

// buildStrategies 汇总全部登录方式。
//
// 手机号登录暂以占位实现注册，以便配置里出现 PHONE_CODE 时给出明确错误，
// 而不是让请求落到「未知登录方式」这种含糊分支。
func (s *Service) buildStrategies() map[MethodType]*Strategy {
	items := []*Strategy{
		s.adminPasswordStrategy(),
		s.passwordStrategy(),
		s.emailCodeStrategy(),
		s.phoneCodeStrategy(),
		s.githubOAuthStrategy(),
	}
	strategies := make(map[MethodType]*Strategy, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		strategies[item.MethodType] = item
	}
	return strategies
}
