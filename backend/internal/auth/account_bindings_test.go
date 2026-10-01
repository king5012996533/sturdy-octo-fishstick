package auth

import (
	"testing"
)

// 绑定域的用例：换绑必须由新地址收码证明，且换完之后旧地址要能被释放出来。

// newBindingTestEnv 在计费夹具上补一条可用的邮件通道。
//
// 发码走的是登录链路的策略层，策略层要求装配了真实投递器（billing 夹具没装）。
// 用 ConsoleSender 而不是测试替身：这里验证的是"码有没有落到正确的目标上"，
// 投递本身在 sender 的用例里覆盖。
func newBindingTestEnv(t *testing.T) *billingTestEnv {
	t.Helper()
	env := newBillingTestService(t)
	service, err := NewService(Options{
		Store:       env.store,
		EmailSender: ConsoleSender{},
		StateSecret: []byte("test-secret"),
		Now:         env.clock.Now,
	})
	if err != nil {
		t.Fatalf("装配认证服务失败: %v", err)
	}
	env.service = service
	return env
}

func TestSendBindingCodeRejectsCurrentAddress(t *testing.T) {
	env := newBindingTestEnv(t)
	seedSelfServiceUser(t, env, "user-bind", "old@example.com")

	_, err := env.service.SendBindingCode(t.Context(), BindingCodeInput{
		UserID:  "user-bind",
		Channel: ChannelEmail,
		Target:  "OLD@example.com",
	})
	assertBillingError(t, err, 400, "新地址与当前绑定的地址相同")
}

func TestSendBindingCodeRejectsTargetOwnedByAnotherAccount(t *testing.T) {
	env := newBindingTestEnv(t)
	seedSelfServiceUser(t, env, "user-bind", "old@example.com")
	seedSelfServiceUser(t, env, "user-victim", "taken@example.com")

	_, err := env.service.SendBindingCode(t.Context(), BindingCodeInput{
		UserID:  "user-bind",
		Channel: ChannelEmail,
		Target:  "taken@example.com",
	})
	assertBillingError(t, err, 409, "该地址已被其他账号使用")
}

func TestSendBindingCodeRejectsMalformedEmail(t *testing.T) {
	env := newBindingTestEnv(t)
	seedSelfServiceUser(t, env, "user-bind", "old@example.com")

	_, err := env.service.SendBindingCode(t.Context(), BindingCodeInput{
		UserID:  "user-bind",
		Channel: ChannelEmail,
		Target:  "not-an-email",
	})
	assertBillingError(t, err, 400, "请输入正确的邮箱地址")
}

func TestConfirmBindingRequiresCodeForNewAddress(t *testing.T) {
	env := newBindingTestEnv(t)
	seedSelfServiceUser(t, env, "user-bind", "old@example.com")
	// 只给旧地址发码：换绑认的是新地址，旧地址的码不能用来完成换绑。
	seedLoginCode(t, env, MethodEmailCode, "old@example.com")

	_, err := env.service.ConfirmBinding(t.Context(), BindingConfirmInput{
		UserID:  "user-bind",
		Channel: ChannelEmail,
		Target:  "new@example.com",
		Code:    "123456",
	})
	assertBillingError(t, err, 400, "验证码不正确或已过期")
}

// TestConfirmBindingMovesContactIdentitiesAndPassword 覆盖换绑要一次做完的四件事。
func TestConfirmBindingMovesContactIdentitiesAndPassword(t *testing.T) {
	env := newBindingTestEnv(t)
	seedSelfServiceUser(t, env, "user-bind", "old@example.com")
	// 旧邮箱上挂着两条身份：验证码登录入口，以及用旧邮箱注册的密码通道。
	if err := env.store.db.Create(&AuthIdentity{
		ID: "identity-code", UserID: "user-bind", MethodType: MethodEmailCode, Identifier: "old@example.com",
	}).Error; err != nil {
		t.Fatalf("建验证码身份失败: %v", err)
	}
	if err := env.store.db.Create(&AuthIdentity{
		ID: "identity-password", UserID: "user-bind", MethodType: MethodPassword, Identifier: "old@example.com",
	}).Error; err != nil {
		t.Fatalf("建密码身份失败: %v", err)
	}
	seedLoginCode(t, env, MethodEmailCode, "new@example.com")

	view, err := env.service.ConfirmBinding(t.Context(), BindingConfirmInput{
		UserID:  "user-bind",
		Channel: ChannelEmail,
		Target:  "new@example.com",
		Code:    "123456",
	})
	if err != nil {
		t.Fatalf("换绑应成功: %v", err)
	}
	if view.Email != "new@example.com" {
		t.Fatalf("联系邮箱应更新为 new@example.com，实际 %q", view.Email)
	}
	stored, err := env.store.UserByID("user-bind")
	if err != nil {
		t.Fatalf("回读账号失败: %v", err)
	}
	if emailOf(stored) != "new@example.com" {
		t.Fatalf("库里的邮箱应更新，实际 %q", emailOf(stored))
	}
	// 旧标识上的验证码身份必须删掉：(method_type, identifier) 是唯一索引，
	// 留着它会让这个邮箱永远无法被再次绑定。
	if _, err := env.store.IdentityByIdentifier(MethodEmailCode, "old@example.com"); err == nil {
		t.Fatalf("旧邮箱的验证码身份行应被删除")
	}
	fresh, err := env.store.IdentityByIdentifier(MethodEmailCode, "new@example.com")
	if err != nil {
		t.Fatalf("新邮箱应有验证码身份行: %v", err)
	}
	if !fresh.IsVerified {
		t.Fatalf("新身份行应标记为已验证")
	}
	// 密码身份跟着搬：不搬的话，用旧邮箱注册的账号换绑之后密码登录会直接失效。
	moved, err := env.store.IdentityByIdentifier(MethodPassword, "new@example.com")
	if err != nil {
		t.Fatalf("密码身份应迁到新邮箱: %v", err)
	}
	if moved.ID != "identity-password" {
		t.Fatalf("密码身份应原地迁移而不是新建，实际 %s", moved.ID)
	}
}

// TestConfirmBindingFreesOldAddress 覆盖"换绑之后旧地址可以被别人绑"。
func TestConfirmBindingFreesOldAddress(t *testing.T) {
	env := newBindingTestEnv(t)
	seedSelfServiceUser(t, env, "user-bind", "old@example.com")
	seedLoginCode(t, env, MethodEmailCode, "new@example.com")
	if _, err := env.service.ConfirmBinding(t.Context(), BindingConfirmInput{
		UserID:  "user-bind",
		Channel: ChannelEmail,
		Target:  "new@example.com",
		Code:    "123456",
	}); err != nil {
		t.Fatalf("换绑应成功: %v", err)
	}

	seedSelfServiceUser(t, env, "user-newcomer", "newcomer@example.com")
	if _, err := env.service.SendBindingCode(t.Context(), BindingCodeInput{
		UserID:  "user-newcomer",
		Channel: ChannelEmail,
		Target:  "old@example.com",
	}); err != nil {
		t.Fatalf("旧地址应可被其他账号绑定，实际报错: %v", err)
	}
}

// TestBindingsViewMarksPrimaryContact 覆盖绑定列表要标出"哪条是联系地址"。
func TestBindingsViewMarksPrimaryContact(t *testing.T) {
	env := newBindingTestEnv(t)
	seedSelfServiceUser(t, env, "user-bind", "old@example.com")
	if err := env.store.db.Create(&AuthIdentity{
		ID: "identity-code", UserID: "user-bind", MethodType: MethodEmailCode, Identifier: "old@example.com", IsVerified: true,
	}).Error; err != nil {
		t.Fatalf("建验证码身份失败: %v", err)
	}
	if err := env.store.db.Create(&AuthIdentity{
		ID: "identity-github", UserID: "user-bind", MethodType: MethodGithubOAuth, Identifier: "gh-12345",
	}).Error; err != nil {
		t.Fatalf("建 GitHub 身份失败: %v", err)
	}

	view, err := env.service.Bindings("user-bind")
	if err != nil {
		t.Fatalf("读取绑定失败: %v", err)
	}
	if len(view.Bindings) != 2 {
		t.Fatalf("应有 2 条绑定，实际 %d", len(view.Bindings))
	}
	for _, binding := range view.Bindings {
		if binding.MethodType == string(MethodEmailCode) && !binding.Primary {
			t.Fatalf("邮箱验证码应是主要联系地址")
		}
		if binding.MethodType == string(MethodGithubOAuth) && binding.Primary {
			t.Fatalf("GitHub 绑定不应被标成主要联系地址")
		}
		if binding.MethodType == string(MethodGithubOAuth) && binding.Label != "GitHub" {
			t.Fatalf("GitHub 的中文名应为 GitHub，实际 %q", binding.Label)
		}
	}
}
