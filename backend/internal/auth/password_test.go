package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClassifyPasswordTarget(t *testing.T) {
	cases := []struct {
		input string
		kind  passwordTargetKind
		want  string
		valid bool
	}{
		{input: "13800138000", kind: targetPhone, want: "13800138000", valid: true},
		{input: "+86 138-0013-8000", kind: targetPhone, want: "13800138000", valid: true},
		{input: " 8613800138000 ", kind: targetPhone, want: "13800138000", valid: true},
		{input: "You@Example.COM", kind: targetEmail, want: "you@example.com", valid: true},
		{input: " you@example.com ", kind: targetEmail, want: "you@example.com", valid: true},
		{input: "", valid: false},
		{input: "nobody", valid: false},
		{input: "12345", valid: false},
		{input: "you@", valid: false},
	}
	for _, tc := range cases {
		kind, got, err := classifyPasswordTarget(tc.input)
		if tc.valid {
			if err != nil {
				t.Fatalf("%q 应通过，却报错: %v", tc.input, err)
			}
			if kind != tc.kind || got != tc.want {
				t.Fatalf("%q 归一为 (%v, %q)，期望 (%v, %q)", tc.input, kind, got, tc.kind, tc.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%q 应被拒绝", tc.input)
		}
		assertAuthError(t, err, http.StatusBadRequest, "invalid_argument")
	}
}

func TestValidatePassword(t *testing.T) {
	// 策略只有长度与空白两道门槛：不强制字符类别，因此"纯数字 8 位"这类口令必须放行。
	// 这条用例是那个决策的锚点——把复杂度规则加回来时它会立刻失败。
	valid := []string{"abc12345", "a1234567", "P@ssw0rd!", "abcdefg1", "12345678", "abcdefgh", "密码密码密码密码"}
	for _, password := range valid {
		if err := validatePassword(password); err != nil {
			t.Fatalf("%q 应通过，却报错: %v", password, err)
		}
	}
	invalid := []string{
		"",                       // 空
		"abc123",                 // 太短
		"abcdefg",                // 7 位，差一位也要拒
		"abc 12345",              // 含空格
		"abc\t12345",             // 含制表符
		strings.Repeat("a1", 33), // 66 位，超长
	}
	for _, password := range invalid {
		if err := validatePassword(password); err == nil {
			t.Fatalf("%q 应被拒绝", password)
		}
	}
}

func TestPasswordRegisterAndLogin(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	output, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodPassword,
		Target:           "+86 138-0013-8000",
		Password:         "abc12345",
		AgreementVersion: CurrentAgreementVersion(),
		RequesterIP:      "203.0.113.9",
		UserAgent:        "beef-tv-test/1.0",
	})
	if err != nil {
		t.Fatalf("密码注册失败: %v", err)
	}
	if output.Token == "" {
		t.Fatal("注册未签发会话令牌")
	}

	// 密码必须落库成哈希，而且要和 CanvasMind 的存量格式一致。
	account, err := env.service.store.UserByPhone("13800138000")
	if err != nil {
		t.Fatalf("注册未落到 phone 列: %v", err)
	}
	if account.PasswordHash == nil || !VerifyPassword("abc12345", account.PasswordHash) {
		t.Fatal("密码未按 scrypt 格式落库")
	}
	if account.PasswordHash != nil && *account.PasswordHash == "abc12345" {
		t.Fatal("密码被明文落库")
	}
	if account.Email != nil {
		t.Fatal("手机号注册不应写 email 列")
	}

	// 密码注册没有验证过标识归属，identity 不能标成已核验。
	identity, err := env.service.store.IdentityByIdentifier(MethodPassword, "13800138000")
	if err != nil {
		t.Fatalf("缺少密码身份绑定: %v", err)
	}
	if identity.IsVerified {
		t.Fatal("密码通道未验证标识归属，identity 不应标为已核验")
	}
	if err := env.service.recordAgreements(account.ID, CurrentAgreementVersion(), "", ""); err != nil {
		t.Fatalf("协议留痕探针失败: %v", err)
	}

	// 换一种写法登录必须命中同一个账号。
	login, err := env.service.Login(ctx, LoginInput{
		MethodType: MethodPassword,
		Target:     "138 0013 8000",
		Password:   "abc12345",
	})
	if err != nil {
		t.Fatalf("密码登录失败: %v", err)
	}
	if login.User.ID != output.User.ID {
		t.Fatalf("登录命中了不同账号: %s != %s", login.User.ID, output.User.ID)
	}
}

func TestPasswordRegisterWritesAgreements(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	output, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodPassword,
		Target:           "13511112222",
		Password:         "abc12345",
		AgreementVersion: CurrentAgreementVersion(),
	})
	if err != nil {
		t.Fatalf("密码注册失败: %v", err)
	}

	var count int64
	if err := env.db.Model(&UserAgreement{}).Where("user_id = ?", output.User.ID).Count(&count).Error; err != nil {
		t.Fatalf("统计协议记录失败: %v", err)
	}
	if count != int64(len(Agreements().Documents)) {
		t.Fatalf("协议留痕 %d 条，期望 %d 条", count, len(Agreements().Documents))
	}
}

func TestPasswordRegisterRejectsExistingIdentifier(t *testing.T) {
	env := newTestEnv(t)

	if _, err := env.service.Register(context.Background(), RegisterInput{
		MethodType:       MethodPassword,
		Target:           "you@example.com",
		Password:         "abc12345",
		AgreementVersion: CurrentAgreementVersion(),
	}); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	_, err := env.service.Register(context.Background(), RegisterInput{
		MethodType:       MethodPassword,
		Target:           "you@example.com",
		Password:         "xyz98765",
		AgreementVersion: CurrentAgreementVersion(),
	})
	assertAuthError(t, err, http.StatusConflict, "conflict")
}

// TestPasswordRegisterRejectsIdentifierOwnedByCodeChannel 覆盖跨通道判重。
//
// 同一个手机号不能既有一个验证码账号又有一个密码账号：唯一索引会拦住写入，但那样
// 用户看到的是 500，而不是「这个号已经注册过」。
func TestPasswordRegisterRejectsIdentifierOwnedByCodeChannel(t *testing.T) {
	env := newTestEnv(t)
	registerTestPhone(t, env, "13622223333")

	_, err := env.service.Register(context.Background(), RegisterInput{
		MethodType:       MethodPassword,
		Target:           "13622223333",
		Password:         "abc12345",
		AgreementVersion: CurrentAgreementVersion(),
	})
	assertAuthError(t, err, http.StatusConflict, "conflict")
}

// TestPasswordLoginDoesNotRevealAccountExistence 确认「不存在」与「密码错」不可区分。
func TestPasswordLoginDoesNotRevealAccountExistence(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	_, missingErr := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13900000000", Password: "abc12345"})
	_, wrongErr := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13900000001", Password: "abc12345"})

	assertAuthError(t, missingErr, http.StatusUnauthorized, "unauthorized")
	assertAuthError(t, wrongErr, http.StatusUnauthorized, "unauthorized")
	if missingErr.Error() != wrongErr.Error() {
		t.Fatalf("账号存在性可从文案区分: %q vs %q", missingErr.Error(), wrongErr.Error())
	}
}

// TestPasswordLoginNeverBindsIdentity 确认失败登录不写库。
//
// resolveExistingUser 会给存量账号补 identity 绑定，登录又是未认证入口；如果复用它，
// 任何人都能用不存在的账号无限触发写入。
func TestPasswordLoginNeverBindsIdentity(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	account := registerTestPhone(t, env, "13444445555")

	for i := 0; i < 3; i++ {
		_, err := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13444445555", Password: "abc12345"})
		assertAuthError(t, err, http.StatusUnauthorized, "unauthorized")
	}

	var count int64
	if err := env.db.Model(&AuthIdentity{}).
		Where("user_id = ? AND method_type = ?", account.ID, string(MethodPassword)).
		Count(&count).Error; err != nil {
		t.Fatalf("统计身份绑定失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("失败登录不应写身份绑定，实际写入 %d 条", count)
	}
}

// TestPasswordLoginFallsBackToIdentifierColumn 覆盖「验证码注册的老用户后来设了密码」。
func TestPasswordLoginFallsBackToIdentifierColumn(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	account := registerTestPhone(t, env, "13333334444")

	hash, err := HashPassword("abc12345")
	if err != nil {
		t.Fatalf("生成密码哈希失败: %v", err)
	}
	if err := env.db.Model(&User{}).Where("id = ?", account.ID).Update("password_hash", hash).Error; err != nil {
		t.Fatalf("写入密码哈希失败: %v", err)
	}

	output, err := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13333334444", Password: "abc12345"})
	if err != nil {
		t.Fatalf("没有 PASSWORD 身份绑定时应回落到按列查找: %v", err)
	}
	if output.User.ID != account.ID {
		t.Fatalf("登录命中了不同账号: %s != %s", output.User.ID, account.ID)
	}
}

// TestPasswordLoginThrottlesFailures 覆盖失败节流与窗口恢复。
func TestPasswordLoginThrottlesFailures(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	if _, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodPassword,
		Target:           "13222223333",
		Password:         "abc12345",
		AgreementVersion: CurrentAgreementVersion(),
	}); err != nil {
		t.Fatalf("密码注册失败: %v", err)
	}

	for i := 0; i < passwordFailureLimit; i++ {
		_, err := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13222223333", Password: "wrong9999"})
		assertAuthError(t, err, http.StatusUnauthorized, "unauthorized")
	}
	// 第 6 次必须被节流挡住，而不是继续拿到 401：爆破成本就卡在这里。
	_, err := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13222223333", Password: "abc12345"})
	assertAuthError(t, err, http.StatusTooManyRequests, "rate_limited")

	// 超过窗口后恢复，否则正常用户会被自己历史上的几次手误永久锁在门外。
	env.clock.Advance(passwordFailureWindow + time.Minute)
	if _, err := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13222223333", Password: "abc12345"}); err != nil {
		t.Fatalf("窗口过期后应恢复登录: %v", err)
	}
	// 成功登录要清空计数：否则用户下次再手误一次就会被已有记录推到上限。
	if _, err := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13222223333", Password: "wrong9999"}); err == nil {
		t.Fatal("错误密码不应登录成功")
	}
	if _, err := env.service.Login(ctx, LoginInput{MethodType: MethodPassword, Target: "13222223333", Password: "abc12345"}); err != nil {
		t.Fatalf("成功登录后计数应被清空: %v", err)
	}
}

func TestValidatePasswordRejectsMissingPasswordOnRegister(t *testing.T) {
	env := newTestEnv(t)
	_, err := env.service.Register(context.Background(), RegisterInput{
		MethodType:       MethodPassword,
		Target:           "13111112222",
		AgreementVersion: CurrentAgreementVersion(),
	})
	assertAuthError(t, err, http.StatusBadRequest, "invalid_argument")
}
