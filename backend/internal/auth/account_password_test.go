package auth

import (
	"strings"
	"testing"
	"time"
)

// seedActiveSession 挂一条未过期的会话，返回它的明文令牌摘要键。
func seedActiveSession(t *testing.T, env *billingTestEnv, id string, userID string) string {
	t.Helper()
	tokenHash := "hash-" + id
	if err := env.store.db.Create(&Session{
		ID:             id,
		UserID:         userID,
		TokenHash:      tokenHash,
		AuthMethodType: MethodEmailCode,
		ExpiresAt:      env.clock.Now().Add(24 * time.Hour),
		CreatedAt:      env.clock.Now(),
	}).Error; err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	return tokenHash
}

func TestSetPasswordRequiresVerificationCode(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-pw", "pw@example.com")

	_, err := env.service.SetPassword(t.Context(), SetPasswordInput{
		UserID:      "user-pw",
		MethodType:  MethodEmailCode,
		Code:        "000000",
		NewPassword: "abcdefgh",
	})
	assertBillingError(t, err, 400, "验证码不正确或已过期")
}

// TestSetPasswordRejectsAccountThatAlreadyHasPassword 覆盖"已有密码必须走修改流程"。
//
// 允许用验证码覆盖一个已经存在的密码，等于把"改密"降级成"有会话 + 有收件箱"，
// 而后者正是攻击者在拿到会话之后还想再拿一次的东西。
func TestSetPasswordRejectsAccountThatAlreadyHasPassword(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-pw", "pw@example.com")
	hash, err := HashPassword("existing-pass-1")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if err := env.store.UpdateUserPassword("user-pw", hash, env.clock.Now()); err != nil {
		t.Fatalf("写入初始密码失败: %v", err)
	}
	seedLoginCode(t, env, MethodEmailCode, "pw@example.com")

	_, err = env.service.SetPassword(t.Context(), SetPasswordInput{
		UserID:      "user-pw",
		MethodType:  MethodEmailCode,
		Code:        "123456",
		NewPassword: "brand-new-pass-1",
	})
	assertBillingError(t, err, 409, "该账号已经设置过密码，请改用「修改密码」")
}

func TestSetPasswordPersistsHashAndRevokesOtherSessions(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-pw", "pw@example.com")
	current := seedActiveSession(t, env, "session-current", "user-pw")
	seedActiveSession(t, env, "session-other", "user-pw")
	seedLoginCode(t, env, MethodEmailCode, "pw@example.com")

	result, err := env.service.SetPassword(t.Context(), SetPasswordInput{
		UserID:           "user-pw",
		MethodType:       MethodEmailCode,
		Code:             "123456",
		NewPassword:      "abcdefgh",
		CurrentTokenHash: current,
	})
	if err != nil {
		t.Fatalf("设置密码应成功: %v", err)
	}
	if !result.State.HasPassword {
		t.Fatalf("设置后 HasPassword 应为 true")
	}
	if result.RevokedSessions != 1 {
		t.Fatalf("应只吊销 1 条其他会话，实际 %d", result.RevokedSessions)
	}
	stored, err := env.store.UserByID("user-pw")
	if err != nil {
		t.Fatalf("回读账号失败: %v", err)
	}
	if !VerifyPassword("abcdefgh", stored.PasswordHash) {
		t.Fatalf("落库的哈希无法用新密码验证")
	}
	// 当前设备必须仍然有效：改密之后自己被踢下线是最容易写错也最恼人的一种回归。
	assertSessionAlive(t, env, current)
	assertSessionRevoked(t, env, "hash-session-other")
}

func TestChangePasswordRequiresCurrentPassword(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-pw", "pw@example.com")
	hash, err := HashPassword("original-pass-1")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if err := env.store.UpdateUserPassword("user-pw", hash, env.clock.Now()); err != nil {
		t.Fatalf("写入初始密码失败: %v", err)
	}

	_, err = env.service.ChangePassword(ChangePasswordInput{
		UserID:          "user-pw",
		CurrentPassword: "wrong-pass-1",
		NewPassword:     "another-pass-1",
	})
	assertBillingError(t, err, 401, "当前密码不正确")
}

func TestChangePasswordRejectsSamePassword(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-pw", "pw@example.com")
	hash, err := HashPassword("original-pass-1")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if err := env.store.UpdateUserPassword("user-pw", hash, env.clock.Now()); err != nil {
		t.Fatalf("写入初始密码失败: %v", err)
	}

	_, err = env.service.ChangePassword(ChangePasswordInput{
		UserID:          "user-pw",
		CurrentPassword: "original-pass-1",
		NewPassword:     "original-pass-1",
	})
	assertBillingError(t, err, 400, "新密码不能与当前密码相同")
}

func TestChangePasswordKeepsCurrentSessionAndRevokesOthers(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-pw", "pw@example.com")
	hash, err := HashPassword("original-pass-1")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if err := env.store.UpdateUserPassword("user-pw", hash, env.clock.Now()); err != nil {
		t.Fatalf("写入初始密码失败: %v", err)
	}
	current := seedActiveSession(t, env, "session-current", "user-pw")
	seedActiveSession(t, env, "session-other", "user-pw")

	result, err := env.service.ChangePassword(ChangePasswordInput{
		UserID:           "user-pw",
		CurrentPassword:  "original-pass-1",
		NewPassword:      "updated-pass-1",
		CurrentTokenHash: current,
	})
	if err != nil {
		t.Fatalf("修改密码应成功: %v", err)
	}
	if result.RevokedSessions != 1 {
		t.Fatalf("应吊销 1 条其他会话，实际 %d", result.RevokedSessions)
	}
	assertSessionAlive(t, env, current)
	assertSessionRevoked(t, env, "hash-session-other")
}

// TestChangePasswordWithoutCurrentTokenRevokesEverything 覆盖"拿不到当前会话就全吊销"。
//
// 宁可让用户重新登录一次，也不留下一台来路不明的设备继续持有凭据。
func TestChangePasswordWithoutCurrentTokenRevokesEverything(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-pw", "pw@example.com")
	hash, err := HashPassword("original-pass-1")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if err := env.store.UpdateUserPassword("user-pw", hash, env.clock.Now()); err != nil {
		t.Fatalf("写入初始密码失败: %v", err)
	}
	seedActiveSession(t, env, "session-current", "user-pw")

	result, err := env.service.ChangePassword(ChangePasswordInput{
		UserID:          "user-pw",
		CurrentPassword: "original-pass-1",
		NewPassword:     "updated-pass-1",
	})
	if err != nil {
		t.Fatalf("修改密码应成功: %v", err)
	}
	if result.RevokedSessions != 1 {
		t.Fatalf("当前会话未知时应全部吊销（1 条），实际 %d", result.RevokedSessions)
	}
}

func TestChangePasswordThrottlesRepeatedFailures(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-pw", "pw@example.com")
	hash, err := HashPassword("original-pass-1")
	if err != nil {
		t.Fatalf("生成哈希失败: %v", err)
	}
	if err := env.store.UpdateUserPassword("user-pw", hash, env.clock.Now()); err != nil {
		t.Fatalf("写入初始密码失败: %v", err)
	}
	var last error
	for attempt := 0; attempt < passwordFailureLimit+1; attempt++ {
		_, last = env.service.ChangePassword(ChangePasswordInput{
			UserID:          "user-pw",
			CurrentPassword: "wrong-pass-1",
			NewPassword:     "updated-pass-1",
		})
	}
	assertBillingError(t, last, 429, "")
	if !strings.Contains(last.Error(), "次数过多") {
		t.Fatalf("节流文案应说明次数过多，实际 %q", last.Error())
	}
}

func assertSessionAlive(t *testing.T, env *billingTestEnv, tokenHash string) {
	t.Helper()
	var session Session
	if err := env.store.db.Where("token_hash = ?", tokenHash).First(&session).Error; err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	if session.RevokedAt != nil {
		t.Fatalf("会话 %s 不应被吊销，实际吊销于 %v", tokenHash, session.RevokedAt)
	}
}

func assertSessionRevoked(t *testing.T, env *billingTestEnv, tokenHash string) {
	t.Helper()
	var session Session
	if err := env.store.db.Where("token_hash = ?", tokenHash).First(&session).Error; err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	if session.RevokedAt == nil {
		t.Fatalf("会话 %s 应被吊销，实际仍然有效", tokenHash)
	}
}
