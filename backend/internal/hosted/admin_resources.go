package hosted

import (
	"net/http"
	"strings"
	"time"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

// 生成产物对账。
//
// 与「素材管理」看的不是同一批数据：素材管理读的是客户端回写的 assets，而产物是
// resources 全量。用户那边回写失败、或者产物压根没进素材库与画布时，只有看 resources
// 才能对账——上游扣了钱、产出了结果，前台却显示失败的情况就藏在这里。

// registerAdminResourceRoutes 挂载产物对账路由，group 已带管理员守卫。
func (e *Extension) registerAdminResourceRoutes(group *gin.RouterGroup) {
	group.GET("/resources", e.handleAdminResourceList)
	// 历史回填是一次性维护动作，但可以重复执行（只补空的 task_id），因此做成
	// 幂等接口而不是启动任务：运维要能自己决定什么时候跑、跑完立刻看到结果。
	group.POST("/resources/backfill", e.handleAdminResourceBackfill)
}

// handleAdminResourceBackfill 把历史产物关联回生成它们的任务。
//
// dryRun=true 先演练：回填会改数据，先看一眼"能对上多少、还剩多少"，比直接写库再
// 发现口径不对要便宜得多。
func (e *Extension) handleAdminResourceBackfill(c *gin.Context) {
	dryRun := adminBoolQuery(c, "dryRun")
	result, err := e.canvas.BackfillResourceProvenance(dryRun)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "resource.backfill", "resource", "", "回填产物溯源", gin.H{
		"dryRun": dryRun, "linked": result.Linked, "unmatched": result.Unmatched,
	})
	respondOK(c, gin.H{"result": result})
}

func (e *Extension) handleAdminResourceList(c *gin.Context) {
	page, pageSize := billingPagination(c)
	since, err := adminTimeQuery(c, "since")
	if err != nil {
		respondFailure(c, http.StatusBadRequest, "since 时间格式无效")
		return
	}
	until, err := adminTimeQuery(c, "until")
	if err != nil {
		respondFailure(c, http.StatusBadRequest, "until 时间格式无效")
		return
	}
	view, err := e.canvas.AdminResourcePage(repository.AdminResourceFilter{
		Keyword:          c.Query("keyword"),
		Kind:             c.Query("kind"),
		UserID:           c.Query("userId"),
		Since:            since,
		Until:            until,
		UnreferencedOnly: adminBoolQuery(c, "unreferenced"),
		UntrackedOnly:    adminBoolQuery(c, "untracked"),
		Page:             page,
		PageSize:         pageSize,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.mergeResourceOwnerNames(view.Resources)
	e.enrichResourceReconciliation(view)
	respondOK(c, view)
}

// adminBoolQuery 解析查询串里的布尔开关。
//
// 前端发 true/1 都认，写错成别的值按"没开"处理：筛选开关写错时返回全量数据，
// 好过直接 400 让管理员连页面都打不开。
func adminBoolQuery(c *gin.Context, name string) bool {
	raw := strings.TrimSpace(c.Query(name))
	return strings.EqualFold(raw, "true") || raw == "1"
}

// adminTimeQuery 解析筛选时间。
//
// 只认 RFC3339 与「2006-01-02」两种写法：前者是前端 toISOString 的结果，后者方便
// 手工在地址栏按天圈范围；平台部署在 Asia/Shanghai，按天筛选时用服务器本地时区更符合直觉。
func adminTimeQuery(c *gin.Context, name string) (time.Time, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local)
	return parsed, err
}

// mergeResourceOwnerNames 用账号库的昵称覆盖画布库读到的名字，理由同素材管理：
// 账号库才是昵称的权威来源，画布库只是延迟副本。
func (e *Extension) mergeResourceOwnerNames(resources []app.AdminResourceView) {
	if len(resources) == 0 || e.service == nil {
		return
	}
	ids := make([]string, 0, len(resources))
	for _, resource := range resources {
		ids = append(ids, resource.UserID)
	}
	owners, err := e.service.AdminUsersByIDs(ids)
	if err != nil {
		return
	}
	for index := range resources {
		if name := owners[resources[index].UserID].Name; name != "" {
			resources[index].UserName = name
		}
	}
}
