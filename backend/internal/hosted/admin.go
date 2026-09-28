package hosted

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// 账号侧的管理端接口。
//
// 为什么不在 internal/handler 里注册：这一组接口要读写 CanvasMind 的账号库，而
// handler 属于桌面产物共享的包，不能依赖 internal/auth（见
// bootstrap/desktop_dependencies_test.go 的依赖图断言）。托管层本来就同时持有
// 账号服务与画布服务，把「账号 + 画布」的合并视图放在这里最自然。
//
// 路径仍然收敛在 /api/admin 前缀下：对前端来说只有一个管理后台，不应该知道
// 某个面板的数据来自哪套库。

const hostedAdminUserKey = "hosted.adminUser"

// adminOverviewResponse 把两套库的读数合成仪表盘的一份响应。
type adminOverviewResponse struct {
	*app.AdminCanvasOverview
	Users *auth.AdminUserCounts `json:"users"`
}

// adminUserRow 是用户列表的一行：账号字段 + 画布侧的作品数。
type adminUserRow struct {
	auth.AdminUserView
	Canvases int64 `json:"canvases"`
}

type adminUserPageResponse struct {
	Users    []adminUserRow `json:"users"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
}

// registerAdminRoutes 挂载账号侧的 /api/admin 子路由。
func (e *Extension) registerAdminRoutes(api *gin.RouterGroup) {
	group := api.Group("/admin")
	group.Use(e.requireAdmin())

	group.GET("/analytics/overview", e.handleAdminOverview)
	group.GET("/users", e.handleAdminUserList)
	group.GET("/users/:id", e.handleAdminUserDetail)
	group.PATCH("/users/:id/status", e.handleAdminUserStatus)
	group.PATCH("/users/:id/role", e.handleAdminUserRole)
	group.POST("/users/:id/password", e.handleAdminResetPassword)
	group.POST("/users/:id/logout", e.handleAdminForceLogout)
	group.GET("/login-methods", e.handleAdminLoginMethods)
	group.PATCH("/login-methods/:methodType", e.handleAdminUpdateLoginMethod)
	group.GET("/audit-events", e.handleAdminAuditEvents)
	// 协议管理：当前版本 / 发布新版（强制重签）/ 签署记录。
	e.registerAgreementRoutes(group)
	// 验证码投递网关：SMTP 与短信的配置、脱敏读取与测试发送。
	e.registerGatewayRoutes(group)
	// 画布内容管理：列表 / 只读详情 / 处置。
	e.registerCanvasModerationRoutes(group)
	// 计费管理：套餐、订单（补单/退款）、优惠券与支付渠道配置。
	e.registerAdminBillingRoutes(group)
	// 角色与权限：RBAC 角色定义、权限点与账号角色分配。
	e.registerAdminRbacRoutes(group)
	// 素材管理：素材列表、处置状态与占用读数。
	e.registerAdminAssetRoutes(group)
	// 模板管理：画布模板上下架、分类与推荐位。
	e.registerAdminTemplateRoutes(group)
	// 工单与反馈：用户工单列表、回复与状态流转。
	e.registerAdminTicketRoutes(group)
	// 模型定价：计费倍率规则与模型单价（单价可留空占位，售价由倍率算出）。
	e.registerAdminPricingRoutes(group)
	// 模型厂商：厂商、厂商下的凭据与模型目录（凭据落成 system channel，前台不持密钥）。
	e.registerAdminVendorRoutes(group)
}

// requireAdmin 解析会话并收敛管理员判定：未登录 401，非管理员 403。
func (e *Extension) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := e.service.SessionUser(c.Request.Context(), auth.ReadSessionToken(c.Request))
		if err != nil {
			respondFailure(c, http.StatusUnauthorized, "当前未登录或登录已失效")
			c.Abort()
			return
		}
		if user.Role != auth.RoleAdmin {
			respondFailure(c, http.StatusForbidden, "需要管理员权限")
			c.Abort()
			return
		}
		c.Set(hostedAdminUserKey, user)
		c.Next()
	}
}

func adminActor(c *gin.Context) *auth.AuthUser {
	value, exists := c.Get(hostedAdminUserKey)
	if !exists {
		return nil
	}
	user, _ := value.(*auth.AuthUser)
	return user
}

// canvasActor 把会话账号投影成画布侧的审计主体。
//
// 审计落在画布库，而账号 ID 是两边唯一的公共键；这里不复制邮箱等账号资料，
// 避免同一个人在两套库里有两份对不上的画像。
func canvasActor(user *auth.AuthUser) *model.User {
	if user == nil {
		return nil
	}
	return &model.User{ID: user.ID, DisplayName: user.Name, Role: model.UserRoleAdmin, Status: model.UserStatusActive}
}

func (e *Extension) handleAdminOverview(c *gin.Context) {
	actor := canvasActor(adminActor(c))
	if e.canvas == nil || actor == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	days := adminIntQuery(c, "days")
	canvasOverview, err := e.canvas.AdminCanvasOverview(actor, days)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	window := time.Now().AddDate(0, 0, -(canvasOverview.Days - 1))
	users, err := e.service.AdminUserCounts(window)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, adminOverviewResponse{AdminCanvasOverview: canvasOverview, Users: users})
}

func (e *Extension) handleAdminUserList(c *gin.Context) {
	page, err := e.service.AdminListUsers(auth.AdminUserFilter{
		Keyword: c.Query("keyword"),
		Status:  c.Query("status"),
		Role:    c.Query("role"),
		Page:    adminIntQuery(c, "page"),
		Limit:   adminIntQuery(c, "pageSize"),
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	rows := make([]adminUserRow, 0, len(page.Users))
	for _, user := range page.Users {
		rows = append(rows, adminUserRow{AdminUserView: user})
	}
	// 作品数来自画布库：取不到就退化成 0，不让仪表盘之外的一次统计失败把
	// 用户管理整页打挂。
	if e.canvas != nil && len(rows) > 0 {
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if counts, countErr := e.canvas.AdminCanvasCountsByOwner(canvasActor(adminActor(c)), ids); countErr == nil {
			for index := range rows {
				rows[index].Canvases = counts[rows[index].ID]
			}
		}
	}
	respondOK(c, adminUserPageResponse{Users: rows, Total: page.Total, Page: page.Page, PageSize: page.Limit})
}

func (e *Extension) handleAdminUserDetail(c *gin.Context) {
	user, err := e.service.AdminUserByID(c.Param("id"))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, user)
}

func (e *Extension) handleAdminUserStatus(c *gin.Context) {
	var input struct {
		Status auth.UserStatus `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "账号状态参数格式错误")
		return
	}
	actor := adminActor(c)
	target := c.Param("id")
	if err := e.service.AdminSetUserStatus(actor.ID, target, input.Status); err != nil {
		respondServiceError(c, err)
		return
	}
	summary := "解封账号"
	if input.Status == auth.StatusDisabled {
		summary = "封禁账号并踢下线"
	}
	e.recordAudit(c, "user.status", "user", target, summary, gin.H{"status": input.Status})
	respondOK(c, gin.H{"updated": true, "status": input.Status})
}

func (e *Extension) handleAdminUserRole(c *gin.Context) {
	var input struct {
		Role auth.UserRole `json:"role"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "角色参数格式错误")
		return
	}
	actor := adminActor(c)
	target := c.Param("id")
	if err := e.service.AdminSetUserRole(actor.ID, target, input.Role); err != nil {
		respondServiceError(c, err)
		return
	}
	summary := "取消管理员"
	if input.Role == auth.RoleAdmin {
		summary = "设为管理员"
	}
	e.recordAudit(c, "user.role", "user", target, summary, gin.H{"role": input.Role})
	respondOK(c, gin.H{"updated": true, "role": input.Role})
}

func (e *Extension) handleAdminResetPassword(c *gin.Context) {
	var input struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "密码参数格式错误")
		return
	}
	target := c.Param("id")
	if err := e.service.AdminResetPassword(target, input.Password); err != nil {
		respondServiceError(c, err)
		return
	}
	// 审计只记录"发生了一次重置"，绝不落明文或哈希：审计日志的可读范围比密钥窄不了多少。
	e.recordAudit(c, "user.password", "user", target, "重置账号密码并强制下线", nil)
	respondOK(c, gin.H{"updated": true})
}

func (e *Extension) handleAdminForceLogout(c *gin.Context) {
	target := c.Param("id")
	revoked, err := e.service.AdminForceLogout(target)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "user.logout", "user", target, "强制账号下线", gin.H{"revokedSessions": revoked})
	respondOK(c, gin.H{"revokedSessions": revoked})
}

func (e *Extension) handleAdminLoginMethods(c *gin.Context) {
	methods, err := e.service.AdminMethodConfigs()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"methods": methods})
}

func (e *Extension) handleAdminUpdateLoginMethod(c *gin.Context) {
	var input auth.AdminMethodConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "登录方式参数格式错误")
		return
	}
	methodType := auth.MethodType(strings.ToUpper(strings.TrimSpace(c.Param("methodType"))))
	view, err := e.service.AdminUpdateMethodConfig(methodType, input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "login-method.update", "auth-method", string(view.MethodType), "更新登录方式开关", gin.H{
		"isEnabled": view.IsEnabled, "isVisible": view.IsVisible, "allowSignUp": view.AllowSignUp,
	})
	respondOK(c, view)
}

func (e *Extension) handleAdminAuditEvents(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	page, err := e.canvas.AdminAuditEvents(canvasActor(adminActor(c)), adminIntQuery(c, "page"), adminIntQuery(c, "pageSize"))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, page)
}

// recordAudit 是"写操作必须留痕"的单一出口。
//
// 审计写失败不回滚业务：用户已经被封禁/改角色了，这时候返回 500 只会让管理员
// 以为操作没生效而重复提交。但要把失败写进日志，便于事后发现审计缺口。
func (e *Extension) recordAudit(c *gin.Context, action string, targetType string, targetID string, summary string, metadata any) {
	if e.canvas == nil {
		return
	}
	actor := canvasActor(adminActor(c))
	if actor == nil {
		return
	}
	if err := e.canvas.RecordAdminAudit(actor, action, targetType, targetID, summary, metadata); err != nil {
		log.Printf("hosted: 审计写入失败 action=%s target=%s: %v", action, targetID, err)
	}
}

func respondOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": kernel.CodeOK, "data": data, "msg": "ok"})
}

func respondFailure(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"code": status, "data": nil, "msg": message})
}

// respondServiceError 把 auth/app 两个模块的结构化错误投影成 HTTP。
//
// 未分类错误一律 500 且不回显原文：上游响应体与凭据不允许进入响应。
func respondServiceError(c *gin.Context, err error) {
	var authErr *auth.Error
	if errors.As(err, &authErr) && authErr.Status >= 400 {
		respondFailure(c, authErr.Status, authErr.Message)
		return
	}
	var appErr *app.AppError
	if errors.As(err, &appErr) && appErr.Status >= 400 {
		respondFailure(c, appErr.Status, appErr.Message)
		return
	}
	log.Printf("hosted admin request failed: error_type=%T", err)
	respondFailure(c, http.StatusInternalServerError, "系统处理失败，请稍后重试")
}

func adminIntQuery(c *gin.Context, name string) int {
	value, err := strconv.Atoi(strings.TrimSpace(c.Query(name)))
	if err != nil {
		return 0
	}
	return value
}

// ---------- 账户与用量 ----------

// accountOverviewResponse 是账户页的一次性读数。
//
// 三段刻意分开：身份来自账号库，用量来自画布库，协议留痕只读。账户页不暴露任何
// 可写字段——改密码、换绑手机号属于认证模块的能力，不该在这个聚合接口里重开一份。
type accountOverviewResponse struct {
	Account struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Email       string    `json:"email"`
		Phone       string    `json:"phone"`
		Role        string    `json:"role"`
		LoginMethod string    `json:"loginMethod"`
		CreatedAt   time.Time `json:"createdAt"`
	} `json:"account"`
	Usage      *app.AccountUsageSummary    `json:"usage"`
	Agreements []auth.AccountAgreementView `json:"agreements"`
	Billing    struct {
		Mode    string `json:"mode"`
		Balance *int64 `json:"balance"`
		Note    string `json:"note"`
	} `json:"billing"`
}

// registerAccountRoutes 挂载用户自己的账户读数。
//
// 路径沿用契约里预留的 /api/finance/account：这是唯一一处既不是 admin 也不是
// 认证模块自身、却要读写账号信息的接口。
func (e *Extension) registerAccountRoutes(api *gin.RouterGroup) {
	api.GET("/finance/account", e.handleAccountOverview)
}

func (e *Extension) handleAccountOverview(c *gin.Context) {
	user, err := e.service.SessionUser(c.Request.Context(), auth.ReadSessionToken(c.Request))
	if err != nil {
		respondFailure(c, http.StatusUnauthorized, "当前未登录或登录已失效")
		return
	}
	// 账号字段取自账号库的读模型（AdminUserByID 不做角色判定，这里查的是自己），
	// 会话视图里没有注册时间与手机号。
	profile, err := e.service.AdminUserByID(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	response := accountOverviewResponse{}
	response.Account.ID = profile.ID
	response.Account.Name = profile.Name
	response.Account.Email = profile.Email
	response.Account.Phone = profile.Phone
	response.Account.Role = string(profile.Role)
	response.Account.LoginMethod = string(user.Method)
	response.Account.CreatedAt = profile.CreatedAt

	agreements, err := e.service.AccountAgreements(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	response.Agreements = agreements

	if e.canvas != nil {
		usage, usageErr := e.canvas.AccountUsage(&model.User{ID: user.ID, DisplayName: user.Name, Role: model.UserRoleUser}, 30)
		if usageErr != nil {
			respondServiceError(c, usageErr)
			return
		}
		response.Usage = usage
	}

	// 计费模式如实回报：模型与上游成本仍由平台统一承担（用户不持密钥），余额因此是
	// null 而不是 0——0 会被读成"用户真的一分钱没有"。套餐与充值已经接入，文案要把
	// 用户指向计费页，不能停在"尚未开放"。
	response.Billing.Mode = "platform"
	response.Billing.Note = "模型与上游成本由平台统一承担，无需自备密钥；套餐与额度可在计费页查看与购买。"
	respondOK(c, response)
}
