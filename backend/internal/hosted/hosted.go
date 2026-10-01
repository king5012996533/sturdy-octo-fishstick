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
	"time"

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
	// janitor 是计费侧的清理协程开关。只允许启动一次，Close 时必须先停它再断开
	// 数据库连接，否则最后一轮清理会打到已关闭的连接上。
	janitorOnce sync.Once
	janitorStop chan struct{}
	// accountJanitor 是到期注销的执行协程开关。它与计费清理分开计数：两者的
	// 周期与失败语义都不同，共用一个 Once 会让其中一个再也起不来。
	accountJanitorOnce sync.Once
	accountJanitorStop chan struct{}
}

// billingJanitorInterval 是超时订单清理周期。
//
// 5 分钟与订单 30 分钟的支付时限同量级：再密也只是空转，再疏则用户刚放弃支付，
// 后台的"待支付"读数还会挂很久。
const billingJanitorInterval = 5 * time.Minute

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
		// 任务计费同样只在托管实例注入：本地/桌面没有账号库，也就没有积分账户，
		// 注入一个空的端口只会让每次生成都去撞一张不存在的表。
		deps.Service.UseTaskCreditLedger(creditLedgerAdapter{service: service})
		if deps.Service.TaskBillingEnabled() {
			// 每次启动都说一遍：一个没接上计费的托管实例不会报错，只会静默白送算力。
			log.Printf("hosted: 任务计费已启用，生成按后台配置的模型单价扣积分")
		}
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
// anonymousPathPrefixes 是会话建立之前就必须可达的路径。
//
// /api/auth/* 是登录流程本身；公开外观是登录页的品牌、文案与 Logo——访客在拿到会话
// 之前就要渲染这一屏，否则后台改完品牌、访客看到的仍是内置默认值（而默认值恰好与
// 线上一致时，故障会一直被掩盖）。这些响应里只有品牌与备案信息，没有任何密钥。
//
// 支付回调同样在这里显式登记，而不是让它自己去 init 里往这个切片追加：放行名单是
// 一张安全边界清单，必须一眼看全，不能散落在各文件里。放行也不等于放权——回调的
// 准入是渠道验签与金额核对（HandleBillingCallback），中间件只负责别提前判 401。
var anonymousPathPrefixes = []string{auth.BasePath, "/api/public/appearance", "/api/public/resources",
	billingCallbackPathPrefix}

func isAnonymousPath(path string) bool {
	for _, prefix := range anonymousPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (e *Extension) WorkspaceMiddleware() gin.HandlerFunc {
	// prepared 缓存已建过工作区的账号，避免把「确保存在」做成每请求一次写库。
	var prepared sync.Map
	return func(c *gin.Context) {
		if isAnonymousPath(c.Request.URL.Path) {
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
	// 资源下载：供模型上游拉取参考素材，准入靠签名而非会话。
	e.registerPublicResourceRoutes(api)
	e.registerOwnCanvasModerationRoutes(api)
	// 计费：套餐货架、结算试算、下单与支付（用户端，主体恒为会话账号）。
	e.registerBillingRoutes(api)
	// 积分：余额与流水（用户端，同样以会话账号为主体）。
	e.registerCreditRoutes(api)
	// 工单与反馈：用户提交工单、查看自己的工单与回复。
	e.registerSupportRoutes(api)
	// 模板目录：前台可套用的画布模板（只读，仅返回已上架）。
	e.registerTemplateCatalogRoutes(api)
	// 灵感广场：前台精选灵感（只读，仅返回已上架）。
	e.registerInspirationCatalogRoutes(api)
	// 投稿：用户把自己的生成产物发布到广场，等待人工审核。
	e.registerCreationPostRoutes(api)
	// 计费清理协程：超时未支付的订单必须由平台自己关闭（用户放弃支付后没人会手动取消），
	// 否则待支付读数失真，且订单占用的优惠券永远不会归还。
	e.startBillingJanitor()
	// 注销执行协程：冷静期到期后必须真的把账号匿名化，"申请了但没人执行"等于
	// 对用户承诺的删除没有兑现。
	e.startAccountDeletionJanitor()
}

// startBillingJanitor 启动超时订单清理：先立刻扫一遍（补上停机期间积压的超时订单），
// 之后按 billingJanitorInterval 周期执行。
func (e *Extension) startBillingJanitor() {
	if e == nil || e.service == nil {
		return
	}
	e.janitorOnce.Do(func() {
		stop := make(chan struct{})
		e.janitorStop = stop
		go func() {
			sweep := func() {
				count, err := e.service.ExpireStaleBillingOrders()
				if err != nil {
					log.Printf("hosted: 关闭超时订单失败: %v", err)
					return
				}
				if count > 0 {
					log.Printf("hosted: 已关闭 %d 笔超时未支付订单", count)
				}
			}
			sweep()
			ticker := time.NewTicker(billingJanitorInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					sweep()
				}
			}
		}()
	})
}

// Close 停止后台清理协程并释放账号库连接。
func (e *Extension) Close() error {
	if e == nil {
		return nil
	}
	if e.janitorStop != nil {
		close(e.janitorStop)
		e.janitorStop = nil
	}
	if e.accountJanitorStop != nil {
		close(e.accountJanitorStop)
		e.accountJanitorStop = nil
	}
	if e.db == nil {
		return nil
	}
	sqlDB, err := e.db.DB()
	if err != nil {
		return nil
	}
	e.db = nil
	return sqlDB.Close()
}
