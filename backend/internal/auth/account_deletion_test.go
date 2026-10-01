package auth

import (
	"errors"
	"testing"
	"time"
)

// 注销域的测试环境：账号 + 验证码 + 注销申请表齐备。
func newAccountDeletionEnv(t *testing.T) *billingTestEnv {
	t.Helper()
	env := newBillingTestService(t)
	if err := EnsureAccountDeletionSchema(env.store.db); err != nil {
		t.Fatalf("初始化注销表失败: %v", err)
	}
	return env
}

// seedDeletionCandidate 建一个可注销的账号：邮箱已验证、状态正常，另挂一条会话
// 与一条第三方身份绑定，用来验证注销时它们会被一起清掉。
func seedDeletionCandidate(t *testing.T, env *billingTestEnv, id string, email string) {
	t.Helper()
	name := "测试用户"
	if err := env.store.CreateUser(&User{ID: id, Name: &name, Email: &email, Status: StatusActive}); err != nil {
		t.Fatalf("建账号失败: %v", err)
	}
	if err := env.store.db.Create(&Session{
		ID:             "session-" + id,
		UserID:         id,
		TokenHash:      "hash-" + id,
		AuthMethodType: MethodEmailCode,
		ExpiresAt:      env.clock.Now().Add(24 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	if err := env.store.db.Create(&AuthIdentity{
		ID:         "identity-" + id,
		UserID:     id,
		MethodType: MethodEmailCode,
		Identifier: email,
	}).Error; err != nil {
		t.Fatalf("建身份绑定失败: %v", err)
	}
}

// seedLoginCode 落一条可用的登录场景验证码，供注销申请消费。
func seedLoginCode(t *testing.T, env *billingTestEnv, methodType MethodType, target string) {
	t.Helper()
	if err := env.store.CreateVerificationCode(&VerificationCode{
		MethodType: methodType,
		Channel:    ChannelEmail,
		Scene:      codeScene,
		Target:     target,
		Code:       "123456",
		ExpiresAt:  env.clock.Now().Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("写验证码失败: %v", err)
	}
}

func TestEnsureAccountDeletionSchemaIsIdempotent(t *testing.T) {
	env := newAccountDeletionEnv(t)
	if err := EnsureAccountDeletionSchema(env.store.db); err != nil {
		t.Fatalf("重复建表应幂等，实际报错: %v", err)
	}
}

// TestRequestAccountDeletionOnlyAcceptsOwnBoundTarget 覆盖「用别人的验证码注销不了」。
//
// 注销请求里的目标不参与校验，验证码必须落在账号自己绑定的邮箱上：否则任何人只要
// 有一个收得到验证码的邮箱，就能把别人的账号注销掉。
func TestRequestAccountDeletionOnlyAcceptsOwnBoundTarget(t *testing.T) {
	env := newAccountDeletionEnv(t)
	seedDeletionCandidate(t, env, "user-1", "owner@example.com")
	seedLoginCode(t, env, MethodEmailCode, "attacker@example.com")

	_, err := env.service.RequestAccountDeletion(t.Context(), AccountDeletionRequest{
		UserID:     "user-1",
		MethodType: MethodEmailCode,
		Code:       "123456",
	})
	assertBillingError(t, err, 400, "验证码不正确或已过期")
}

func TestRequestAccountDeletionRequiresBoundChannel(t *testing.T) {
	env := newAccountDeletionEnv(t)
	name := "无邮箱用户"
	if err := env.store.CreateUser(&User{ID: "user-2", Name: &name, Status: StatusActive}); err != nil {
		t.Fatalf("建账号失败: %v", err)
	}
	_, err := env.service.RequestAccountDeletion(t.Context(), AccountDeletionRequest{
		UserID:     "user-2",
		MethodType: MethodEmailCode,
		Code:       "123456",
	})
	assertBillingError(t, err, 400, "该账号未绑定邮箱，无法用邮箱验证码确认身份")
}

// TestRequestAccountDeletionConsumesCodeOnce 覆盖验证码一次性：注销申请被重放时，
// 第二次必须因为没有可用验证码而失败，而不是静默延长冷静期。
func TestRequestAccountDeletionConsumesCodeOnce(t *testing.T) {
	env := newAccountDeletionEnv(t)
	seedDeletionCandidate(t, env, "user-1", "owner@example.com")
	seedLoginCode(t, env, MethodEmailCode, "owner@example.com")

	first, err := env.service.RequestAccountDeletion(t.Context(), AccountDeletionRequest{
		UserID:     "user-1",
		MethodType: MethodEmailCode,
		Code:       "123456",
	})
	if err != nil {
		t.Fatalf("首次申请应成功，实际: %v", err)
	}
	if first.Status != AccountDeletionPending || first.ScheduledAt == nil {
		t.Fatalf("应返回待执行状态与到期时间，实际 %#v", first)
	}
	if want := env.clock.Now().AddDate(0, 0, AccountDeletionGraceDays); !first.ScheduledAt.Equal(want) {
		t.Fatalf("到期时间应是 %s，实际 %s", want, first.ScheduledAt)
	}

	_, err = env.service.RequestAccountDeletion(t.Context(), AccountDeletionRequest{
		UserID:     "user-1",
		MethodType: MethodEmailCode,
		Code:       "123456",
	})
	assertBillingError(t, err, 400, "验证码不正确或已过期")
}

// TestRequestAccountDeletionKeepsSingleActiveRequest 覆盖重复申请只保留一条待执行。
//
// 两条并存会让到期扫描对同一个人跑两遍匿名化，第二遍必然失败。
func TestRequestAccountDeletionKeepsSingleActiveRequest(t *testing.T) {
	env := newAccountDeletionEnv(t)
	seedDeletionCandidate(t, env, "user-1", "owner@example.com")

	seedLoginCode(t, env, MethodEmailCode, "owner@example.com")
	if _, err := env.service.RequestAccountDeletion(t.Context(), AccountDeletionRequest{
		UserID: "user-1", MethodType: MethodEmailCode, Code: "123456",
	}); err != nil {
		t.Fatalf("首次申请失败: %v", err)
	}
	env.clock.Advance(48 * time.Hour)
	seedLoginCode(t, env, MethodEmailCode, "owner@example.com")
	if _, err := env.service.RequestAccountDeletion(t.Context(), AccountDeletionRequest{
		UserID: "user-1", MethodType: MethodEmailCode, Code: "123456",
	}); err != nil {
		t.Fatalf("二次申请失败: %v", err)
	}

	var pending int64
	if err := env.store.db.Model(&AccountDeletion{}).
		Where("user_id = ? AND status = ?", "user-1", AccountDeletionPending).
		Count(&pending).Error; err != nil {
		t.Fatalf("统计待执行申请失败: %v", err)
	}
	if pending != 1 {
		t.Fatalf("待执行申请应恒为 1 条，实际 %d", pending)
	}
	// 冷静期必须重新计时：否则用户可以用二次申请把到期时间往前挪。
	active, err := env.store.ActiveAccountDeletion("user-1")
	if err != nil {
		t.Fatalf("读取待执行申请失败: %v", err)
	}
	if want := env.clock.Now().AddDate(0, 0, AccountDeletionGraceDays); !active.ScheduledAt.Equal(want) {
		t.Fatalf("冷静期应重新计时到 %s，实际 %s", want, active.ScheduledAt)
	}
}

func TestExecuteDueAccountDeletionAnonymizesUser(t *testing.T) {
	env := newAccountDeletionEnv(t)
	seedDeletionCandidate(t, env, "user-1", "owner@example.com")
	seedLoginCode(t, env, MethodEmailCode, "owner@example.com")
	if _, err := env.service.RequestAccountDeletion(t.Context(), AccountDeletionRequest{
		UserID: "user-1", MethodType: MethodEmailCode, Code: "123456", Reason: "不再使用",
	}); err != nil {
		t.Fatalf("申请注销失败: %v", err)
	}

	// 冷静期未到时不能执行：这是用户撤销的唯一窗口。
	if count, err := env.service.ExecuteDueAccountDeletions(0); err != nil || count != 0 {
		t.Fatalf("冷静期内不应执行，实际 count=%d err=%v", count, err)
	}

	env.clock.Advance(time.Duration(AccountDeletionGraceDays+1) * 24 * time.Hour)
	count, err := env.service.ExecuteDueAccountDeletions(0)
	if err != nil {
		t.Fatalf("执行到期注销失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("应执行 1 条，实际 %d", count)
	}

	user, err := env.store.UserByID("user-1")
	if err != nil {
		t.Fatalf("账号行必须保留（账本与订单按 user_id 外键指向它），实际: %v", err)
	}
	if user.Email != nil || user.Phone != nil || user.Username != nil {
		t.Fatalf("可识别字段应全部清空，实际 email=%v phone=%v username=%v", user.Email, user.Phone, user.Username)
	}
	if user.Name == nil || *user.Name != accountDeletedDisplayName {
		t.Fatalf("展示名应被匿名化，实际 %v", user.Name)
	}
	if user.Status != StatusDisabled {
		t.Fatalf("注销后状态应为 DISABLED，实际 %s", user.Status)
	}

	var sessions int64
	if err := env.store.db.Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL", "user-1").
		Count(&sessions).Error; err != nil {
		t.Fatalf("统计会话失败: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("注销必须吊销全部会话，仍有 %d 条有效", sessions)
	}
	var identities int64
	if err := env.store.db.Model(&AuthIdentity{}).Where("user_id = ?", "user-1").Count(&identities).Error; err != nil {
		t.Fatalf("统计身份绑定失败: %v", err)
	}
	if identities != 0 {
		t.Fatalf("第三方身份绑定应被删除，仍有 %d 条", identities)
	}

	// 重复扫描不能把已执行的申请再跑一遍。
	if count, err := env.service.ExecuteDueAccountDeletions(0); err != nil || count != 0 {
		t.Fatalf("已执行的申请不应重复执行，实际 count=%d err=%v", count, err)
	}
}

func TestCancelAccountDeletionKeepsAccountUsable(t *testing.T) {
	env := newAccountDeletionEnv(t)
	seedDeletionCandidate(t, env, "user-1", "owner@example.com")
	seedLoginCode(t, env, MethodEmailCode, "owner@example.com")
	if _, err := env.service.RequestAccountDeletion(t.Context(), AccountDeletionRequest{
		UserID: "user-1", MethodType: MethodEmailCode, Code: "123456",
	}); err != nil {
		t.Fatalf("申请注销失败: %v", err)
	}

	view, err := env.service.CancelAccountDeletion("user-1")
	if err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	if view.Status != AccountDeletionCancelled {
		t.Fatalf("撤销后状态应为 %s，实际 %s", AccountDeletionCancelled, view.Status)
	}
	if _, err := env.service.CancelAccountDeletion("user-1"); err == nil {
		t.Fatal("没有待执行申请时撤销应报错，否则界面上会出现“撤销成功但什么都没发生”")
	}

	env.clock.Advance(time.Duration(AccountDeletionGraceDays+1) * 24 * time.Hour)
	if count, err := env.service.ExecuteDueAccountDeletions(0); err != nil || count != 0 {
		t.Fatalf("已撤销的申请不应被执行，实际 count=%d err=%v", count, err)
	}
	user, err := env.store.UserByID("user-1")
	if err != nil {
		t.Fatalf("撤销后账号必须还在，实际: %v", err)
	}
	if user.Email == nil || user.Status != StatusActive {
		t.Fatalf("撤销后账号应保持原样，实际 email=%v status=%s", user.Email, user.Status)
	}
	status, err := env.service.AccountDeletionStatus("user-1")
	if err != nil {
		t.Fatalf("读取注销状态失败: %v", err)
	}
	if status.Status != "NONE" {
		t.Fatalf("撤销后状态应为 NONE，实际 %s", status.Status)
	}
}

// TestExecuteAccountDeletionSkipsAlreadyCancelledRow 覆盖并发下的竞态：撤销与执行
// 同时发生时，晚到的那次执行必须拿这个账号没办法。
func TestExecuteAccountDeletionSkipsAlreadyCancelledRow(t *testing.T) {
	env := newAccountDeletionEnv(t)
	seedDeletionCandidate(t, env, "user-1", "owner@example.com")
	record := &AccountDeletion{
		ID:          "deletion-1",
		UserID:      "user-1",
		Status:      AccountDeletionPending,
		MethodType:  string(MethodEmailCode),
		RequestedAt: env.clock.Now(),
		ScheduledAt: env.clock.Now().AddDate(0, 0, AccountDeletionGraceDays),
	}
	if err := env.store.CreateAccountDeletion(record); err != nil {
		t.Fatalf("落申请失败: %v", err)
	}
	if _, err := env.store.CancelAccountDeletion("user-1", env.clock.Now()); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	err := env.store.ExecuteAccountDeletion(record.ID, record.UserID, env.clock.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("已撤销的申请应返回 ErrNotFound，实际 %v", err)
	}
	user, err := env.store.UserByID("user-1")
	if err != nil || user.Email == nil {
		t.Fatalf("账号不应被匿名化，实际 err=%v user=%#v", err, user)
	}
}
