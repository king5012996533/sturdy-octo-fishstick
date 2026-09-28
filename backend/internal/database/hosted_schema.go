package database

import (
	"fmt"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// MigrateHostedSharedSchema 建托管实例用到的“共享画布库”表。
//
// 这些表属于托管侧的平台账务/审计能力，不能进 LocalModels：桌面产物必须能证明
// 自己的结构里没有它们（见 bootstrap 的 TestDesktopSchemaExcludesHostedTables），
// 而本地桌面又拿不到管理员身份，永远写不到这里。它们和账号库（CanvasMind 的
// Prisma 迁移）分开，因为归属不同：这里是画布库上运维改动导致的体积增长，
// 与租户数据同批备份最省事。
//
// 调用方必须只在托管实例上调用，桌面 profile 走到这里就说明装配顺序错了。
func MigrateHostedSharedSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("托管共享结构迁移：数据库连接为空")
	}
	// 渠道/模型目录本身在 LocalModels 里（桌面端也自建渠道），因此这里只补平台
	// 专属的表：审计流水与画布审核状态。新增表时同样要问一次：桌面端是否可能产生
	// 这些行——审核状态只有管理后台能写，桌面端拿不到管理员身份。
	if err := db.AutoMigrate(&model.AdminAuditEvent{}, &model.CanvasModeration{}); err != nil {
		return fmt.Errorf("迁移托管共享结构: %w", err)
	}
	return nil
}

// RequireHostedSharedSchema 在禁用自动迁移的部署上给出可执行的报错，而不是
// 让第一个管理员写操作在运行期撞上 "no such table"。
func RequireHostedSharedSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("校验托管共享结构：数据库连接为空")
	}
	for _, entry := range []struct {
		table any
		name  string
	}{
		{&model.AdminAuditEvent{}, "admin_audit_events"},
		{&model.CanvasModeration{}, "canvas_moderation"},
	} {
		if !db.Migrator().HasTable(entry.table) {
			return fmt.Errorf("托管共享结构缺失 %s，请启用自动迁移或先建表", entry.name)
		}
	}
	return nil
}
