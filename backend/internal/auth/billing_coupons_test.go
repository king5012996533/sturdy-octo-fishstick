package auth

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newBillingCouponTestService 在本包内自建一套账号库 + 服务。
//
// 与 internal/hosted 的 newTestExtension 同路：sqlite + EnsureDevSchema。计费域的表结构
// 只在开发库由此建立（生产由 Prisma 迁移管理），测试必须走与开发完全一致的建表路径，
// 否则校验逻辑绿了、真实库里却少列。
func newBillingCouponTestService(t *testing.T) (*Service, *Store) {
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
	store := NewStore(db)
	service, err := NewService(Options{Store: store})
	if err != nil {
		t.Fatalf("装配服务失败: %v", err)
	}
	return service, store
}

// validBillingCouponInput 返回一份合法的满减券入参，各用例只改需要突出的一项。
func validBillingCouponInput() BillingCouponInput {
	start := time.Now().Add(-time.Hour)
	return BillingCouponInput{
		Code:         "KINO-2026",
		Name:         "KinoTV 新客立减",
		Kind:         CouponKindAmount,
		Value:        500,
		MinAmountFen: 0,
		StartsAt:     start,
		ExpiresAt:    start.Add(24 * time.Hour),
		Enabled:      true,
	}
}

func TestSaveBillingCouponRejectsInvalidCode(t *testing.T) {
	service, _ := newBillingCouponTestService(t)
	invalidCodes := []string{
		"",                      // 空
		"A",                     // 太短
		"AB",                    // 长度不足 3
		"A B",                   // 含空格
		"A_B",                   // 含下划线
		"-PRO",                  // 不以字母/数字开头
		"PRO@2026",              // 含非法字符
		strings.Repeat("A", 33), // 超过 32 位
	}
	for _, code := range invalidCodes {
		input := validBillingCouponInput()
		input.Code = code
		if _, err := service.SaveBillingCoupon(input); err == nil {
			t.Fatalf("券码 %q 应被拒绝", code)
		}
	}

	// 大小写与首尾空格会被归一化，不应因此被拒。
	input := validBillingCouponInput()
	input.Code = "  kino-2026  "
	if _, err := service.SaveBillingCoupon(input); err != nil {
		t.Fatalf("归一化后的合法券码不应被拒绝: %v", err)
	}
}

func TestSaveBillingCouponRejectsDuplicateCode(t *testing.T) {
	service, _ := newBillingCouponTestService(t)
	first, err := service.SaveBillingCoupon(validBillingCouponInput())
	if err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}

	dup := validBillingCouponInput()
	dup.Code = "kino-2026" // 大小写不同仍指向同一张券
	if _, err := service.SaveBillingCoupon(dup); err == nil {
		t.Fatal("重复券码应被拒绝")
	} else if !strings.Contains(err.Error(), "券码已被占用") {
		t.Fatalf("重复券码错误文案不符: %v", err)
	}

	// 改自己（同一 ID 沿用原券码）不算冲突。
	update := validBillingCouponInput()
	update.ID = first.ID
	update.Name = "KinoTV 新客立减（改）"
	if _, err := service.SaveBillingCoupon(update); err != nil {
		t.Fatalf("更新自身不应被判为重复券码: %v", err)
	}
}

func TestSaveBillingCouponRejectsZeroDiscountPercent(t *testing.T) {
	service, _ := newBillingCouponTestService(t)
	for _, value := range []int64{0, 10000, 12000} {
		input := validBillingCouponInput()
		input.Kind = CouponKindPercent
		input.Value = value
		if _, err := service.SaveBillingCoupon(input); err == nil {
			t.Fatalf("折扣率 %d 应被拒绝", value)
		}
	}

	// 边界内的折扣券应当通过。
	input := validBillingCouponInput()
	input.Kind = CouponKindPercent
	input.Value = 8000
	if _, err := service.SaveBillingCoupon(input); err != nil {
		t.Fatalf("合法折扣券不应被拒绝: %v", err)
	}

	// 满减券面额为 0 同样被拒。
	zero := validBillingCouponInput()
	zero.Value = 0
	if _, err := service.SaveBillingCoupon(zero); err == nil {
		t.Fatal("面额为 0 的满减券应被拒绝")
	}
}

func TestSaveBillingCouponRejectsInvertedWindow(t *testing.T) {
	service, _ := newBillingCouponTestService(t)

	before := validBillingCouponInput()
	before.ExpiresAt = before.StartsAt.Add(-time.Minute)
	if _, err := service.SaveBillingCoupon(before); err == nil {
		t.Fatal("结束时间早于开始时间应被拒绝")
	}

	equal := validBillingCouponInput()
	equal.ExpiresAt = equal.StartsAt
	if _, err := service.SaveBillingCoupon(equal); err == nil {
		t.Fatal("结束时间等于开始时间应被拒绝")
	}
}

func TestDeleteBillingCouponRefusesWhenRedeemed(t *testing.T) {
	service, store := newBillingCouponTestService(t)
	view, err := service.SaveBillingCoupon(validBillingCouponInput())
	if err != nil {
		t.Fatalf("创建优惠券失败: %v", err)
	}

	coupon, err := store.BillingCouponByID(view.ID)
	if err != nil {
		t.Fatalf("读取优惠券失败: %v", err)
	}
	if err := store.RedeemCoupon(*coupon, "user-1", "order-1", 500); err != nil {
		t.Fatalf("核销失败: %v", err)
	}

	if err := service.DeleteBillingCoupon(view.ID); err == nil {
		t.Fatal("已有核销记录的券不应被删除")
	} else if !strings.Contains(err.Error(), "请改为停用") {
		t.Fatalf("拒绝删除的文案不符: %v", err)
	}

	// 从未核销的券仍应被真的删除。
	fresh := validBillingCouponInput()
	fresh.Code = "FRESH-2026"
	freshView, err := service.SaveBillingCoupon(fresh)
	if err != nil {
		t.Fatalf("创建第二张券失败: %v", err)
	}
	if err := service.DeleteBillingCoupon(freshView.ID); err != nil {
		t.Fatalf("无核销记录的券应可删除: %v", err)
	}
	if _, err := store.BillingCouponByID(freshView.ID); err == nil {
		t.Fatal("删除后不应再查得到该券")
	}
}

func TestAvailableBillingCouponsFilters(t *testing.T) {
	service, store := newBillingCouponTestService(t)
	now := time.Now()

	save := func(mutate func(*BillingCouponInput)) *BillingCouponView {
		input := validBillingCouponInput()
		mutate(&input)
		view, err := service.SaveBillingCoupon(input)
		if err != nil {
			t.Fatalf("创建优惠券 %s 失败: %v", input.Code, err)
		}
		return view
	}

	usable := save(func(in *BillingCouponInput) { in.Code = "USABLE-01" })
	save(func(in *BillingCouponInput) { in.Code = "DISABLED1"; in.Enabled = false })
	exhausted := save(func(in *BillingCouponInput) { in.Code = "SOLDOUT-01"; in.TotalQuota = 1 })
	save(func(in *BillingCouponInput) {
		in.Code = "FUTURE-01"
		in.StartsAt = now.Add(time.Hour)
		in.ExpiresAt = now.Add(2 * time.Hour)
	})
	save(func(in *BillingCouponInput) {
		in.Code = "EXPIRED-01"
		in.StartsAt = now.Add(-2 * time.Hour)
		in.ExpiresAt = now.Add(-time.Hour)
	})
	limited := save(func(in *BillingCouponInput) { in.Code = "LIMITED-01"; in.PerUserLimit = 1 })

	// 已领完的券把总量用完，每人限领的券让 user-1 用掉唯一一次。
	for _, target := range []struct {
		view   *BillingCouponView
		userID string
		order  string
	}{
		{exhausted, "user-2", "order-soldout"},
		{limited, "user-1", "order-limited"},
	} {
		coupon, err := store.BillingCouponByID(target.view.ID)
		if err != nil {
			t.Fatalf("读取优惠券失败: %v", err)
		}
		if err := store.RedeemCoupon(*coupon, target.userID, target.order, 500); err != nil {
			t.Fatalf("核销失败: %v", err)
		}
	}

	codesFor := func(userID string) map[string]bool {
		views, err := service.AvailableBillingCoupons(userID)
		if err != nil {
			t.Fatalf("查询可用券失败: %v", err)
		}
		codes := make(map[string]bool, len(views))
		for _, view := range views {
			codes[view.Code] = true
		}
		return codes
	}

	// user-1：已用完每人限领，应当只剩 USABLE-01。
	user1 := codesFor("user-1")
	if len(user1) != 1 || !user1[usable.Code] {
		t.Fatalf("user-1 可用券应为 [%s]，实际 %v", usable.Code, user1)
	}

	// user-2：没触发每人限领，但 SOLDOUT-01 全局已领完，仍应被排除。
	user2 := codesFor("user-2")
	if len(user2) != 2 || !user2[usable.Code] || !user2[limited.Code] {
		t.Fatalf("user-2 可用券应为 [%s %s]，实际 %v", usable.Code, limited.Code, user2)
	}
	for _, excluded := range []string{"DISABLED1", "SOLDOUT-01", "FUTURE-01", "EXPIRED-01"} {
		if user2[excluded] {
			t.Fatalf("不应返回券 %s，实际 %v", excluded, user2)
		}
	}
}

func TestCouponRedemptionsClampsPaging(t *testing.T) {
	service, store := newBillingCouponTestService(t)
	view, err := service.SaveBillingCoupon(validBillingCouponInput())
	if err != nil {
		t.Fatalf("创建优惠券失败: %v", err)
	}
	coupon, err := store.BillingCouponByID(view.ID)
	if err != nil {
		t.Fatalf("读取优惠券失败: %v", err)
	}
	for _, order := range []string{"order-1", "order-2", "order-3"} {
		if err := store.RedeemCoupon(*coupon, "user-1", order, 500); err != nil {
			t.Fatalf("核销失败: %v", err)
		}
	}

	// 非法 page（<=0 归 1）与超大 pageSize（夹到 200）都应当拿到完整一批。
	records, total, err := service.CouponRedemptions(CouponRedemptionFilter{CouponID: view.ID, Page: 0, PageSize: 9999})
	if err != nil {
		t.Fatalf("查询核销流水失败: %v", err)
	}
	if total != 3 || len(records) != 3 {
		t.Fatalf("核销流水应为 3 条，实际 total=%d len=%d", total, len(records))
	}
}
