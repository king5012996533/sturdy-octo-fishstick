package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// nodeScryptFixture 由 Node 生成，用于锁定跨实现兼容性：
//
//	node -e "const c=require('crypto');console.log('scrypt:'+salt+':'+c.scryptSync('correct horse battery staple', salt, 64).toString('hex'))"
//
// 与 CanvasMind 的 hashUserPassword 使用同一算法与参数；改动 scrypt 参数会让存量
// 用户的密码全部失效，因此这里用固定向量守住它。
const (
	nodeScryptSalt = "00112233445566778899aabbccddeeff"
	nodeScryptHash = "scrypt:" + nodeScryptSalt + ":5699cfee2c5c280e66678242092f368ce88ff05305af2c75a9e629d473deb2165b3797e0e31ec3cda30414573befb697f928384c38b187e8c176107e5be20f01"
	nodeScryptPass = "correct horse battery staple"
)

type captureSender struct {
	mu    sync.Mutex
	to    string
	code  string
	calls int
}

func (s *captureSender) SendLoginCode(_ context.Context, to string, code string, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.to = to
	s.code = code
	s.calls++
	return nil
}

func (s *captureSender) last() (string, string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.to, s.code, s.calls
}

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

type testEnv struct {
	service *Service
	sender  *captureSender
	sms     *captureSender
	clock   *testClock
	db      *gorm.DB
}

func seedMethodConfig(t *testing.T, db *gorm.DB, methodType MethodType, category MethodCategory, allowSignUp bool) {
	t.Helper()
	record := &MethodConfig{
		ID:          string(methodType) + "-config",
		MethodType:  methodType,
		Category:    category,
		DisplayName: string(methodType),
		IsEnabled:   true,
		IsVisible:   true,
		SortOrder:   10,
		AllowSignUp: allowSignUp,
	}
	// EnsureDevSchema 已经补了一批默认配置，因此这里按 method_type 查找后按需插入：
	// 直接用 Create 会撞上唯一索引。
	var existing MethodConfig
	switch err := db.Where("method_type = ?", string(methodType)).First(&existing).Error; {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := db.Create(record).Error; err != nil {
			t.Fatalf("写入登录方式配置失败: %v", err)
		}
	case err != nil:
		t.Fatalf("读取登录方式配置失败: %v", err)
	}
	if err := db.Model(&MethodConfig{}).
		Where("method_type = ?", string(methodType)).
		Updates(map[string]any{"allow_sign_up": allowSignUp, "is_enabled": true}).Error; err != nil {
		t.Fatalf("更新登录方式配置失败: %v", err)
	}
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := EnsureDevSchema(db); err != nil {
		t.Fatalf("初始化测试表失败: %v", err)
	}
	seedMethodConfig(t, db, MethodEmailCode, CategoryCode, true)
	seedMethodConfig(t, db, MethodGithubOAuth, CategoryOAuth, true)
	seedMethodConfig(t, db, MethodAdminPassword, CategoryPassword, false)
	seedMethodConfig(t, db, MethodPassword, CategoryPassword, true)
	// GitHub 的 clientId/secret 存在 auth_method_configs.config_json 里；
	// 这里走配置而不是环境变量，覆盖的是真实读取路径。
	if err := db.Model(&MethodConfig{}).
		Where("method_type = ?", string(MethodGithubOAuth)).
		Updates(map[string]any{
			"config_json": []byte(`{"clientId":"test-client","clientSecret":"test-secret","redirectUri":"https://example.com/api/auth/oauth/callback"}`),
			"is_enabled":  true,
		}).Error; err != nil {
		t.Fatalf("写入 GitHub 登录配置失败: %v", err)
	}

	clock := &testClock{at: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)}
	sender := &captureSender{}
	smsSender := &captureSender{}
	store := NewStore(db)
	// 存储与业务必须共用一个时钟：否则写入的 expires_at 与判断用的 now 会错位，
	// 冷却与过期这类时间相关的行为在测试里就测不准。
	store.UseClock(clock.Now)
	service, err := NewService(Options{
		Store:               store,
		EmailSender:         sender,
		SMSSender:           smsSender,
		CodeCooldownSeconds: 60,
		StateSecret:         []byte("test-state-secret"),
		Now:                 clock.Now,
	})
	if err != nil {
		t.Fatalf("装配认证服务失败: %v", err)
	}
	return &testEnv{service: service, sender: sender, sms: smsSender, clock: clock, db: db}
}

// TestPasswordHashMatchesNodeScrypt 守住与 CanvasMind 的密码哈希兼容性。
func TestPasswordHashMatchesNodeScrypt(t *testing.T) {
	stored := nodeScryptHash
	if !VerifyPassword(nodeScryptPass, &stored) {
		t.Fatal("Node 生成的 scrypt 哈希在 Go 侧验证失败，存量用户将无法登录")
	}
	if VerifyPassword("wrong password", &stored) {
		t.Fatal("错误密码不应通过验证")
	}

	generated, err := HashPassword(nodeScryptPass)
	if err != nil {
		t.Fatalf("生成密码哈希失败: %v", err)
	}
	if !strings.HasPrefix(generated, "scrypt:") {
		t.Fatalf("哈希格式与 Node 版不一致: %s", generated)
	}
	if !VerifyPassword(nodeScryptPass, &generated) {
		t.Fatal("自生成哈希无法通过验证")
	}
}

// TestEmailCodeLoginIssuesSession 覆盖「下发验证码 → 登录 → 会话可用」主链路。
func TestEmailCodeLoginIssuesSession(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// 登录不再自动建号，因此先走注册入口把账号建出来。
	registerTestAccount(t, env, "user@example.com")

	_, _, before := env.sender.last()
	if _, err := env.service.SendCode(ctx, SendCodeInput{
		MethodType: MethodEmailCode,
		Target:     "User@Example.com",
	}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	to, code, calls := env.sender.last()
	if calls != before+1 || to != "user@example.com" || len(code) != 6 {
		t.Fatalf("投递异常: to=%q codeLen=%d calls=%d", to, len(code), calls)
	}

	output, err := env.service.Login(ctx, LoginInput{
		MethodType: MethodEmailCode,
		Target:     "user@example.com",
		Code:       code,
	})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if output.Token == "" {
		t.Fatal("登录未签发会话令牌")
	}
	if output.User.Email != "user@example.com" {
		t.Fatalf("用户邮箱不正确: %q", output.User.Email)
	}

	// 令牌以摘要落库，明文不得出现在数据库里。
	tokenHash := hashSessionToken(output.Token)
	if tokenHash == output.Token {
		t.Fatal("会话令牌未哈希")
	}
	var session Session
	if err := env.db.Where("token_hash = ?", tokenHash).First(&session).Error; err != nil {
		t.Fatalf("会话记录未写入: %v", err)
	}

	current, err := env.service.SessionUser(ctx, output.Token)
	if err != nil {
		t.Fatalf("会话解析失败: %v", err)
	}
	if current.ID != output.User.ID {
		t.Fatalf("会话解析到错误用户: got=%s want=%s", current.ID, output.User.ID)
	}
}

// TestLoginResponseNeverLeaksCode 确认响应结构里不存在验证码字段。
func TestLoginResponseNeverLeaksCode(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	output, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "leak@example.com"})
	if err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	_, code, _ := env.sender.last()
	if strings.Contains(string(encoded), code) {
		t.Fatalf("下发验证码的响应体包含验证码: %s", encoded)
	}
}

// TestVerificationCodeIsSingleUse 确认验证码一次性消费。
func TestVerificationCodeIsSingleUse(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	registerTestAccount(t, env, "reuse@example.com")

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "reuse@example.com"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()

	if _, err := env.service.Login(ctx, LoginInput{MethodType: MethodEmailCode, Target: "reuse@example.com", Code: code}); err != nil {
		t.Fatalf("首次登录失败: %v", err)
	}
	if _, err := env.service.Login(ctx, LoginInput{MethodType: MethodEmailCode, Target: "reuse@example.com", Code: code}); err == nil {
		t.Fatal("同一验证码被重复使用")
	}
}

// TestSendCodeCooldown 确认冷却窗口生效，避免被刷。
func TestSendCodeCooldown(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "cool@example.com"}); err != nil {
		t.Fatalf("首次下发失败: %v", err)
	}
	_, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "cool@example.com"})
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Status != 429 {
		t.Fatalf("冷却期内应返回 429，实际: %v", err)
	}

	env.clock.Advance(61 * time.Second)
	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "cool@example.com"}); err != nil {
		t.Fatalf("冷却结束后仍无法下发: %v", err)
	}
}

// TestDisabledUserSessionRejected 确认禁用账号立即失效。
//
// CanvasMind 的会话查询不校验 status（其安全审计第 6 条），禁用用户仍可继续使用。
func TestDisabledUserSessionRejected(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	registerTestAccount(t, env, "ban@example.com")

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "ban@example.com"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()
	output, err := env.service.Login(ctx, LoginInput{MethodType: MethodEmailCode, Target: "ban@example.com", Code: code})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	if err := env.db.Model(&User{}).Where("id = ?", output.User.ID).Update("status", StatusDisabled).Error; err != nil {
		t.Fatalf("更新用户状态失败: %v", err)
	}

	_, err = env.service.SessionUser(ctx, output.Token)
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Status != 403 {
		t.Fatalf("禁用用户会话应被拒绝，实际: %v", err)
	}

	var session Session
	if err := env.db.Where("token_hash = ?", hashSessionToken(output.Token)).First(&session).Error; err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	if session.RevokedAt == nil {
		t.Fatal("禁用账号的存量会话未被吊销")
	}
}

// TestSessionAbsoluteExpiry 确认滑动续期不能让会话无限存活。
func TestSessionAbsoluteExpiry(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	registerTestAccount(t, env, "old@example.com")

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "old@example.com"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()
	output, err := env.service.Login(ctx, LoginInput{MethodType: MethodEmailCode, Target: "old@example.com", Code: code})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	// 持续活跃时滑动续期应让会话跨越 30 天的基础有效期。
	for round := 1; round <= 3; round++ {
		env.clock.Advance(25 * 24 * time.Hour)
		if _, err := env.service.SessionUser(ctx, output.Token); err != nil {
			t.Fatalf("第 %d 次滑动续期失败: %v", round, err)
		}
	}

	// 绝对上限不可突破：第 75 天的续期最多只能顶到第 90 天。
	env.clock.Advance(14 * 24 * time.Hour)
	if _, err := env.service.SessionUser(ctx, output.Token); err != nil {
		t.Fatalf("绝对上限内会话应有效: %v", err)
	}
	env.clock.Advance(2 * 24 * time.Hour)
	if _, err := env.service.SessionUser(ctx, output.Token); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("超过绝对上限应失效，实际: %v", err)
	}
}

// TestLogoutRevokesSession 确认登出后令牌立即失效。
func TestLogoutRevokesSession(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	registerTestAccount(t, env, "bye@example.com")

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "bye@example.com"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()
	output, err := env.service.Login(ctx, LoginInput{MethodType: MethodEmailCode, Target: "bye@example.com", Code: code})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if err := env.service.Logout(output.Token); err != nil {
		t.Fatalf("登出失败: %v", err)
	}
	if _, err := env.service.SessionUser(ctx, output.Token); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("登出后令牌应失效，实际: %v", err)
	}
}

// TestLoginNeverCreatesAccount 确认登录不会静默建号。
//
// 这是注册模块的前提：只要登录还能自动建号，用户就会在没有同意任何协议的情况下
// 拿到账号，协议留痕也就成了摆设。两种 allow_sign_up 取值都必须如此。
func TestLoginNeverCreatesAccount(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	for _, allowSignUp := range []bool{true, false} {
		if err := env.db.Model(&MethodConfig{}).
			Where("method_type = ?", string(MethodEmailCode)).
			Update("allow_sign_up", allowSignUp).Error; err != nil {
			t.Fatalf("更新配置失败: %v", err)
		}
		email := fmt.Sprintf("nobody-%v@example.com", allowSignUp)
		if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: email}); err != nil {
			t.Fatalf("下发验证码失败: %v", err)
		}
		_, code, _ := env.sender.last()

		_, err := env.service.Login(ctx, LoginInput{MethodType: MethodEmailCode, Target: email, Code: code})
		var authErr *Error
		if !errors.As(err, &authErr) || authErr.Status != http.StatusNotFound {
			t.Fatalf("未注册邮箱登录应返回 404（前端据此引导注册），实际: %v", err)
		}
		env.clock.Advance(61 * time.Second)
	}

	var count int64
	if err := env.db.Model(&User{}).Where("email LIKE ?", "nobody-%").Count(&count).Error; err != nil {
		t.Fatalf("统计用户失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("登录创建了 %d 个用户", count)
	}
}

// TestOAuthStateSignatureRejected 确认伪造的 state 无法通过回调校验。
func TestOAuthStateSignatureRejected(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	authorized, err := env.service.AuthorizeURL(ctx, AuthorizeInput{MethodType: MethodGithubOAuth, RedirectURI: "https://example.com/api/auth/oauth/callback"})
	if err != nil {
		t.Fatalf("生成授权地址失败: %v", err)
	}
	if !strings.Contains(authorized.AuthURL, "state="+authorized.State) {
		t.Fatalf("授权地址缺少 state: %s", authorized.AuthURL)
	}

	_, err = env.service.HandleCallback(ctx, CallbackInput{
		MethodType: MethodGithubOAuth,
		Code:       "fake-code",
		State:      "forged.payload.signature",
	})
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Status != 400 {
		t.Fatalf("伪造 state 应被拒绝，实际: %v", err)
	}

	// state 超期后同样必须拒绝。
	env.clock.Advance(oauthStateTTL + time.Minute)
	_, err = env.service.HandleCallback(ctx, CallbackInput{
		MethodType: MethodGithubOAuth,
		Code:       "fake-code",
		State:      authorized.State,
	})
	if !errors.As(err, &authErr) || authErr.Status != 400 {
		t.Fatalf("过期 state 应被拒绝，实际: %v", err)
	}
}

// TestDefaultUserNameUsesEmailLocalPart 确认邮箱自动建号时展示名取的是本地部分，
// 而不是整串邮箱的后四位（那会固定得到域名后缀，例如 "用户.com"）。
func TestDefaultUserNameUsesEmailLocalPart(t *testing.T) {
	cases := map[string]string{
		"demo-1790524787@example.com": "用户4787",
		"ab@example.com":              "用户ab",
		"13800001234":                 "用户1234",
	}
	for identifier, want := range cases {
		if got := defaultUserName(identifier); got != want {
			t.Fatalf("defaultUserName(%q) = %q, want %q", identifier, got, want)
		}
	}
}

// TestDevEchoRequiresConsoleSender 守住验证码回显的两个开关。
//
// 回显是「本地联调」专用的：只要投递通道换成了真实通道（这里用 captureSender
// 代表 SMTP），即使开关打开也不得把验证码写进响应——否则这就是 CanvasMind
// debugCode 那类认证绕过。
func TestDevEchoRequiresConsoleSender(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		devEcho bool
		sender  EmailSender
		want    bool
	}{
		{name: "开关关闭时不回显", devEcho: false, sender: ConsoleSender{}, want: false},
		{name: "开关打开且为控制台投递时回显", devEcho: true, sender: ConsoleSender{}, want: true},
		{name: "开关打开但投递通道已换成真实通道时不回显", devEcho: true, sender: &captureSender{}, want: false},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(env.db)
			store.UseClock(env.clock.Now)
			service, err := NewService(Options{
				Store:       store,
				EmailSender: tc.sender,
				DevEchoCode: tc.devEcho,
				Now:         env.clock.Now,
			})
			if err != nil {
				t.Fatalf("装配认证服务失败: %v", err)
			}
			output, err := service.SendCode(ctx, SendCodeInput{
				MethodType: MethodEmailCode,
				Target:     fmt.Sprintf("dev-echo-%d@example.com", index),
			})
			if err != nil {
				t.Fatalf("下发验证码失败: %v", err)
			}
			switch {
			case tc.want && output.DevCode == "":
				t.Fatal("期望回显验证码，实际为空")
			case !tc.want && output.DevCode != "":
				t.Fatalf("不应回显验证码，实际: %q", output.DevCode)
			}
		})
	}
}

// registerTestAccount 走真实注册入口建号，并把时钟推过冷却窗口。
//
// 登录不再自动建号，凡是需要「账号已存在」的用例都得先注册；推时钟是为了让紧随
// 其后的登录验证码不会被 60 秒下发冷却挡住。
func registerTestAccount(t *testing.T, env *testEnv, email string) *User {
	t.Helper()
	ctx := context.Background()
	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: email}); err != nil {
		t.Fatalf("下发注册验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()
	output, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodEmailCode,
		Target:           email,
		Code:             code,
		AgreementVersion: CurrentAgreementVersion(),
		RequesterIP:      "203.0.113.7",
		UserAgent:        "beef-tv-test/1.0",
	})
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	account, err := env.service.store.UserByID(output.User.ID)
	if err != nil {
		t.Fatalf("读取注册账号失败: %v", err)
	}
	env.clock.Advance(61 * time.Second)
	return account
}

// TestRegisterRecordsAgreementAndIssuesSession 确认注册落协议留痕并直接签发会话。
func TestRegisterRecordsAgreementAndIssuesSession(t *testing.T) {
	env := newTestEnv(t)
	account := registerTestAccount(t, env, "signup@example.com")

	if account.EmailValue() != "signup@example.com" {
		t.Fatalf("注册邮箱不正确: %q", account.EmailValue())
	}
	if account.Status != StatusActive {
		t.Fatalf("注册账号状态应为 ACTIVE，实际: %q", account.Status)
	}

	var records []UserAgreement
	if err := env.db.Where("user_id = ?", account.ID).Order("agreement_type asc").Find(&records).Error; err != nil {
		t.Fatalf("读取协议记录失败: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("协议留痕应为用户协议 + 隐私政策两条，实际 %d 条", len(records))
	}
	for _, record := range records {
		if record.Version != CurrentAgreementVersion() {
			t.Fatalf("协议版本不正确: %q", record.Version)
		}
		if record.AcceptedAt.IsZero() {
			t.Fatal("协议接受时间缺失，争议时无法举证")
		}
		if record.IPAddress == nil || *record.IPAddress == "" {
			t.Fatal("协议来源 IP 缺失")
		}
		if record.UserAgent == nil || *record.UserAgent == "" {
			t.Fatal("协议来源 User-Agent 缺失")
		}
	}
}

// TestRegisterRejectsStaleAgreementVersion 确认协议版本不一致时既不建号也不发会话。
func TestRegisterRejectsStaleAgreementVersion(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "stale@example.com"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()

	_, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodEmailCode,
		Target:           "stale@example.com",
		Code:             code,
		AgreementVersion: "2020-01-01",
	})
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Status != http.StatusBadRequest {
		t.Fatalf("过期协议版本应被拒绝，实际: %v", err)
	}

	var count int64
	if err := env.db.Model(&User{}).Where("email = ?", "stale@example.com").Count(&count).Error; err != nil {
		t.Fatalf("统计用户失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("协议版本不符却建了 %d 个账号", count)
	}
}

// TestRegisterRejectsWrongCode 确认错误的验证码无法建号。
func TestRegisterRejectsWrongCode(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "code@example.com"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodEmailCode,
		Target:           "code@example.com",
		Code:             "000000",
		AgreementVersion: CurrentAgreementVersion(),
	})
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Status != http.StatusUnauthorized {
		t.Fatalf("错误验证码应被拒绝，实际: %v", err)
	}

	var count int64
	if err := env.db.Model(&User{}).Where("email = ?", "code@example.com").Count(&count).Error; err != nil {
		t.Fatalf("统计用户失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("验证码错误却建了 %d 个账号", count)
	}
}

// TestRegisterRejectsExistingEmail 确认重复注册被挡，且不会烧掉验证码。
func TestRegisterRejectsExistingEmail(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	registerTestAccount(t, env, "dup@example.com")

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodEmailCode, Target: "dup@example.com"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, code, _ := env.sender.last()

	_, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodEmailCode,
		Target:           "dup@example.com",
		Code:             code,
		AgreementVersion: CurrentAgreementVersion(),
	})
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Status != http.StatusConflict {
		t.Fatalf("重复注册应返回 409，实际: %v", err)
	}

	// 判重发生在消费验证码之前：用户拿着这个码可以直接去登录。
	if _, err := env.service.Login(ctx, LoginInput{
		MethodType: MethodEmailCode,
		Target:     "dup@example.com",
		Code:       code,
	}); err != nil {
		t.Fatalf("重复注册后该验证码应仍可用于登录: %v", err)
	}
}

// TestAgreementsPayloadIsComplete 确认对外下发的协议自带版本与正文。
func TestAgreementsPayloadIsComplete(t *testing.T) {
	payload := Agreements()
	if payload.Version != CurrentAgreementVersion() {
		t.Fatalf("协议版本不一致: %q", payload.Version)
	}
	if len(payload.Documents) != 2 {
		t.Fatalf("应下发用户协议与隐私政策两份，实际 %d 份", len(payload.Documents))
	}
	for _, document := range payload.Documents {
		if strings.TrimSpace(document.Title) == "" || strings.TrimSpace(document.Body) == "" {
			t.Fatalf("协议正文缺失: %+v", document)
		}
	}
}
