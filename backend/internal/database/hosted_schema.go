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
	// 专属的表：审计流水、画布审核状态、素材处置状态、运营维护的画布模板，以及平台
	// 视角的厂商与厂商凭据。新增表时同样要问一次：桌面端是否可能产生这些行——这些
	// 表都只有管理后台能写，桌面端拿不到管理员身份。精选灵感同理：它是运营维护的
	// 广场内容，桌面端只读它自己仓库里那份本地列表。
	//
	// 厂商凭据刻意只存"指向某条 system channel"的指针与展示用尾号，密钥本体仍留在
	// model_channels（那里已经有一套加密与脱敏），避免同一条密钥两处各存一份。
	// 模型广场文案同理：它是运营维护的对外内容，桌面端没有广场，只读它自己仓库里那
	// 份本地文案。
	if err := db.AutoMigrate(
		&model.AdminAuditEvent{},
		&model.CanvasModeration{},
		&model.AssetModeration{},
		&model.CanvasTemplate{},
		&model.ModelVendor{},
		&model.VendorCredential{},
		&model.CreationInspiration{},
		&model.ModelShowcaseEntry{},
	); err != nil {
		return fmt.Errorf("迁移托管共享结构: %w", err)
	}
	// 灵感广场从"运营独占"扩成"运营精选 + 用户投稿"时新增了来源与审核结论两列。
	// 存量行都是运营手工录入的平台内容，这里补上取值，让前台的可见性判断不必再写
	// 一套 NULL 兼容分支——"空来源"一旦要在查询里兜底，就会在每个新查询里再兜一次。
	// 语句按"来源为空"过滤，重复执行无副作用。
	if err := db.Model(&model.CreationInspiration{}).
		Where("origin IS NULL OR origin = ?", "").
		Updates(map[string]any{
			"origin":        model.CreationInspirationOriginPlatform,
			"review_status": model.CreationInspirationReviewApproved,
		}).Error; err != nil {
		return fmt.Errorf("回填灵感广场来源: %w", err)
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
		{&model.AssetModeration{}, "asset_moderation"},
		{&model.CanvasTemplate{}, "canvas_templates"},
		{&model.ModelVendor{}, "model_vendors"},
		{&model.VendorCredential{}, "vendor_credentials"},
		{&model.CreationInspiration{}, "creation_inspirations"},
	} {
		if !db.Migrator().HasTable(entry.table) {
			return fmt.Errorf("托管共享结构缺失 %s，请启用自动迁移或先建表", entry.name)
		}
	}
	return nil
}
