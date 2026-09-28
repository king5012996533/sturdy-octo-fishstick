package hosted

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const canvasPayload = `{"project":{"id":"canvas-mod-1","revision":0,"title":"待审核画布","nodes":[{"id":"n1","type":"text","title":"文本节点","metadata":{"content":"正文"}}],"connections":[]}}`

func seedCanvas(t *testing.T, router *gin.Engine, cookie *http.Cookie, canvasID string) {
	t.Helper()
	recorder := perform(router, http.MethodPut, "/api/canvas-projects/"+canvasID, canvasPayload, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("创建画布失败：%d %s", recorder.Code, recorder.Body.String())
	}
}

// TestHostedCanvasModerationBlocksOwner 覆盖「下架 → 用户读写被拒 → 恢复」。
func TestHostedCanvasModerationBlocksOwner(t *testing.T) {
	extension, router, authDB, canvasDB := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-canvas@example.com")
	promoteToAdmin(t, authDB, adminID)
	ownerCookie, _ := registerAccount(t, router, authDB, "owner@example.com")
	seedCanvas(t, router, ownerCookie, "canvas-mod-1")

	recorder := perform(router, http.MethodGet, "/api/admin/canvases", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("画布列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Data struct {
			Canvases []struct {
				ID               string `json:"id"`
				Title            string `json:"title"`
				OwnerEmail       string `json:"ownerEmail"`
				ModerationStatus string `json:"moderationStatus"`
			} `json:"canvases"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析画布列表失败: %v %s", err, recorder.Body.String())
	}
	if page.Data.Total < 1 || len(page.Data.Canvases) == 0 {
		t.Fatalf("画布列表为空：%s", recorder.Body.String())
	}
	if page.Data.Canvases[0].ModerationStatus != "NORMAL" || page.Data.Canvases[0].OwnerEmail != "owner@example.com" {
		t.Fatalf("列表行缺少所有者或默认状态：%s", recorder.Body.String())
	}

	// 详情要能看到内容摘要，否则审核员只能凭标题判断。
	recorder = perform(router, http.MethodGet, "/api/admin/canvases/canvas-mod-1", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "正文") || !strings.Contains(recorder.Body.String(), `"nodeCount":1`) {
		t.Fatalf("画布详情缺少结构统计：%d %s", recorder.Code, recorder.Body.String())
	}

	// 下架必须带理由：没有理由的处置无法向用户解释，也无法复盘。
	if recorder := perform(router, http.MethodPatch, "/api/admin/canvases/canvas-mod-1/moderation", `{"status":"HIDDEN"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("空理由下架应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodPatch, "/api/admin/canvases/canvas-mod-1/moderation", `{"status":"HIDDEN","reason":"含违规内容"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("下架失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 用户侧随即不可读、不可写、不可删除，也不允许从历史里翻出来。
	// 单块读取沿用 404（不泄露画布是否存在），但必须带上可读的下架原因。
	if recorder := perform(router, http.MethodGet, "/api/canvas-projects/canvas-mod-1", "", ownerCookie); recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "含违规内容") {
		t.Fatalf("下架画布仍可读取：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPut, "/api/canvas-projects/canvas-mod-1", canvasPayload, ownerCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("下架画布仍可写入：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodDelete, "/api/canvas-projects/canvas-mod-1", "", ownerCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("下架画布仍可删除：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/canvas-projects/canvas-mod-1/history", "", ownerCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("下架画布仍可读取历史：%d %s", recorder.Code, recorder.Body.String())
	}

	// 用户端要能看到"哪块被下架了、为什么"。
	recorder = perform(router, http.MethodGet, "/api/canvas-moderation/mine", "", ownerCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "含违规内容") || !strings.Contains(recorder.Body.String(), "canvas-mod-1") {
		t.Fatalf("用户下架清单异常：%d %s", recorder.Code, recorder.Body.String())
	}

	// 恢复后必须立刻可以继续编辑。
	if recorder := perform(router, http.MethodPatch, "/api/admin/canvases/canvas-mod-1/moderation", `{"status":"NORMAL","reason":"误判，已恢复"}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("恢复失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/canvas-projects/canvas-mod-1", "", ownerCookie); recorder.Code != http.StatusOK {
		t.Fatalf("恢复后仍不可读：%d %s", recorder.Code, recorder.Body.String())
	}

	// 处置必须留痕，且留痕里带理由。
	var auditCount int64
	if err := canvasDB.Table("admin_audit_events").Where("action = ?", "canvas.moderation").Count(&auditCount).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if auditCount != 2 {
		t.Fatalf("两次处置应留下 2 条审计，实际 %d 条", auditCount)
	}

	// 按状态筛选：下架过的画布在"已下架"里查不到，因为已经恢复成正常了。
	recorder = perform(router, http.MethodGet, "/api/admin/canvases?status=HIDDEN", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("按状态筛选失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var hiddenPage struct {
		Data struct {
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &hiddenPage); err != nil || hiddenPage.Data.Total != 0 {
		t.Fatalf("已恢复的画布不应出现在已下架列表：%s", recorder.Body.String())
	}
}

// TestHostedCanvasModerationRequiresAdmin 确认画布审核不是普通账号可用的接口。
func TestHostedCanvasModerationRequiresAdmin(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()
	cookie, _ := registerAccount(t, router, authDB, "plain-canvas@example.com")
	if recorder := perform(router, http.MethodGet, "/api/admin/canvases", "", cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号读取画布列表应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPatch, "/api/admin/canvases/whatever/moderation", `{"status":"HIDDEN","reason":"x"}`, cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号处置画布应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
