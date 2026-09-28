// Package hosted 装配只存在于服务端的托管能力。
//
// 它刻意与 internal/bootstrap 分开：本地桌面二进制的依赖图里不允许出现认证、
// 计费一类的基础设施（见 bootstrap/desktop_dependencies_test.go），所以托管能力
// 必须由服务端入口显式注入，而不是写进共享的 bootstrap 包。
package hosted

import (
	"errors"
	"log"
	"strings"
	"sync"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/workspace"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Options 描述托管侧的外部依赖。
type Options struct {
	// DatabaseDriver/DatabaseURL 指向 CanvasMind 的账号库；生产为 MySQL。
	DatabaseDriver string
	DatabaseURL    string
	// StateSecret 用于给 OAuth state 签名。
	StateSecret string
	// CookieSecure 在 HTTPS 部署下必须为 true。
	CookieSecure bool
	// DevEchoCode 在本地投递通道下把验证码回显给前端，见 auth.Service.devEcho。
	DevEchoCode bool
}

// Extension 是登录模块在宿主里的生命周期载体。
type Extension struct {
	service *auth.Service
	// canvas 是画布侧服务：账号管理要合并"作品数/调用量"，审计也必须落在画布库。
	canvas  *app.Service
	db      *gorm.DB
	dataDir string
	ensure  func(userID string, displayName string) error
	cookie  auth.CookieOptions
}

// New 装配登录模块。
//
// 账号库与画布库刻意分开：画布数据是租户数据，账号表归 CanvasMind 的 Prisma
// 迁移管理，两者的备份与发布节奏不同。
func New(deps bootstrap.HostedDeps, options Options) (bootstrap.HostedExtension, error) {
	if strings.TrimSpace(options.DatabaseURL) == "" {
		return nil, errors.New("hosted: 账号库 DSN 不能为空")
	}
	db, err := database.Open(database.Config{
		Driver:  options.DatabaseDriver,
		DSN:     options.DatabaseURL,
		DataDir: deps.DataDir,
	})
	if err != nil {
		return nil, err
	}
	closeDB := func() {
		if sqlDB, closeErr := db.DB(); closeErr == nil {
			_ = sqlDB.Close()
		}
	}
	if err := database.ConfigurePool(db); err != nil {
		closeDB()
		return nil, err
	}
	// 本地开发允许用 SQLite 跑通登录链路；生产库的表结构只能由 Prisma 迁移负责，
	// 因此自动建表被严格限制在 sqlite 驱动上。
	if db.Dialector.Name() == "sqlite" {
		if err := auth.EnsureDevSchema(db); err != nil {
			closeDB()
			return nil, err
		}
	} else if err := auth.VerifySchema(db); err != nil {
		closeDB()
		return nil, err
	}
	if options.DevEchoCode {
		log.Printf("auth: 开发回显已开启，验证码会随下发响应一起返回；仅限本地联调")
	}
	service, err := auth.NewService(auth.Options{
		Store:       auth.NewStore(db),
		EmailSender: emailSender(),
		SMSSender:   smsSender(),
		StateSecret: []byte(strings.TrimSpace(options.StateSecret)),
		DevEchoCode: options.DevEchoCode,
	})
	if err != nil {
		closeDB()
		return nil, err
	}
	extension := &Extension{
		service: service,
		canvas:  deps.Service,
		db:      db,
		dataDir: deps.DataDir,
		cookie:  auth.CookieOptions{Secure: options.CookieSecure},
	}
	if deps.Service != nil {
		extension.ensure = deps.Service.EnsureWorkspace
		// 内容审核是托管专属能力：本地/桌面形态的库结构里没有 canvas_moderation，
		// 开着它只会让每次读写画布都撞上一张不存在的表。
		deps.Service.EnableCanvasModeration(true)
	}
	return extension, nil
}

// emailSender 选择验证码投递通道。
//
// 未配置 SMTP 时回落到控制台：本机联调必须能拿到验证码。日志投递等于把登录
// 能力交给任何能看到日志的人，因此每次启动都把这件事显式说出来。
func emailSender() auth.EmailSender {
	sender, err := auth.SMTPSenderFromEnv()
	if err == nil {
		return sender
	}
	log.Printf("auth: 未配置 SMTP（%v），邮箱验证码将只写入服务端日志，禁止用于生产", err)
	return auth.ConsoleSender{}
}

// smsSender 选择短信投递通道，未配置时回落到控制台。
//
// 与 emailSender 同构，但有一点不同：SMSSenderFromEnv 在「配了 AccessKey 却缺
// 签名或模板」时会报错，这里同样回落到日志投递。区别是那属于半配置状态，因此
// 日志把原因原样带出来，便于上线前发现。
func smsSender() auth.SMSSender {
	sender, err := auth.SMSSenderFromEnv()
	if err == nil {
		return sender
	}
	log.Printf("auth: 未配置短信通道（%v），手机号验证码将只写入服务端日志，禁止用于生产", err)
	return auth.ConsoleSMSSender{}
}

// WorkspaceMiddleware 把会话映射成工作区作用域。
//
// 这是多租户的唯一入口：工作区 ID 只来自会话里的用户 ID，请求参数无法覆盖它。
// 认证自身的路由不参与校验，否则用户连登录页都打不开。
func (e *Extension) WorkspaceMiddleware() gin.HandlerFunc {
	// prepared 缓存已建过工作区的账号，避免把「确保存在」做成每请求一次写库。
	var prepared sync.Map
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, auth.BasePath) {
			c.Next()
			return
		}
		user, err := e.service.SessionUser(c.Request.Context(), auth.ReadSessionToken(c.Request))
		if err != nil {
			handler.AbortUnauthorized(c, "当前未登录或登录已失效")
			return
		}
		if e.ensure != nil {
			if _, done := prepared.Load(user.ID); !done {
				if ensureErr := e.ensure(user.ID, user.Name); ensureErr != nil {
					handler.AbortInternal(c, ensureErr)
					return
				}
				prepared.Store(user.ID, struct{}{})
			}
		}
		handler.SetWorkspaceScope(c, workspace.Context{ID: user.ID, DataDir: e.dataDir})
		handler.SetSessionUser(c, sessionPrincipal(user))
		c.Next()
	}
}

// sessionPrincipal 把会话账号映射成本地身份模型。
//
// 本地模型里没有邮箱与第三方信息，只保留角色与状态：它们决定前端如何展示，
// 真正的数据可见范围由工作区作用域决定，不依赖这里的字段。
func sessionPrincipal(user *auth.AuthUser) model.User {
	role := model.UserRoleUser
	if user.Role == auth.RoleAdmin {
		role = model.UserRoleAdmin
	}
	status := model.UserStatusActive
	if user.Status == auth.StatusDisabled {
		status = model.UserStatusDisabled
	}
	return model.User{ID: user.ID, DisplayName: user.Name, Role: role, Status: status}
}

// RegisterRoutes 挂载登录路由与账号侧的管理端路由。
func (e *Extension) RegisterRoutes(api *gin.RouterGroup) {
	auth.RegisterRoutes(api, e.service, e.cookie)
	e.registerAdminRoutes(api)
	e.registerAccountRoutes(api)
	e.registerOwnCanvasModerationRoutes(api)
}

// Close 释放账号库连接。
func (e *Extension) Close() error {
	if e == nil || e.db == nil {
		return nil
	}
	sqlDB, err := e.db.DB()
	if err != nil {
		return nil
	}
	e.db = nil
	return sqlDB.Close()
}
