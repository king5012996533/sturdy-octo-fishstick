package auth

import (
	"context"
	"net/http"
	"testing"
)

// seedPasswordUser 建一个"已经有密码"的账号：重置密码的主场景。
func seedPasswordUser(t *testing.T, env *testEnv, email string, password string) *User {
	t.Helper()
	user := registerTestAccount(t, env, email)
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("生成密码哈希失败: %v", err)
	}
	if err := env.service.store.UpdateUserPassword(user.ID, hash, env.clock.Now()); err != nil {
		t.Fatalf("写入密码失败: %v", err)
	}
	return user
}

// activeSessionCount 数当前还活着的会话。
//
// 不写死数字：注册本身就会签发一条会话，用例多登一次就变一个数，写死等于把
// "注册会发会话"这条无关的实现细节钉进断言里。
func activeSessionCount(t *testing.T, env *testEnv, userID string) int64 {
	t.Helper()
	var count int64
	if err := env.db.Model(&Session{}).Where("user_id = ? AND revoked_at IS NULL", userID).Count(&count).Error; err != nil {
		t.Fatalf("统计会话失败: %v", err)
	}
	if count == 0 {
		t.Fatal("用例前置条件不成立：账号没有任何有效会话")
	}
	return count
}

func loginWithPassword(t *testing.T, env *testEnv, email string, password string) *LoginOutput {
	t.Helper()
	output, err := env.service.Login(context.Background(), LoginInput{
		MethodType:  MethodPassword,
		Target:      email,
		Password:    password,
		RequesterIP: "203.0.113.11",
		UserAgent:   "beef-tv-test/1.0",
	})
	if err != nil {
		t.Fatalf("密码登录失败: %v", err)
	}
	return output
}

// TestPasswordResetCodeHasItsOwnScene 覆盖「忘记密码」验证码不进登录冷却的那条决策。
//
// 用一个场景会怎样：用户在登录页点过一次"发送验证码"，转头点忘记密码，会被同一分钟的
// 冷却挡回去，而他能看到的只有一句"发送过于频繁"。这里的断言就是钉住两者互不消耗。
func TestPasswordResetCodeHasItsOwnScene(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	registerTestAccount(t, env, "reset-scene@example.com")

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "reset-scene@example.com"}); err != nil {
		t.Fatalf("下发登录验证码失败: %v", err)
	}
	// 同一目标、同一通道、同一时刻：登录场景的冷却对重置场景无效。
	if _, err := env.service.SendPasswordResetCode(ctx, RequestPasswordResetCodeInput{MethodType: MethodEmailCode, Target: "reset-scene@example.com"}); err != nil {
		t.Fatalf("重置验证码不应受登录场景冷却影响: %v", err)
	}
	var resetRecords int64
	if err := env.db.Model(&VerificationCode{}).
		Where("target = ? AND scene = ?", "reset-scene@example.com", passwordResetScene).
		Count(&resetRecords).Error; err != nil {
		t.Fatalf("统计重置验证码失败: %v", err)
	}
	if resetRecords != 1 {
		t.Fatalf("重置验证码应落在 %q 场景下，实际 %d 条", passwordResetScene, resetRecords)
	}
	// 反向也要成立：重置这条通道自己仍然有冷却，不是一条绕过频率限制的后门。
	if _, err := env.service.SendPasswordResetCode(ctx, RequestPasswordResetCodeInput{MethodType: MethodEmailCode, Target: "reset-scene@example.com"}); err == nil {
		t.Fatal("同一目标连续下发重置验证码应被冷却拦下")
	}
}

// TestResetPasswordRejectsLoginSceneCode 是场景分离的安全属性：登录码不能当重置码用。
func TestResetPasswordRejectsLoginSceneCode(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	seedPasswordUser(t, env, "reset-scene-mix@example.com", "abc12345")

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "reset-scene-mix@example.com"}); err != nil {
		t.Fatalf("下发登录验证码失败: %v", err)
	}
	_, loginCode, _ := env.sender.last()

	_, err := env.service.ResetPassword(ResetPasswordInput{
		MethodType:  MethodEmailCode,
		Target:      "reset-scene-mix@example.com",
		Code:        loginCode,
		NewPassword: "newpass123",
	})
	assertAuthError(t, err, http.StatusBadRequest, "invalid_argument")
}

// TestResetPasswordRotatesCredentialAndRevokesSessions 覆盖主线：换掉密码、踢掉其他设备、
// 保留发起重置的那台设备。
func TestResetPasswordRotatesCredentialAndRevokesSessions(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	user := seedPasswordUser(t, env, "reset-main@example.com", "abc12345")

	first := loginWithPassword(t, env, "reset-main@example.com", "abc12345")
	second := loginWithPassword(t, env, "reset-main@example.com", "abc12345")
	active := activeSessionCount(t, env, user.ID)

	if _, err := env.service.SendPasswordResetCode(ctx, RequestPasswordResetCodeInput{MethodType: MethodEmailCode, Target: "reset-main@example.com"}); err != nil {
		t.Fatalf("下发重置验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()

	result, err := env.service.ResetPassword(ResetPasswordInput{
		MethodType:       MethodEmailCode,
		Target:           "reset-main@example.com",
		Code:             code,
		NewPassword:      "brandnew9876",
		CurrentTokenHash: SessionTokenHash(first.Token),
	})
	if err != nil {
		t.Fatalf("重置密码失败: %v", err)
	}
	if !result.State.HasPassword {
		t.Fatal("重置后账号应处于已设置密码状态")
	}
	if result.RevokedSessions != active-1 {
		t.Fatalf("应保留当前设备、吊销其余 %d 条会话，实际 %d", active-1, result.RevokedSessions)
	}
	// 发起重置的那台设备继续可用；另一台立刻失效——密码换了而旧会话还能用，
	// 正是账号被盗之后攻击者最想要的结果。
	if _, err := env.service.SessionUser(ctx, second.Token); err == nil {
		t.Fatal("另一台设备的会话应已被吊销")
	}
	// 新密码能登、旧密码不能登。
	loginWithPassword(t, env, "reset-main@example.com", "brandnew9876")
	if _, err := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "reset-main@example.com", Password: "abc12345"}); err == nil {
		t.Fatal("旧密码不应还能登录")
	}
	// 验证码是一次性的：同一串码不能把密码再改一次。
	if _, err := env.service.ResetPassword(ResetPasswordInput{
		MethodType:  MethodEmailCode,
		Target:      "reset-main@example.com",
		Code:        code,
		NewPassword: "thirdpass123",
	}); err == nil {
		t.Fatal("同一个验证码不应能二次重置")
	}
}

// TestResetPasswordWithoutSessionRevokesAll 覆盖登录页那条路：没有会话时全部吊销。
func TestResetPasswordWithoutSessionRevokesAll(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	user := seedPasswordUser(t, env, "reset-anon@example.com", "abc12345")
	session := loginWithPassword(t, env, "reset-anon@example.com", "abc12345")
	active := activeSessionCount(t, env, user.ID)

	if _, err := env.service.SendPasswordResetCode(ctx, RequestPasswordResetCodeInput{MethodType: MethodEmailCode, Target: "reset-anon@example.com"}); err != nil {
		t.Fatalf("下发重置验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()

	result, err := env.service.ResetPassword(ResetPasswordInput{
		MethodType:  MethodEmailCode,
		Target:      "reset-anon@example.com",
		Code:        code,
		NewPassword: "brandnew9876",
	})
	if err != nil {
		t.Fatalf("重置密码失败: %v", err)
	}
	if result.RevokedSessions != active {
		t.Fatalf("无会话发起重置时应吊销全部 %d 条会话，实际 %d", active, result.RevokedSessions)
	}
	if _, err := env.service.SessionUser(ctx, session.Token); err == nil {
		t.Fatal("全部会话都该失效")
	}
}

// TestResetPasswordRejectsPasswordChannel 是这条路径的底线：它绝不能接受任何密码通道，
// 否则"忘记密码"就成了"绕过密码"。
func TestResetPasswordRejectsPasswordChannel(t *testing.T) {
	env := newTestEnv(t)
	seedPasswordUser(t, env, "reset-channel@example.com", "abc12345")

	for _, methodType := range []MethodType{MethodPassword, MethodAdminPassword, MethodGithubOAuth} {
		_, err := env.service.ResetPassword(ResetPasswordInput{
			MethodType:  methodType,
			Target:      "reset-channel@example.com",
			Code:        "123456",
			NewPassword: "brandnew9876",
		})
		assertAuthError(t, err, http.StatusBadRequest, "invalid_argument")
	}
}

func TestResetPasswordRejectsUnknownTargetAndBadCode(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	seedPasswordUser(t, env, "reset-errors@example.com", "abc12345")

	_, err := env.service.ResetPassword(ResetPasswordInput{
		MethodType:  MethodEmailCode,
		Target:      "nobody@example.com",
		Code:        "123456",
		NewPassword: "brandnew9876",
	})
	assertAuthError(t, err, http.StatusNotFound, "not_found")

	// 码错了要能重来，但密码必须原封不动。
	_, err = env.service.ResetPassword(ResetPasswordInput{
		MethodType:  MethodEmailCode,
		Target:      "reset-errors@example.com",
		Code:        "000000",
		NewPassword: "brandnew9876",
	})
	assertAuthError(t, err, http.StatusBadRequest, "invalid_argument")
	loginWithPassword(t, env, "reset-errors@example.com", "abc12345")

	// 新密码不满足长度规则：验证码已被核销，但密码没被改。
	if _, err := env.service.SendPasswordResetCode(ctx, RequestPasswordResetCodeInput{MethodType: MethodEmailCode, Target: "reset-errors@example.com"}); err != nil {
		t.Fatalf("下发重置验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()
	_, err = env.service.ResetPassword(ResetPasswordInput{
		MethodType:  MethodEmailCode,
		Target:      "reset-errors@example.com",
		Code:        code,
		NewPassword: "short",
	})
	assertAuthError(t, err, http.StatusBadRequest, "invalid_argument")
	loginWithPassword(t, env, "reset-errors@example.com", "abc12345")
}

// TestPasswordResetRefusesDisabledAccount 确认注销/封禁之后的账号拿不回凭据。
func TestPasswordResetRefusesDisabledAccount(t *testing.T) {
	env := newTestEnv(t)
	seedPasswordUser(t, env, "reset-disabled@example.com", "abc12345")
	if err := env.db.Model(&User{}).Where("email = ?", "reset-disabled@example.com").
		Update("status", string(StatusDisabled)).Error; err != nil {
		t.Fatalf("停用账号失败: %v", err)
	}

	if _, err := env.service.SendPasswordResetCode(context.Background(), RequestPasswordResetCodeInput{
		MethodType: MethodEmailCode,
		Target:     "reset-disabled@example.com",
	}); err == nil {
		t.Fatal("停用账号不应收到重置验证码")
	}
	_, err := env.service.ResetPassword(ResetPasswordInput{
		MethodType:  MethodEmailCode,
		Target:      "reset-disabled@example.com",
		Code:        "123456",
		NewPassword: "brandnew9876",
	})
	assertAuthError(t, err, http.StatusForbidden, "forbidden")
}
