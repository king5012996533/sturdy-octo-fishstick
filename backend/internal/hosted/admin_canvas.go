package hosted

import (
	"net/http"
	"strings"
	"time"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// 画布内容管理与审核。
//
// 这一组接口横跨两个库：画布与审核状态在画布库，所有者昵称在账号库。合并放在
// 托管层，是因为只有这里同时持有两边；画布侧服务不适合知道账号库的存在。

// adminCanvasRow 是列表里的一行：画布 + 所有者标识。
type adminCanvasRow struct {
	app.AdminCanvasListItem
	OwnerName  string `json:"ownerName"`
	OwnerEmail string `json:"ownerEmail"`
	OwnerPhone string `json:"ownerPhone"`
}

type adminCanvasPageResponse struct {
	Canvases []adminCanvasRow `json:"canvases"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"pageSize"`
}

func (e *Extension) registerCanvasModerationRoutes(group *gin.RouterGroup) {
	group.GET("/canvases", e.handleAdminCanvasList)
	group.GET("/canvases/:id", e.handleAdminCanvasDetail)
	group.PATCH("/canvases/:id/moderation", e.handleAdminCanvasModeration)
}

// registerOwnCanvasModerationRoutes 挂用户自己的下架提示。
//
// 用户端需要一个只读入口回答"我的哪块画布被下架了、为什么"。没有它，用户只能
// 在点开画布被 403 之后才知道发生了什么。
func (e *Extension) registerOwnCanvasModerationRoutes(api *gin.RouterGroup) {
	api.GET("/canvas-moderation/mine", e.handleOwnCanvasModeration)
}

func (e *Extension) handleAdminCanvasList(c *gin.Context) {
	page, err := e.canvas.AdminCanvasListView(canvasActor(adminActor(c)), app.AdminCanvasQuery{
		Keyword: c.Query("keyword"),
		UserID:  c.Query("userId"),
		Status:  c.Query("status"),
		Page:    adminIntQuery(c, "page"),
		Limit:   adminIntQuery(c, "pageSize"),
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	rows := make([]adminCanvasRow, 0, len(page.Canvases))
	ownerIDs := make([]string, 0, len(page.Canvases))
	for _, item := range page.Canvases {
		rows = append(rows, adminCanvasRow{AdminCanvasListItem: item})
		ownerIDs = append(ownerIDs, item.UserID)
	}
	// 所有者昵称取不到就退化成空字符串：一块画布少个名字不该让整页审核列表失败。
	if owners, ownerErr := e.service.AdminUsersByIDs(ownerIDs); ownerErr == nil {
		for index := range rows {
			owner := owners[rows[index].UserID]
			rows[index].OwnerName = owner.Name
			rows[index].OwnerEmail = owner.Email
			rows[index].OwnerPhone = owner.Phone
		}
	}
	respondOK(c, adminCanvasPageResponse{Canvases: rows, Total: page.Total, Page: page.Page, PageSize: page.PageSize})
}

// adminCanvasDetailResponse 在详情上补所有者信息。
type adminCanvasDetailResponse struct {
	*app.AdminCanvasDetail
	OwnerName  string `json:"ownerName"`
	OwnerEmail string `json:"ownerEmail"`
	OwnerPhone string `json:"ownerPhone"`
}

func (e *Extension) handleAdminCanvasDetail(c *gin.Context) {
	detail, err := e.canvas.AdminCanvasDetailView(canvasActor(adminActor(c)), c.Param("id"))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	response := adminCanvasDetailResponse{AdminCanvasDetail: detail}
	if owners, ownerErr := e.service.AdminUsersByIDs([]string{detail.UserID}); ownerErr == nil {
		owner := owners[detail.UserID]
		response.OwnerName = owner.Name
		response.OwnerEmail = owner.Email
		response.OwnerPhone = owner.Phone
	}
	respondOK(c, response)
}

func (e *Extension) handleAdminCanvasModeration(c *gin.Context) {
	var input struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "内容状态参数格式错误")
		return
	}
	status := model.CanvasModerationStatus(strings.ToUpper(strings.TrimSpace(input.Status)))
	item, err := e.canvas.AdminSetCanvasModeration(canvasActor(adminActor(c)), c.Param("id"), status, input.Reason)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// AdminSetCanvasModeration 内部已经写了审计（与其它处置共用同一出口），这里
	// 只把结果回给前端。
	respondOK(c, item)
}

// ownCanvasModerationResponse 是用户自己的下架清单。
type ownCanvasModerationResponse struct {
	CanvasID  string    `json:"canvasId"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (e *Extension) handleOwnCanvasModeration(c *gin.Context) {
	user, err := e.service.SessionUser(c.Request.Context(), auth.ReadSessionToken(c.Request))
	if err != nil {
		respondFailure(c, http.StatusUnauthorized, "当前未登录或登录已失效")
		return
	}
	items, err := e.canvas.OwnCanvasModeration(&model.User{ID: user.ID, DisplayName: user.Name, Role: model.UserRoleUser})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	response := make([]ownCanvasModerationResponse, 0, len(items))
	for _, item := range items {
		response = append(response, ownCanvasModerationResponse{CanvasID: item.CanvasID, Status: string(item.Status), Reason: item.Reason, UpdatedAt: item.UpdatedAt})
	}
	respondOK(c, gin.H{"canvases": response})
}
