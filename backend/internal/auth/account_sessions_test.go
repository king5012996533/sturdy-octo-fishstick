package auth

import (
	"testing"
	"time"
)

// 登录设备域的用例：列表要能认出"我在哪"，下线要拒绝跨账号操作。

func seedSessionWithAgent(t *testing.T, env *billingTestEnv, id string, userID string, agent string) {
	t.Helper()
	ip := "203.0.113.7"
	if err := env.store.db.Create(&Session{
		ID:                 id,
		UserID:             userID,
		TokenHash:          "hash-" + id,
		AuthMethodType:     MethodEmailCode,
		IdentifierSnapshot: &[]string{"user@example.com"}[0],
		IPAddress:          &ip,
		UserAgent:          &agent,
		CreatedAt:          env.clock.Now(),
		LastActiveAt:       &[]time.Time{env.clock.Now()}[0],
		ExpiresAt:          env.clock.Now().Add(24 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
}

func TestListSessionsMarksCurrentDevice(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-sessions", "sessions@example.com")
	seedSessionWithAgent(t, env, "session-a", "user-sessions", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36")
	seedSessionWithAgent(t, env, "session-b", "user-sessions", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36")

	views, err := env.service.ListSessions("user-sessions", "hash-session-a")
	if err != nil {
		t.Fatalf("读取会话列表失败: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("应有 2 条会话，实际 %d", len(views))
	}
	currentCount := 0
	for _, view := range views {
		if view.Current {
			currentCount++
			if view.ID != "session-a" {
				t.Fatalf("当前设备应为 session-a，实际 %s", view.ID)
			}
		}
		// 标识必须脱敏：设备列表常被截图给客服，完整邮箱在这里没有用途。
		if view.Identifier != "us***@example.com" {
			t.Fatalf("登录标识应脱敏，实际 %q", view.Identifier)
		}
	}
	if currentCount != 1 {
		t.Fatalf("应恰好标记 1 条当前设备，实际 %d", currentCount)
	}
}

func TestDescribeUserAgentFallsBackToUnknown(t *testing.T) {
	if got := describeUserAgent(""); got != "未知设备" {
		t.Fatalf("空 UA 应回落到未知设备，实际 %q", got)
	}
	if got := describeUserAgent("SomeRandomClient/1.0"); got != "未知设备" {
		t.Fatalf("认不出的 UA 应回落到未知设备，实际 %q", got)
	}
	if got := describeUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"); got != "Safari · iPhone" {
		t.Fatalf("iPhone Safari 应被识别，实际 %q", got)
	}
}

// TestRevokeSessionRejectsForeignSession 覆盖"拿到别人的会话 ID 也踢不掉"。
func TestRevokeSessionRejectsForeignSession(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-self", "self@example.com")
	seedSelfServiceUser(t, env, "user-other", "other@example.com")
	seedSessionWithAgent(t, env, "session-victim", "user-other", "Mozilla/5.0 (Windows NT 10.0) Chrome/141.0.0.0 Safari/537.36")

	_, err := env.service.RevokeSession("user-self", "session-victim", "")
	assertBillingError(t, err, 404, "该设备已不在登录状态")
	assertSessionAlive(t, env, "hash-session-victim")
}

func TestRevokeOtherSessionsKeepsCurrentDevice(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-sessions", "sessions@example.com")
	seedSessionWithAgent(t, env, "session-a", "user-sessions", "Mozilla/5.0 (Macintosh) Chrome/141.0.0.0 Safari/537.36")
	seedSessionWithAgent(t, env, "session-b", "user-sessions", "Mozilla/5.0 (Windows NT 10.0) Chrome/141.0.0.0 Safari/537.36")
	seedSessionWithAgent(t, env, "session-c", "user-sessions", "Mozilla/5.0 (X11; Linux x86_64) Firefox/146.0")

	views, revoked, err := env.service.RevokeOtherSessions("user-sessions", "hash-session-a")
	if err != nil {
		t.Fatalf("下线其他设备应成功: %v", err)
	}
	if revoked != 2 {
		t.Fatalf("应下线 2 条会话，实际 %d", revoked)
	}
	if len(views) != 1 || views[0].ID != "session-a" {
		t.Fatalf("剩余会话应只有当前设备，实际 %+v", views)
	}
	assertSessionAlive(t, env, "hash-session-a")
	assertSessionRevoked(t, env, "hash-session-b")
	assertSessionRevoked(t, env, "hash-session-c")
}

// TestActiveSessionsExcludesExpiredAndRevoked 覆盖"过期与已吊销的不能出现在列表里"。
func TestActiveSessionsExcludesExpiredAndRevoked(t *testing.T) {
	env := newBillingTestService(t)
	seedSelfServiceUser(t, env, "user-sessions", "sessions@example.com")
	seedSessionWithAgent(t, env, "session-live", "user-sessions", "Mozilla/5.0 (Windows NT 10.0) Chrome/141.0.0.0 Safari/537.36")

	expired := env.clock.Now().Add(-time.Hour)
	if err := env.store.db.Create(&Session{
		ID: "session-expired", UserID: "user-sessions", TokenHash: "hash-expired",
		AuthMethodType: MethodEmailCode, ExpiresAt: expired, CreatedAt: env.clock.Now().Add(-48 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("建过期会话失败: %v", err)
	}
	revokedAt := env.clock.Now().Add(-time.Minute)
	if err := env.store.db.Create(&Session{
		ID: "session-revoked", UserID: "user-sessions", TokenHash: "hash-revoked",
		AuthMethodType: MethodEmailCode, ExpiresAt: env.clock.Now().Add(time.Hour), CreatedAt: env.clock.Now(), RevokedAt: &revokedAt,
	}).Error; err != nil {
		t.Fatalf("建已吊销会话失败: %v", err)
	}

	views, err := env.service.ListSessions("user-sessions", "")
	if err != nil {
		t.Fatalf("读取会话列表失败: %v", err)
	}
	if len(views) != 1 || views[0].ID != "session-live" {
		t.Fatalf("列表应只含仍在生效的会话，实际 %+v", views)
	}
}
