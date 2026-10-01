package auth

import "testing"

// seedSelfServiceUser 建一个可做自助操作的账号：状态正常、邮箱已验证。
//
// 四个用户中心域（资料 / 密码 / 设备 / 绑定）的用例共用它，因为它们的共同前置条件
// 完全一样：账号处于 ACTIVE。前置条件写四遍，改一次状态口径就要改四处。
func seedSelfServiceUser(t *testing.T, env *billingTestEnv, id string, email string) *User {
	t.Helper()
	name := "测试用户"
	verified := env.clock.Now()
	user := &User{ID: id, Name: &name, Email: &email, Status: StatusActive, CreatedAt: verified, UpdatedAt: verified}
	if err := env.store.CreateUser(user); err != nil {
		t.Fatalf("建账号失败: %v", err)
	}
	return user
}

func TestUpdateProfileTrimsAndPersists(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-profile", "profile@example.com")

	view, err := env.service.UpdateProfile(ProfileUpdate{
		UserID:    "user-profile",
		Name:      "  光启云科  ",
		AvatarURL: " https://cdn.example.com/avatar.png ",
	})
	if err != nil {
		t.Fatalf("更新资料应成功: %v", err)
	}
	if view.Name != "光启云科" {
		t.Fatalf("昵称应被去除首尾空格，实际 %q", view.Name)
	}
	if view.AvatarURL != "https://cdn.example.com/avatar.png" {
		t.Fatalf("头像地址应被去除首尾空格，实际 %q", view.AvatarURL)
	}
	// 回读一次，确认落库而不是只在返回值里改了。
	stored, err := env.store.UserByID("user-profile")
	if err != nil {
		t.Fatalf("回读账号失败: %v", err)
	}
	if displayNameOf(stored) != "光启云科" {
		t.Fatalf("库里的昵称应为 光启云科，实际 %q", displayNameOf(stored))
	}
}

func TestUpdateProfileRejectsEmptyName(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-profile", "profile@example.com")

	_, err := env.service.UpdateProfile(ProfileUpdate{UserID: "user-profile", Name: "   "})
	assertBillingError(t, err, 400, "昵称不能为空")
}

// TestUpdateProfileRejectsNonHTTPAvatar 覆盖「头像字段不能变成脚本注入点」。
//
// 这个值最终会进 <img src>，javascript: 与 data: 在前端没有正当用途。
func TestUpdateProfileRejectsNonHTTPAvatar(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-profile", "profile@example.com")

	for _, avatar := range []string{"javascript:alert(1)", "data:image/svg+xml;base64,PHN2Zz4=", "ftp://example.com/a.png", "//example.com/a.png"} {
		_, err := env.service.UpdateProfile(ProfileUpdate{UserID: "user-profile", Name: "正常昵称", AvatarURL: avatar})
		if err == nil {
			t.Fatalf("%q 应被拒绝", avatar)
		}
	}
}

func TestUpdateProfileClearsAvatarWithEmptyString(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-profile", "profile@example.com")
	if _, err := env.service.UpdateProfile(ProfileUpdate{UserID: "user-profile", Name: "光启云科", AvatarURL: "https://cdn.example.com/a.png"}); err != nil {
		t.Fatalf("首次设置头像应成功: %v", err)
	}
	view, err := env.service.UpdateProfile(ProfileUpdate{UserID: "user-profile", Name: "光启云科"})
	if err != nil {
		t.Fatalf("清空头像应成功: %v", err)
	}
	if view.AvatarURL != "" {
		t.Fatalf("头像应被清空，实际 %q", view.AvatarURL)
	}
	stored, err := env.store.UserByID("user-profile")
	if err != nil {
		t.Fatalf("回读账号失败: %v", err)
	}
	// 空串必须落成 NULL，而不是空字符串：与前缀签名之类的判空逻辑保持一致。
	if stored.AvatarURL != nil {
		t.Fatalf("头像列应为 NULL，实际 %q", *stored.AvatarURL)
	}
}

// TestUpdateProfileRejectsDisabledAccount 覆盖「停用账号不能继续改资料」。
func TestUpdateProfileRejectsDisabledAccount(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-disabled", "disabled@example.com")
	if err := env.store.UpdateUserStatus("user-disabled", StatusDisabled, env.clock.Now()); err != nil {
		t.Fatalf("停用账号失败: %v", err)
	}
	_, err := env.service.UpdateProfile(ProfileUpdate{UserID: "user-disabled", Name: "新名字"})
	assertBillingError(t, err, 403, "")
}
