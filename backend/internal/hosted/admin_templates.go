package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/app"

	"github.com/gin-gonic/gin"
)

// 画布模板管理（平台内容，存画布库）。
//
// 模板是运营维护的平台素材：后台负责增删改与上下架，用户端只读目录。两条路由分别
// 挂在管理员守卫与登录守卫下，视图结构体共用 app.CanvasTemplateView，字段名不再
// 各解释一次。
//
// 每个写操作都必须落审计：上架/下架会直接改变用户端可见的模板集合，事后复盘
// "谁在什么时候撤掉了哪个模板"只能靠留痕。

// registerAdminTemplateRoutes 挂载 /api/admin 下的模板管理路由（group 已带管理员守卫）。
func (e *Extension) registerAdminTemplateRoutes(group *gin.RouterGroup) {
	group.GET("/templates", e.handleAdminTemplateList)
	group.POST("/templates", e.handleAdminTemplateCreate)
	group.PUT("/templates/:id", e.handleAdminTemplateUpdate)
	group.DELETE("/templates/:id", e.handleAdminTemplateDelete)
}

// registerTemplateCatalogRoutes 挂载用户端只读模板目录（需要登录）。
func (e *Extension) registerTemplateCatalogRoutes(api *gin.RouterGroup) {
	api.GET("/templates", e.handleTemplateCatalog)
}

func (e *Extension) handleAdminTemplateList(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	templates, err := e.canvas.AdminCanvasTemplates()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"templates": billingList(templates)})
}

func (e *Extension) handleAdminTemplateCreate(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	var input app.CanvasTemplateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "模板参数格式错误")
		return
	}
	// 新建丢弃请求体里的 id：主键由服务层生成，客户端传进来的 id 会让一次"新建"
	// 静默覆盖已有模板。
	input.ID = ""
	template, err := e.canvas.SaveCanvasTemplate(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "template.create", "template", billingView(template).ID, "新建画布模板", gin.H{
		"code":      input.Code,
		"name":      input.Name,
		"status":    input.Status,
		"featured":  input.Featured,
		"sortOrder": input.SortOrder,
	})
	respondOK(c, gin.H{"template": billingView(template)})
}

func (e *Extension) handleAdminTemplateUpdate(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	var input app.CanvasTemplateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "模板参数格式错误")
		return
	}
	// 主键以路径为准：body 与路径不一致时信 body 会把一次误点改到另一条模板上。
	input.ID = strings.TrimSpace(c.Param("id"))
	if input.ID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少模板标识")
		return
	}
	template, err := e.canvas.SaveCanvasTemplate(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "template.update", "template", input.ID, "更新画布模板", gin.H{
		"code":      input.Code,
		"name":      input.Name,
		"status":    input.Status,
		"featured":  input.Featured,
		"sortOrder": input.SortOrder,
	})
	respondOK(c, gin.H{"template": billingView(template)})
}

func (e *Extension) handleAdminTemplateDelete(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少模板标识")
		return
	}
	if err := e.canvas.DeleteCanvasTemplate(id); err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "template.delete", "template", id, "删除画布模板", gin.H{"id": id})
	// 删除后直接回全量列表：省掉前端再补一次 GET，也避免"已经删掉了、列表还停在
	// 旧数据"的半拍状态（与套餐删除一致）。
	templates, listErr := e.canvas.AdminCanvasTemplates()
	if listErr != nil {
		respondServiceError(c, listErr)
		return
	}
	respondOK(c, gin.H{"templates": billingList(templates)})
}

// handleTemplateCatalog 返回用户端可见的模板目录。
//
// 会话校验不能省：这一组路由可能被挂到没有中间件的分组上（只注册业务路由的测试
// 入口），缺了会话必须显式 401，而不是拿空身份去查出一份目录。
func (e *Extension) handleTemplateCatalog(c *gin.Context) {
	if _, ok := e.billingSessionUser(c); !ok {
		return
	}
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "模板目录尚未就绪")
		return
	}
	templates, err := e.canvas.CanvasTemplateCatalog()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"templates": billingList(templates)})
}
