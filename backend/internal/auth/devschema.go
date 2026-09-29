package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"gorm.io/gorm"
)

// EnsureDevSchema 仅在本地 SQLite 上建表，用于在没有 MySQL 的环境跑通登录链路。
//
// 这里刻意用驱动白名单而不是「TryAutoMigrate」：账号表由 CanvasMind 的 Prisma
// 迁移管理，一旦让 GORM 碰到共享的 MySQL 库，它可能按自己的类型推断改写列定义。
// 生产路径必须走 Prisma 迁移。
func EnsureDevSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("auth: 数据库连接为空")
	}
	if name := db.Dialector.Name(); name != "sqlite" {
		return fmt.Errorf("auth: 拒绝为 %s 驱动创建账号表；该库的表结构由 Prisma 迁移管理", name)
	}
	// 计费域（套餐/订阅/订单/优惠券）同样只在开发库建表：它们的生产结构也要落到
	// CanvasMind 的 Prisma 迁移里，理由与账号表一致。
	models := []any{
		&User{},
		&Session{},
		&VerificationCode{},
		&AuthIdentity{},
		&MethodConfig{},
		&UserAgreement{},
		&AgreementVersion{},
		&GatewayConfig{},
	}
	models = append(models, BillingModels()...)
	// 角色权限与工单反馈同属账号侧平台能力：它们只在托管实例里被读写（本地桌面
	// 拿不到管理员身份，也没有客服台），生产结构同样归 CanvasMind 的 Prisma 迁移。
	models = append(models, RbacModels()...)
	models = append(models, SupportModels()...)
	// 模型定价与倍率是商业化域的数据：单价占位、倍率规则都归运营配置，生产结构同归
	// CanvasMind 的 Prisma 迁移。
	models = append(models, PricingModels()...)
	// 积分账户与流水同样只在开发库建表：生产结构归 Prisma 迁移，理由与上面几个域一致。
	models = append(models, CreditModels()...)
	if err := db.AutoMigrate(models...); err != nil {
		return err
	}

	// 三个域各自还要建唯一索引（角色 code、账号-角色、工单号、模型-能力、规则作用域-目标）：
	// AutoMigrate 只按结构体标签建索引，而它们的模型刻意不挂标签（这些表归 Prisma 迁移所有），
	// 索引由 Ensure<域>Schema 用幂等 SQL 声明。少调这一步，读取时的 ON CONFLICT 会直接报
	// "不匹配任何唯一约束"。
	if err := EnsureRbacSchema(db); err != nil {
		return err
	}
	if err := EnsureSupportSchema(db); err != nil {
		return err
	}
	if err := EnsurePricingSchema(db); err != nil {
		return err
	}
	if err := EnsureCreditSchema(db); err != nil {
		return err
	}

	// AutoMigrate 只按结构体标签建索引，而这里刻意不给模型挂标签：这些表归 Prisma
	// 迁移所有，标签一旦和真实 DDL 漂移，宿主就可能误以为结构由自己控制。索引改用
	// 幂等 SQL 显式声明，名称与 Prisma 的 map 保持一致。
	//
	// 唯一索引必须建：它决定「邮箱重复注册」「token_hash 撞车」在开发环境的行为，
	// 与生产一致才能暴露真实竞态。
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_app_users_username ON app_users (username)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_app_users_email ON app_users (email)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_app_users_phone ON app_users (phone)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_app_sessions_token_hash ON app_sessions (token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_app_sessions_user_expires_at ON app_sessions (user_id, expires_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_auth_method_configs_method_type ON auth_method_configs (method_type)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_app_user_auth_identities_method_identifier ON app_user_auth_identities (method_type, identifier)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_app_user_auth_identities_method_provider_user_id ON app_user_auth_identities (method_type, provider_user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_app_user_auth_identities_user_method ON app_user_auth_identities (user_id, method_type)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_verification_codes_method_target_scene_created_at ON auth_verification_codes (method_type, target, scene, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_app_user_agreements_user_created_at ON app_user_agreements (user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_app_user_agreements_version_type ON app_user_agreements (version, agreement_type)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_agreement_versions_published_at ON auth_agreement_versions (published_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_auth_gateway_configs_channel ON auth_gateway_configs (channel)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_billing_plans_code ON billing_plans (code)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_billing_orders_order_no ON billing_orders (order_no)`,
		`CREATE INDEX IF NOT EXISTS idx_billing_orders_user_status_created_at ON billing_orders (user_id, status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_billing_orders_status_expires_at ON billing_orders (status, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_billing_subscriptions_user_status_expires_at ON billing_subscriptions (user_id, status, expires_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_billing_coupons_code ON billing_coupons (code)`,
		`CREATE INDEX IF NOT EXISTS idx_billing_coupon_redemptions_coupon_user ON billing_coupon_redemptions (coupon_id, user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_billing_coupon_redemptions_order ON billing_coupon_redemptions (order_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_billing_payment_configs_channel ON billing_payment_configs (channel)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("auth: 创建开发库索引失败（%s）: %w", statement, err)
		}
	}
	return seedDevMethodConfigs(db)
}

// seedDevMethodConfigs 在开发库里补齐登录方式配置。
//
// 生产由 CanvasMind 后台维护这张表；本地 SQLite 没有后台，缺行会让登录接口一律
// 返回「当前登录方式未启用」，因此只在这里补默认值。GitHub 是否启用取决于是否
// 提供了客户端凭据——没有凭据就置灰，避免点开之后才报未配置。
func seedDevMethodConfigs(db *gorm.DB) error {
	configs := []MethodConfig{
		{
			ID: "dev-config-email-code", MethodType: MethodEmailCode, Category: CategoryCode,
			DisplayName: "邮箱验证码", IsEnabled: true, IsVisible: true, SortOrder: 10,
			AllowAutoFill: true, AllowSignUp: true,
		},
		{
			ID: "dev-config-phone-code", MethodType: MethodPhoneCode, Category: CategoryCode,
			DisplayName: "手机号验证码", IsEnabled: true, IsVisible: true, SortOrder: 11,
			AllowAutoFill: true, AllowSignUp: true,
		},
		{
			// 密码通道默认开着：短信签名审批没过之前，这是唯一一条不依赖任何
			// 第三方投递能力的注册与登录路径。
			ID: "dev-config-password", MethodType: MethodPassword, Category: CategoryPassword,
			DisplayName: "密码登录", IsEnabled: true, IsVisible: true, SortOrder: 12,
			AllowAutoFill: false, AllowSignUp: true,
		},
	}
	clientID := strings.TrimSpace(os.Getenv("BEEFTV_GITHUB_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("BEEFTV_GITHUB_CLIENT_SECRET"))
	redirectURI := strings.TrimSpace(os.Getenv("BEEFTV_GITHUB_REDIRECT_URI"))
	oauthJSON, err := json.Marshal(OAuthConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
	})
	if err != nil {
		return err
	}
	configs = append(configs, MethodConfig{
		ID: "dev-config-github-oauth", MethodType: MethodGithubOAuth, Category: CategoryOAuth,
		DisplayName: "GitHub 登录", IsVisible: true, SortOrder: 20,
		AllowAutoFill: true, AllowSignUp: true,
		IsEnabled: clientID != "" && clientSecret != "", ConfigJSON: oauthJSON,
	})
	for _, config := range configs {
		var existing MethodConfig
		switch err := db.Where("method_type = ?", string(config.MethodType)).First(&existing).Error; {
		case errors.Is(err, gorm.ErrRecordNotFound):
			record := config
			if err := db.Create(&record).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		case config.MethodType == MethodGithubOAuth:
			// 上面只补缺行，而 GitHub 的启用状态由两个来源共同决定：环境变量里的凭据，
			// 以及管理后台的开关。这里按"凭据齐备 且 管理员没有关掉"重算，两边都不会
			// 被对方抹掉——只重写 is_enabled 会丢管理动作，只保留旧值则"补上凭据后不
			// 重启不生效"，都会让运营以为代码没写完。
			adminEnabled := true
			if previous, parseErr := ParseOAuthConfig(existing.ConfigJSON); parseErr == nil && previous.AdminEnabled != nil {
				adminEnabled = *previous.AdminEnabled
			}
			merged, mergeErr := mergeOAuthAdminFlag(config.ConfigJSON, adminEnabled)
			if mergeErr != nil {
				return mergeErr
			}
			if err := db.Model(&MethodConfig{}).
				Where("method_type = ?", string(config.MethodType)).
				Updates(map[string]any{
					"is_enabled":  config.IsEnabled && adminEnabled,
					"config_json": merged,
				}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
