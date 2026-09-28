package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 工单与反馈。
//
// 两组路由共用同一套服务层视图：用户端只能看到自己的工单（主体恒取自会话），
// 后台则挂了 requireAdmin 守卫、能看到全部工单并回复/流转状态。工单数据在账号库，
// 因此这一层与计费一样挂在托管层——只有这里同时持有账号服务与审计出口。

// registerSupportRoutes 挂载需要登录的用户端工单路由。
//
// 路径与 web/src/services/api/support.ts 一一对应；后台的读写走
// registerAdminTicketRoutes，两边的视图结构体是同一个，字段名不再各解释一次。
func (e *Extension) registerSupportRoutes(api *gin.RouterGroup) {
	api.POST("/support/tickets", e.handleSupportTicketCreate)
	api.GET("/support/tickets", e.handleSupportTicketList)
	api.GET("/support/tickets/:id", e.handleSupportTicketDetail)
	api.POST("/support/tickets/:id/replies", e.handleSupportTicketReply)
}

// registerAdminTicketRoutes 挂载后台工单路由（group 已带管理员守卫）。
func (e *Extension) registerAdminTicketRoutes(group *gin.RouterGroup) {
	group.GET("/tickets", e.handleAdminTicketList)
	group.GET("/tickets/:id", e.handleAdminTicketDetail)
	group.POST("/tickets/:id/replies", e.handleAdminTicketReply)
	group.PATCH("/tickets/:id/status", e.handleAdminTicketStatus)
}

// userSupportTicketPagePayload 组装用户端工单分页响应。
//
// 列表恒为数组、分页回显服务层实际生效的值：前端直接对 tickets 做 map，null 会在
// 渲染时抛错。
func userSupportTicketPagePayload(view *auth.SupportTicketPageView, page, pageSize int) gin.H {
	tickets := []auth.SupportTicketView{}
	total := int64(0)
	if view != nil {
		tickets = billingList(view.Tickets)
		total = view.Total
		if view.Page > 0 {
			page = view.Page
		}
		if view.PageSize > 0 {
			pageSize = view.PageSize
		}
	}
	return gin.H{"tickets": tickets, "total": total, "page": page, "pageSize": pageSize}
}

// adminSupportTicketPagePayload 在用户端形状之上补一份全量计数。
//
// 指标卡要的是"不受当前筛选影响"的读数：让前端拿筛选结果自己加总，会把"只看了待处理"
// 当成站内只有几张工单。counts 恒为对象，缺数据时退化成零值而不是 null。
func adminSupportTicketPagePayload(view *auth.SupportTicketPageView, page, pageSize int) gin.H {
	payload := userSupportTicketPagePayload(view, page, pageSize)
	counts := auth.SupportTicketCounts{}
	if view != nil && view.Counts != nil {
		counts = *view.Counts
	}
	payload["counts"] = counts
	return payload
}

func (e *Extension) handleSupportTicketCreate(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	var input auth.SupportTicketInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "请求参数格式错误")
		return
	}
	ticket, err := e.service.CreateSupportTicket(user.ID, input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"ticket": billingView(ticket)})
}

func (e *Extension) handleSupportTicketList(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	page, pageSize := billingPagination(c)
	view, err := e.service.UserSupportTickets(user.ID, page, pageSize)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, userSupportTicketPagePayload(view, page, pageSize))
}

func (e *Extension) handleSupportTicketDetail(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	ticket, err := e.service.SupportTicketForUser(user.ID, strings.TrimSpace(c.Param("id")))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"ticket": billingView(ticket)})
}

func (e *Extension) handleSupportTicketReply(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	var input struct {
		Body string `json:"body"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "回复内容格式错误")
		return
	}
	ticket, err := e.service.ReplySupportTicket(user.ID, strings.TrimSpace(c.Param("id")), input.Body)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"ticket": billingView(ticket)})
}

func (e *Extension) handleAdminTicketList(c *gin.Context) {
	page, pageSize := billingPagination(c)
	view, err := e.service.AdminSupportTickets(auth.SupportTicketFilter{
		Status:   strings.TrimSpace(c.Query("status")),
		Keyword:  c.Query("keyword"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, adminSupportTicketPagePayload(view, page, pageSize))
}

func (e *Extension) handleAdminTicketDetail(c *gin.Context) {
	ticket, err := e.service.AdminSupportTicket(strings.TrimSpace(c.Param("id")))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"ticket": billingView(ticket)})
}

func (e *Extension) handleAdminTicketReply(c *gin.Context) {
	ticketID := strings.TrimSpace(c.Param("id"))
	var input struct {
		Body string `json:"body"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "回复内容格式错误")
		return
	}
	ticket, err := e.service.AdminReplySupportTicket(ticketID, adminActorID(c), input.Body)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 客服回复是"已经对用户发出的答复"，必须留痕：事后复盘一次客诉时，只有审计能
	// 回答"是谁在什么时候回了什么状态"。
	e.recordAudit(c, "ticket.reply", "ticket", ticketID, "回复用户工单", gin.H{
		"ticketNo": ticket.TicketNo,
		"status":   ticket.Status,
	})
	respondOK(c, gin.H{"ticket": billingView(ticket)})
}

func (e *Extension) handleAdminTicketStatus(c *gin.Context) {
	ticketID := strings.TrimSpace(c.Param("id"))
	var input struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "请求参数格式错误")
		return
	}
	ticket, err := e.service.UpdateSupportTicketStatus(ticketID, input.Status)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "ticket.status", "ticket", ticketID, "更新工单状态", gin.H{
		"ticketNo": ticket.TicketNo,
		"status":   ticket.Status,
	})
	respondOK(c, gin.H{"ticket": billingView(ticket)})
}
