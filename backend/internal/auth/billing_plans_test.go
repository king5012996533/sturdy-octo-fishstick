package auth

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// billingTestEnv 是服务层用例的最小装配：一个临时账号库 + 可推进的固定时钟。
type billingTestEnv struct {
	service *Service
	store   *Store
	clock   *testClock
}

// newBillingTestService 用真实的 SQLite 建库跑服务层。
//
// 不 mock Store：套餐的排序、唯一索引与订阅顺延都依赖 SQL 语义，用假存储测出来的
// "通过"证明不了这些行为。时钟同时注入 Store 与 Service，否则写入的 expires_at
// 与判断过期用的 now 会来自两个时间源，续期用例会测出错误结论。
func newBillingTestService(t *testing.T) *billingTestEnv {
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
	clock := &testClock{at: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)}
	store := NewStore(db)
	store.UseClock(clock.Now)
	service, err := NewService(Options{
		Store:       store,
		StateSecret: []byte("test-secret"),
		Now:         clock.Now,
	})
	if err != nil {
		t.Fatalf("装配认证服务失败: %v", err)
	}
	return &billingTestEnv{service: service, store: store, clock: clock}
}

// assertBillingError 断言返回的是带指定状态与文案的模块错误。
func assertBillingError(t *testing.T, err error, status int, message string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望报错（%d %s），实际成功", status, message)
	}
	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("期望模块错误，实际 %T: %v", err, err)
	}
	if authErr.Status != status {
		t.Fatalf("期望状态 %d，实际 %d（%s）", status, authErr.Status, authErr.Message)
	}
	if message != "" && authErr.Message != message {
		t.Fatalf("期望文案 %q，实际 %q", message, authErr.Message)
	}
}

// seedPlan 是各用例共用的建套餐入口，走的是将要被验证的 SaveBillingPlan。
func seedPlan(t *testing.T, env *billingTestEnv, input BillingPlanInput) *BillingPlanView {
	t.Helper()
	view, err := env.service.SaveBillingPlan(input)
	if err != nil {
		t.Fatalf("保存套餐失败: %v", err)
	}
	return view
}

func TestSaveBillingPlanValidation(t *testing.T) {
	env := newBillingTestService(t)

	// 非法 code：大写、空、下划线、长度超限都必须被挡在写库之前。
	for _, code := range []string{"Pro-Month", "", "pro_month", "-pro", strings.Repeat("a", 33)} {
		_, err := env.service.SaveBillingPlan(BillingPlanInput{
			Code: code, Name: "专业版", PeriodDays: 30,
		})
		assertBillingError(t, err, 400, "")
	}

	// 名称长度边界：空与超过 40 个字符都非法。
	for _, name := range []string{"", strings.Repeat("字", 41)} {
		_, err := env.service.SaveBillingPlan(BillingPlanInput{
			Code: "pro-month", Name: name, PeriodDays: 30,
		})
		assertBillingError(t, err, 400, "")
	}

	// 金额、周期、配额为负/越界都非法。
	base := BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 100, PeriodDays: 30}
	negative := base
	negative.PriceFen = -1
	assertBillingError(t, mustSaveErr(t, env, negative), 400, "")

	zeroPeriod := base
	zeroPeriod.PeriodDays = 0
	assertBillingError(t, mustSaveErr(t, env, zeroPeriod), 400, "")

	longPeriod := base
	longPeriod.PeriodDays = 3651
	assertBillingError(t, mustSaveErr(t, env, longPeriod), 400, "")

	negativeQuota := base
	negativeQuota.QuotaCalls = -1
	assertBillingError(t, mustSaveErr(t, env, negativeQuota), 400, "")

	negativeStorage := base
	negativeStorage.QuotaStorageMB = -1
	assertBillingError(t, mustSaveErr(t, env, negativeStorage), 400, "")

	negativeMembers := base
	negativeMembers.QuotaMembers = -1
	assertBillingError(t, mustSaveErr(t, env, negativeMembers), 400, "")

	// 同一个 code 不能出现第二个套餐，必须给出可展示的中文冲突提示。
	seedPlan(t, env, base)
	_, err := env.service.SaveBillingPlan(BillingPlanInput{
		Code: "pro-month", Name: "专业版二代", PeriodDays: 30,
	})
	assertBillingError(t, err, 409, "套餐标识已被占用")

	// 更新自身时 code 不变不应被判为冲突。
	saved := seedPlan(t, env, BillingPlanInput{Code: "team-year", Name: "团队版", PeriodDays: 365})
	updated, err := env.service.SaveBillingPlan(BillingPlanInput{
		ID: saved.ID, Code: "team-year", Name: "团队版（改）", PeriodDays: 365, Enabled: true,
	})
	if err != nil {
		t.Fatalf("更新自身套餐不应冲突: %v", err)
	}
	if updated.Name != "团队版（改）" {
		t.Fatalf("更新后名称应为改名值，实际 %q", updated.Name)
	}
}

func mustSaveErr(t *testing.T, env *billingTestEnv, input BillingPlanInput) error {
	t.Helper()
	_, err := env.service.SaveBillingPlan(input)
	return err
}

func TestBillingCatalogHidesDisabledPlan(t *testing.T) {
	env := newBillingTestService(t)
	seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", SortOrder: 20, PriceFen: 3900, PeriodDays: 30, Enabled: true})
	seedPlan(t, env, BillingPlanInput{Code: "legacy", Name: "老套餐", SortOrder: 10, PriceFen: 900, PeriodDays: 30, Enabled: false})
	seedPlan(t, env, BillingPlanInput{Code: "basic", Name: "基础版", SortOrder: 10, PriceFen: 1900, PeriodDays: 30, Enabled: true})

	catalog, err := env.service.BillingCatalog()
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(catalog) != 2 {
		t.Fatalf("目录应只含启用套餐（2 个），实际 %d 个", len(catalog))
	}
	// 目录顺序即运营排的货架顺序：sortOrder 优先，其次价格。
	if catalog[0].Code != "basic" || catalog[1].Code != "pro-month" {
		t.Fatalf("目录排序应为 basic、pro-month，实际 %s、%s", catalog[0].Code, catalog[1].Code)
	}

	admin, err := env.service.AdminBillingPlans()
	if err != nil {
		t.Fatalf("读取后台货架失败: %v", err)
	}
	if len(admin) != 3 {
		t.Fatalf("后台应看到全部 3 个套餐，实际 %d 个", len(admin))
	}
	// legacy 与 basic 同为 sortOrder=10，900 分的 legacy 应排在前面；
	// 后台要看到它，也要看到它是停用状态（Enabled=false）。
	if admin[0].Code != "legacy" || admin[0].Enabled {
		t.Fatalf("后台排序应为 legacy 优先且保持停用，实际首项 %s enabled=%v", admin[0].Code, admin[0].Enabled)
	}
}

func TestDeleteBillingPlanRejectsPlanWithOrders(t *testing.T) {
	env := newBillingTestService(t)
	plan := seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 3900, PeriodDays: 30, Enabled: true})

	now := env.clock.Now()
	if err := env.store.CreateBillingOrder(&BillingOrder{
		UserID:     "user-1",
		PlanID:     plan.ID,
		PlanCode:   plan.Code,
		PlanName:   plan.Name,
		AmountFen:  plan.PriceFen,
		PayableFen: plan.PriceFen,
		Status:     OrderStatusPending,
		ExpiresAt:  now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("写入测试订单失败: %v", err)
	}

	assertBillingError(t, env.service.DeleteBillingPlan(plan.ID), 409, "该套餐已有订单，请改为停用")
	// 被拒后套餐必须原样保留，否则"拒绝"反而成了静默删除。
	if _, err := env.store.BillingPlanByID(plan.ID); err != nil {
		t.Fatalf("删除被拒后套餐应仍存在: %v", err)
	}

	// 没有订单的套餐可以真删。
	free := seedPlan(t, env, BillingPlanInput{Code: "trial", Name: "试用版", PeriodDays: 7, Enabled: false})
	if err := env.service.DeleteBillingPlan(free.ID); err != nil {
		t.Fatalf("无订单套餐应可删除: %v", err)
	}
	if _, err := env.store.BillingPlanByID(free.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应查不到套餐，实际 err=%v", err)
	}
}

func TestBillingEntitlementsWithoutSubscription(t *testing.T) {
	env := newBillingTestService(t)

	view, err := env.service.BillingEntitlements("user-nobody")
	if err != nil {
		t.Fatalf("无订阅不应报错: %v", err)
	}
	if view.Active {
		t.Fatal("无订阅时 Active 应为 false")
	}
	if view.ExpiresAt != nil {
		t.Fatal("无订阅时不应有到期时间")
	}
}

func TestBillingEntitlementsWithSubscription(t *testing.T) {
	env := newBillingTestService(t)
	seedPlan(t, env, BillingPlanInput{
		Code: "pro-month", Name: "专业版", PriceFen: 3900, PeriodDays: 30, Enabled: true,
		QuotaCalls: 1000, QuotaStorageMB: 2048, QuotaMembers: 5,
	})

	view, err := env.service.GrantBillingSubscription("user-1", "pro-month", "admin-1")
	if err != nil {
		t.Fatalf("开通订阅失败: %v", err)
	}
	if !view.Active {
		t.Fatal("开通后 Active 应为 true")
	}
	if view.PlanCode != "pro-month" || view.PlanName != "专业版" {
		t.Fatalf("套餐标识/名称不符: %s / %s", view.PlanCode, view.PlanName)
	}
	if view.QuotaCalls != 1000 || view.QuotaStorageMB != 2048 || view.QuotaMembers != 5 {
		t.Fatalf("配额应来自套餐: %d / %d / %d", view.QuotaCalls, view.QuotaStorageMB, view.QuotaMembers)
	}
	wantExpires := env.clock.Now().AddDate(0, 0, 30)
	if view.ExpiresAt == nil || !view.ExpiresAt.Equal(wantExpires) {
		t.Fatalf("到期时间应为 %v，实际 %v", wantExpires, view.ExpiresAt)
	}

	// 再次查询应与开通时一致：权益是订阅 + 套餐的投影，不应依赖开通那次调用的返回值。
	again, err := env.service.BillingEntitlements("user-1")
	if err != nil {
		t.Fatalf("查询权益失败: %v", err)
	}
	if !again.Active || again.PlanCode != "pro-month" || again.QuotaCalls != 1000 {
		t.Fatalf("复查权益不一致: %+v", again)
	}
}

func TestGrantBillingSubscriptionRenewsFromCurrentExpiry(t *testing.T) {
	env := newBillingTestService(t)
	plan := seedPlan(t, env, BillingPlanInput{Code: "pro-month", Name: "专业版", PriceFen: 3900, PeriodDays: 30, Enabled: true})

	first, err := env.service.GrantBillingSubscription("user-1", plan.Code, "admin-1")
	if err != nil {
		t.Fatalf("首次开通失败: %v", err)
	}
	firstExpiry := env.clock.Now().AddDate(0, 0, 30)
	if !first.ExpiresAt.Equal(firstExpiry) {
		t.Fatalf("首次到期应为 %v，实际 %v", firstExpiry, first.ExpiresAt)
	}

	// 用掉 10 天后续期：应从剩余到期时间继续顺延 30 天（共 60 天），而不是从今天重置。
	env.clock.Advance(10 * 24 * time.Hour)
	renewed, err := env.service.GrantBillingSubscription("user-1", plan.Code, "admin-1")
	if err != nil {
		t.Fatalf("续期失败: %v", err)
	}
	wantExpiry := firstExpiry.AddDate(0, 0, 30)
	if !renewed.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("续期后到期应为 %v（顺延），实际 %v", wantExpiry, renewed.ExpiresAt)
	}
}

func TestGrantBillingSubscriptionRejectsUnknownOrDisabledPlan(t *testing.T) {
	env := newBillingTestService(t)
	seedPlan(t, env, BillingPlanInput{Code: "paused", Name: "已停用", PeriodDays: 30, Enabled: false})

	assertBillingError(t, mustGrantErr(t, env, "user-1", "no-such-plan"), 400, "套餐不存在或已停用")
	assertBillingError(t, mustGrantErr(t, env, "user-1", "paused"), 400, "套餐不存在或已停用")
	assertBillingError(t, mustGrantErr(t, env, "", "paused"), 400, "")
}

func mustGrantErr(t *testing.T, env *billingTestEnv, userID string, planCode string) error {
	t.Helper()
	_, err := env.service.GrantBillingSubscription(userID, planCode, "admin-1")
	return err
}
