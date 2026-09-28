package hosted

import (
	"net/http"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

// 素材资源管理与处置。
//
// 素材与审核状态同在画布库（不像画布审核那样是托管专属表），所有者昵称也先由画布库
// 补齐；账号库只做一次覆盖，让后台里展示的名字与账号管理一致。

// registerAdminAssetRoutes 挂载素材资源路由，group 已带管理员守卫。
func (e *Extension) registerAdminAssetRoutes(group *gin.RouterGroup) {
	group.GET("/assets", e.handleAdminAssetList)
	group.GET("/assets/:id", e.handleAdminAssetDetail)
	group.POST("/assets/:id/moderation", e.handleAdminAssetModeration)
}

func (e *Extension) handleAdminAssetList(c *gin.Context) {
	page, pageSize := billingPagination(c)
	view, err := e.canvas.AdminAssetPage(repository.AdminAssetFilter{
		Keyword:  c.Query("keyword"),
		Status:   c.Query("status"),
		Kind:     c.Query("kind"),
		UserID:   c.Query("userId"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.mergeAssetOwnerNames(view.Assets)
	respondOK(c, view)
}

func (e *Extension) handleAdminAssetDetail(c *gin.Context) {
	view, err := e.canvas.AdminAssetDetail(c.Param("id"))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	if e.service != nil {
		if owners, ownerErr := e.service.AdminUsersByIDs([]string{view.UserID}); ownerErr == nil {
			if name := owners[view.UserID].Name; name != "" {
				view.UserName = name
			}
		}
	}
	respondOK(c, gin.H{"asset": view})
}

func (e *Extension) handleAdminAssetModeration(c *gin.Context) {
	var input struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "素材状态参数格式错误")
		return
	}
	actorID := ""
	if actor := adminActor(c); actor != nil {
		actorID = actor.ID
	}
	assetID := c.Param("id")
	view, err := e.canvas.ModerateAsset(assetID, input.Status, input.Reason, actorID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 处置是写操作，必须留痕；审计的 actor 由会话推导，不信任请求体。
	e.recordAudit(c, "asset.moderate", "asset", assetID, "素材处置", gin.H{
		"status": view.ModerationStatus, "reason": view.ModerationReason,
	})
	respondOK(c, gin.H{"asset": view})
}

// mergeAssetOwnerNames 用账号库的昵称覆盖画布库读到的名字。
//
// 覆盖而不是拼接：账号库是昵称的权威来源，画布库的 workspaces.name 只是它的一份
// 延迟副本；取不到时保留原值，避免一次账号库抖动让整页列表失去所有者信息。
func (e *Extension) mergeAssetOwnerNames(assets []app.AdminAssetRowView) {
	if len(assets) == 0 || e.service == nil {
		return
	}
	ids := make([]string, 0, len(assets))
	for _, asset := range assets {
		ids = append(ids, asset.UserID)
	}
	owners, err := e.service.AdminUsersByIDs(ids)
	if err != nil {
		return
	}
	for index := range assets {
		if name := owners[assets[index].UserID].Name; name != "" {
			assets[index].UserName = name
		}
	}
}
