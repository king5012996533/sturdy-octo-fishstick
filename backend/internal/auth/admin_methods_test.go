package auth

import (
	"errors"
	"testing"
)

func boolPtr(value bool) *bool { return &value }

// TestAdminMethodConfigSurvivesReseed 守住管理开关与凭据推导的边界。
//
// GitHub 通道的 is_enabled 列每次启动都会按"凭据齐备 且 管理员允许"重算，如果
// 管理员动作只写那一列，就会被下次启动悄悄打开——这是最容易被当成"后台没保存"
// 的坑，因此必须有用例钉住。
func TestAdminMethodConfigSurvivesReseed(t *testing.T) {
	t.Setenv("BEEFTV_GITHUB_CLIENT_ID", "test-client-id")
	t.Setenv("BEEFTV_GITHUB_CLIENT_SECRET", "test-client-secret")
	t.Setenv("BEEFTV_GITHUB_REDIRECT_URI", "http://localhost:3000/auth/oauth/callback")

	env := newTestEnv(t)
	if err := seedDevMethodConfigs(env.db); err != nil {
		t.Fatalf("按凭据收敛失败: %v", err)
	}
	if view, err := env.service.AdminUpdateMethodConfig(MethodGithubOAuth, AdminMethodConfigInput{IsEnabled: boolPtr(false)}); err != nil || view.IsEnabled {
		t.Fatalf("关闭 GitHub 登录失败: enabled=%v err=%v", view != nil && view.IsEnabled, err)
	}
	if err := seedDevMethodConfigs(env.db); err != nil {
		t.Fatalf("重启后收敛失败: %v", err)
	}
	config, err := env.service.store.MethodConfig(MethodGithubOAuth)
	if err != nil {
		t.Fatalf("读取 GitHub 配置失败: %v", err)
	}
	if config.IsEnabled {
		t.Fatal("管理员的关闭动作被凭据重算覆盖了")
	}
	// 凭据本身不能被开关一起抹掉：否则"关掉再打开"会变成一次静默的注销配置。
	oauth, err := ParseOAuthConfig(config.ConfigJSON)
	if err != nil || oauth.ClientID != "test-client-id" {
		t.Fatalf("重算丢掉了凭据：clientId=%q err=%v", oauth.ClientID, err)
	}

	// 再打开一次：应当恢复启用，且不需要重启。
	view, err := env.service.AdminUpdateMethodConfig(MethodGithubOAuth, AdminMethodConfigInput{IsEnabled: boolPtr(true)})
	if err != nil || !view.IsEnabled {
		t.Fatalf("重新打开 GitHub 登录失败: %+v err=%v", view, err)
	}
}

// TestAdminMethodConfigRejectsDisablingLastChannel 确认不能把登录通道全部关掉。
func TestAdminMethodConfigRejectsDisablingLastChannel(t *testing.T) {
	env := newTestEnv(t)
	// 开发库里默认启用了邮箱验证码、手机验证码、GitHub 与密码四条通道（见
	// EnsureDevSchema 的补默认值），这里逐条关到只剩密码为止。
	for _, method := range []MethodType{MethodEmailCode, MethodPhoneCode, MethodGithubOAuth, MethodAdminPassword} {
		if _, err := env.service.AdminUpdateMethodConfig(method, AdminMethodConfigInput{IsEnabled: boolPtr(false)}); err != nil {
			t.Fatalf("关闭 %s 失败: %v", method, err)
		}
	}
	_, err := env.service.AdminUpdateMethodConfig(MethodPassword, AdminMethodConfigInput{IsEnabled: boolPtr(false)})
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Status != 400 {
		t.Fatalf("关闭最后一种登录方式应被拒绝，实际 err=%v", err)
	}
}

// TestAdminMethodConfigReportsUnconfiguredOAuth 确认"没配凭据"不会变成一个能点的开关。
func TestAdminMethodConfigReportsUnconfiguredOAuth(t *testing.T) {
	t.Setenv("BEEFTV_GITHUB_CLIENT_ID", "")
	t.Setenv("BEEFTV_GITHUB_CLIENT_SECRET", "")
	env := newTestEnv(t)
	if err := env.db.Model(&MethodConfig{}).
		Where("method_type = ?", string(MethodGithubOAuth)).
		Updates(map[string]any{"config_json": []byte(`{}`), "is_enabled": false}).Error; err != nil {
		t.Fatalf("清空 GitHub 凭据失败: %v", err)
	}
	view, err := env.service.AdminUpdateMethodConfig(MethodGithubOAuth, AdminMethodConfigInput{IsEnabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("更新 GitHub 登录失败: %v", err)
	}
	if view.IsEnabled || view.Ready || view.UnavailableReason == "" {
		t.Fatalf("缺凭据时不应真的打开，且必须说明原因: %+v", view)
	}
}

// TestAdminMethodConfigListsDisabledChannels 确认管理视角能看到被禁用的通道。
func TestAdminMethodConfigListsDisabledChannels(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.service.AdminUpdateMethodConfig(MethodGithubOAuth, AdminMethodConfigInput{IsEnabled: boolPtr(false)}); err != nil {
		t.Fatalf("关闭 GitHub 失败: %v", err)
	}
	methods, err := env.service.AdminMethodConfigs()
	if err != nil {
		t.Fatalf("读取登录方式列表失败: %v", err)
	}
	found := false
	for _, method := range methods {
		if method.MethodType == MethodGithubOAuth {
			found = true
			if method.IsEnabled {
				t.Fatal("管理视角必须能看到被关闭的通道")
			}
		}
	}
	if !found {
		t.Fatal("列表缺少 GitHub 登录方式")
	}
}
