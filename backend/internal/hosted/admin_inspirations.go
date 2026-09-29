package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/app"

	"github.com/gin-gonic/gin"
)

// 精选灵感广场管理（平台内容，存画布库）。
//
// 广场此前把素材硬编码在前端仓库里，每次换一批都要发版；这里把它变成后台数据：
// 后台负责增删改、上下架、排序与推荐位，前台只读目录。视图结构体复用
// app.CreationInspirationView，字段名不再各解释一次。
//
// 每个写操作都必须落审计：下架会立刻让用户端少一条灵感，事后复盘"谁在什么时候撤掉了
// 哪一条"只能靠留痕。

// registerAdminInspirationRoutes 挂载 /api/admin 下的灵感管理路由（group 已带管理员守卫）。
func (e *Extension) registerAdminInspirationRoutes(group *gin.RouterGroup) {
	group.GET("/inspirations", e.handleAdminInspirationList)
	group.POST("/inspirations", e.handleAdminInspirationCreate)
	group.PUT("/inspirations/:id", e.handleAdminInspirationUpdate)
	group.DELETE("/inspirations/:id", e.handleAdminInspirationDelete)
}

// registerInspirationCatalogRoutes 挂载用户端只读灵感目录（需要登录）。
func (e *Extension) registerInspirationCatalogRoutes(api *gin.RouterGroup) {
	api.GET("/inspirations", e.handleInspirationCatalog)
}

func (e *Extension) handleAdminInspirationList(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	inspirations, err := e.canvas.AdminCreationInspirations()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"inspirations": billingList(inspirations)})
}

func (e *Extension) handleAdminInspirationCreate(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	var input app.CreationInspirationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "灵感参数格式错误")
		return
	}
	// 新建丢弃请求体里的 id：主键由服务层生成，客户端传进来的 id 会让一次"新建"
	// 静默覆盖已有灵感。
	input.ID = ""
	inspiration, err := e.canvas.SaveCreationInspiration(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "inspiration.create", "inspiration", billingView(inspiration).ID, "新建精选灵感", gin.H{
		"title":     input.Title,
		"mode":      input.Mode,
		"status":    input.Status,
		"featured":  input.Featured,
		"sortOrder": input.SortOrder,
	})
	respondOK(c, gin.H{"inspiration": billingView(inspiration)})
}

func (e *Extension) handleAdminInspirationUpdate(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	var input app.CreationInspirationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "灵感参数格式错误")
		return
	}
	// 主键以路径为准：body 与路径不一致时信 body 会把一次误点改到另一条灵感上。
	input.ID = strings.TrimSpace(c.Param("id"))
	if input.ID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少灵感标识")
		return
	}
	inspiration, err := e.canvas.SaveCreationInspiration(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "inspiration.update", "inspiration", input.ID, "更新精选灵感", gin.H{
		"title":     input.Title,
		"mode":      input.Mode,
		"status":    input.Status,
		"featured":  input.Featured,
		"sortOrder": input.SortOrder,
	})
	respondOK(c, gin.H{"inspiration": billingView(inspiration)})
}

func (e *Extension) handleAdminInspirationDelete(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少灵感标识")
		return
	}
	if err := e.canvas.DeleteCreationInspiration(id); err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "inspiration.delete", "inspiration", id, "删除精选灵感", gin.H{"id": id})
	// 删除后直接回全量列表：省掉前端再补一次 GET，也避免"已经删掉了、列表还停在
	// 旧数据"的半拍状态（与模板删除一致）。
	inspirations, listErr := e.canvas.AdminCreationInspirations()
	if listErr != nil {
		respondServiceError(c, listErr)
		return
	}
	respondOK(c, gin.H{"inspirations": billingList(inspirations)})
}

// handleInspirationCatalog 返回前台可见的灵感目录。
//
// 会话校验不能省：这一组路由可能被挂到没有中间件的分组上（只注册业务路由的测试
// 入口），缺了会话必须显式 401，而不是拿空身份去查出一份目录。
func (e *Extension) handleInspirationCatalog(c *gin.Context) {
	if _, ok := e.billingSessionUser(c); !ok {
		return
	}
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "灵感目录尚未就绪")
		return
	}
	inspirations, err := e.canvas.CreationInspirationCatalog()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"inspirations": billingList(inspirations)})
}
