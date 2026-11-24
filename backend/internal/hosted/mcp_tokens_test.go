package hosted

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

func performBearer(router *gin.Engine, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestMCPTokenLifecycle(t *testing.T) {
	extension, router, authDB, canvasDB := newAdminRouter(t)
	defer extension.Close()

	cookie, userID := registerAccount(t, router, authDB, "mcp@example.com")

	if recorder := perform(router, http.MethodPost, "/api/integrations/mcp/tokens",
		`{"name":"Claude Code","expiresInDays":30}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录创建令牌应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	recorder := perform(router, http.MethodPost, "/api/integrations/mcp/tokens",
		`{"name":"Claude Code","expiresInDays":30,"permissionMode":"auto","maxCreditsPerRun":1500,"maxCreditsPerDay":3000}`, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("创建令牌失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Data struct {
			Token     string `json:"token"`
			TokenView struct {
				ID      string   `json:"id"`
				Name    string   `json:"name"`
				Prefix  string   `json:"prefix"`
				GrantID string   `json:"grantId"`
				Scopes  []string `json:"scopes"`
			} `json:"tokenView"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Data.Token, "ktv_mcp_") || created.Data.TokenView.ID == "" {
		t.Fatalf("令牌响应异常：%s", recorder.Body.String())
	}
	if len(created.Data.TokenView.Scopes) == 0 {
		t.Fatalf("令牌缺少 scopes：%s", recorder.Body.String())
	}

	recorder = perform(router, http.MethodGet, "/api/integrations/mcp/tokens", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取令牌列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), created.Data.Token) {
		t.Fatalf("令牌明文泄漏到列表：%s", recorder.Body.String())
	}

	var stored model.MCPToken
	if err := canvasDB.Where("id = ?", created.Data.TokenView.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.UserID != userID || stored.TokenHash == created.Data.Token || len(stored.TokenHash) != 64 {
		t.Fatalf("令牌落库异常：user=%s hash=%q", stored.UserID, stored.TokenHash)
	}
	if created.Data.TokenView.GrantID == "" || stored.GrantID != created.Data.TokenView.GrantID {
		t.Fatalf("令牌没有绑定 Grant：view=%q stored=%q", created.Data.TokenView.GrantID, stored.GrantID)
	}
	var grant model.AgentGrant
	if err := canvasDB.First(&grant, "id = ?", stored.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	if grant.PermissionMode != "auto" || grant.MaxCreditsPerRun != 1500 || grant.MaxCreditsPerDay != 3000 || grant.Version != 1 {
		t.Fatalf("授权快照异常：%+v", grant)
	}

	for _, path := range []string{"/api/canvas-projects", "/api/tasks"} {
		if recorder := performBearer(router, http.MethodGet, path, created.Data.Token); recorder.Code != http.StatusOK {
			t.Fatalf("Bearer 访问 %s 失败：%d %s", path, recorder.Code, recorder.Body.String())
		}
	}
	if recorder := performBearer(router, http.MethodGet, "/api/workspace/bootstrap", created.Data.Token); recorder.Code != http.StatusForbidden {
		t.Fatalf("令牌不应访问工作区引导接口，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := performBearer(router, http.MethodPost, "/api/tasks", created.Data.Token); recorder.Code != http.StatusForbidden {
		t.Fatalf("令牌不应直接创建任务，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	// 加额审批只能由用户会话完成。令牌即使带 agent scope，也只能提交申请，
	// 不能列审批单或调用批准接口，否则 Agent 就能绕过用户给自己加额度。
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/agent/budget-requests"},
		{http.MethodPost, "/api/agent/budget-requests/any/approve"},
	} {
		if recorder := performBearer(router, request.method, request.path, created.Data.Token); recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s 应被令牌拒绝，实际 %d：%s", request.method, request.path, recorder.Code, recorder.Body.String())
		}
	}

	recorder = perform(router, http.MethodDelete, "/api/integrations/mcp/tokens/"+created.Data.TokenView.ID, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("吊销令牌失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := performBearer(router, http.MethodGet, "/api/canvas-projects", created.Data.Token); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("吊销后令牌仍可用，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if err := canvasDB.First(&grant, "id = ?", stored.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	if grant.Status != model.AgentGrantStatusRevoked || grant.Version != 2 {
		t.Fatalf("吊销令牌未同步撤销授权：status=%q version=%d", grant.Status, grant.Version)
	}
}
