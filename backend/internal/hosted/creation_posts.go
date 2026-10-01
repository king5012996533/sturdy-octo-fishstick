package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/app"

	"github.com/gin-gonic/gin"
)

// 用户投稿与投稿审核的路由。
//
// 两组路由刻意分开挂：用户端只需要"提交 / 看自己的 / 撤回"，审核端是管理员才有的
// 待人队列与裁决。把审核动作混进用户路由会让"能不能审"退化成 handler 里的一个 if。

// registerCreationPostRoutes 挂载用户端投稿路由（需要登录）。
func (e *Extension) registerCreationPostRoutes(api *gin.RouterGroup) {
	api.POST("/posts", e.handleCreationPostSubmit)
	api.GET("/me/posts", e.handleMyCreationPosts)
	api.DELETE("/me/posts/:id", e.handleCreationPostWithdraw)
}

// registerAdminCreationPostRoutes 挂载投稿审核路由（group 已带管理员守卫）。
func (e *Extension) registerAdminCreationPostRoutes(group *gin.RouterGroup) {
	group.GET("/posts", e.handleAdminCreationPostQueue)
	group.POST("/posts/:id/approve", e.handleAdminCreationPostApprove)
	group.POST("/posts/:id/reject", e.handleAdminCreationPostReject)
}

func (e *Extension) handleCreationPostSubmit(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "灵感广场尚未就绪")
		return
	}
	var input app.CreationPostInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "投稿参数格式错误")
		return
	}
	post, err := e.canvas.SubmitCreationPost(user.ID, user.Name, input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"post": billingView(post)})
}

func (e *Extension) handleMyCreationPosts(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "灵感广场尚未就绪")
		return
	}
	posts, err := e.canvas.MyCreationPosts(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"posts": billingList(posts)})
}

func (e *Extension) handleCreationPostWithdraw(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "灵感广场尚未就绪")
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少投稿标识")
		return
	}
	if err := e.canvas.WithdrawCreationPost(user.ID, id); err != nil {
		respondServiceError(c, err)
		return
	}
	posts, err := e.canvas.MyCreationPosts(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 撤回后直接回全量列表：与灵感删除一致，省掉一次 GET，也避免前端停在旧数据上。
	respondOK(c, gin.H{"posts": billingList(posts)})
}

func (e *Extension) handleAdminCreationPostQueue(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "灵感广场尚未就绪")
		return
	}
	posts, err := e.canvas.AdminCreationPostQueue()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"posts": billingList(posts)})
}

func (e *Extension) handleAdminCreationPostApprove(c *gin.Context) {
	e.handleAdminCreationPostReview(c, true, "post.review-approve", "通过投稿审核")
}

func (e *Extension) handleAdminCreationPostReject(c *gin.Context) {
	e.handleAdminCreationPostReview(c, false, "post.review-reject", "驳回投稿审核")
}

// handleAdminCreationPostReview 是审核动作的单一实现：通过与驳回只差一个布尔值，
// 分成两份会把"通过时上架、驳回时下架"这条规则抄两遍，早晚只改一处。
func (e *Extension) handleAdminCreationPostReview(c *gin.Context, approve bool, action string, summary string) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "灵感广场尚未就绪")
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少投稿标识")
		return
	}
	// 请求体可以缺省：通过不需要理由，驳回不写理由时由服务层补默认文案。
	var input struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&input)

	post, err := e.canvas.ReviewCreationPost(id, approve, input.Note)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, action, "post", id, summary, gin.H{
		"title":  post.Title,
		"mode":   post.Mode,
		"note":   post.ReviewNote,
		"status": post.Status,
	})
	respondOK(c, gin.H{"post": billingView(post)})
}
