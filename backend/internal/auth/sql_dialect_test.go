package auth

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestNormalizeAuthDialect(t *testing.T) {
	cases := map[string]string{
		"sqlite":     "sqlite",
		"SQLite":     "sqlite",
		" mysql ":    "mysql",
		"postgres":   "postgres",
		"postgresql": "postgres",
		"pgx":        "postgres",
		"":           "sqlite",
		"mariadb":    "sqlite",
	}
	for input, want := range cases {
		if got := normalizeAuthDialect(input); got != want {
			t.Errorf("normalizeAuthDialect(%q) = %q, want %q", input, got, want)
		}
	}
}

// SQLite 的 MAX(a, b) 是标量函数，MySQL/PostgreSQL 的 MAX() 是聚合函数，
// 双参数写法会在那边直接报错，必须换成 GREATEST()。
func TestScalarMaxExprPerDialect(t *testing.T) {
	if got := scalarMaxExpr("sqlite", "t.uncollected", "?"); got != "MAX(t.uncollected, ?)" {
		t.Fatalf("sqlite = %q", got)
	}
	for _, driver := range []string{"mysql", "postgres"} {
		if got := scalarMaxExpr(driver, "t.uncollected", "?"); got != "GREATEST(t.uncollected, ?)" {
			t.Fatalf("%s = %q, want GREATEST", driver, got)
		}
	}
}

func TestScalarMaxExprNeverUsesTwoArgMaxOffSQLite(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		if expr := scalarMaxExpr(driver, "t.uncollected", "?"); expr[:3] == "MAX" {
			t.Fatalf("%s 不应使用双参数 MAX", driver)
		}
	}
}

// 重放收尾时欠款只能变大不能变小——这正是 MAX/GREATEST 的语义，必须实测而不是只测字符串。
func TestRecordCreditSettleGapKeepsLargerUncollected(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&CreditSettleGap{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	store := NewStore(db)

	if err := store.RecordCreditSettleGap(CreditSettleGap{TaskID: "task-1", UserID: "u1", ModelKey: "m", Uncollected: 50}); err != nil {
		t.Fatalf("首次记录失败: %v", err)
	}
	// 较小的值不得覆盖
	if err := store.RecordCreditSettleGap(CreditSettleGap{TaskID: "task-1", UserID: "u1", ModelKey: "m", Uncollected: 20}); err != nil {
		t.Fatalf("重放记录失败: %v", err)
	}
	var gap CreditSettleGap
	if err := db.Where("task_id = ?", "task-1").First(&gap).Error; err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if gap.Uncollected != 50 {
		t.Fatalf("Uncollected = %d, want 50（小值不应覆盖大值）", gap.Uncollected)
	}

	// 较大的值要更新上去
	if err := store.RecordCreditSettleGap(CreditSettleGap{TaskID: "task-1", UserID: "u1", ModelKey: "m", Uncollected: 80}); err != nil {
		t.Fatalf("重放记录失败: %v", err)
	}
	if err := db.Where("task_id = ?", "task-1").First(&gap).Error; err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if gap.Uncollected != 80 {
		t.Fatalf("Uncollected = %d, want 80", gap.Uncollected)
	}

	var count int64
	if err := db.Model(&CreditSettleGap{}).Where("task_id = ?", "task-1").Count(&count).Error; err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("同一任务应只有一条欠款记录，得到 %d 条", count)
	}
}
