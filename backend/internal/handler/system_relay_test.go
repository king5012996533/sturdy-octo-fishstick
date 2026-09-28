package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type systemRelayTestEnv struct {
	router  *gin.Engine
	service *app.Service
	db      *gorm.DB
}

// newSystemRelayTestEnv 搭一个托管形态的最小运行时：系统渠道 + 转发路由 + 会话账号。
// modelsJSON 是渠道表上的旧目录缓存，测试用它验证「缓存过期也不能误拒已配置模型」。
func newSystemRelayTestEnv(t *testing.T, baseURL string, modelsJSON string) *systemRelayTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.ModelChannel{}, &model.ChannelModel{}); err != nil {
		t.Fatal(err)
	}
	channel := model.ModelChannel{
		ID: "sys-relay", UserID: "platform", Scope: model.ChannelScopeSystem, Enabled: true,
		Name: "平台转发渠道", PublicAlias: "平台渠道", BaseURL: baseURL, APIKey: "sk-platform-secret",
		APIFormat: "openai", ModelsJSON: modelsJSON,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	svc := app.New(repository.New(db), t.TempDir())
	t.Cleanup(func() { _ = svc.Close() })

	router := gin.New()
	group := router.Group("/api")
	group.Use(RuntimeDependenciesMiddleware(defaultRuntimeDependencies(svc)))
	group.Use(func(c *gin.Context) { SetSessionUser(c, model.User{ID: "user-1", DisplayName: "测试用户"}) })
	RegisterSystemRelayRoutes(group, svc)
	return &systemRelayTestEnv{router: router, service: svc, db: db}
}

func (e *systemRelayTestEnv) addModel(t *testing.T, item model.ChannelModel) {
	t.Helper()
	item.ChannelID = "sys-relay"
	item.Enabled = true
	if err := e.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSystemRelayForwardsWithPlatformCredentialAndRewritesModel(t *testing.T) {
	const apiKey = "sk-platform-secret"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization = %q", got)
		}
		for _, name := range []string{"Cookie", "Origin", "Referer"} {
			if value := r.Header.Get(name); value != "" {
				t.Errorf("upstream received browser header %s = %q", name, value)
			}
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "upstream-real-model") {
			t.Errorf("upstream model was not rewritten: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"cmpl-1","echo":"`+apiKey+`"}`)
	}))
	defer upstream.Close()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")

	env := newSystemRelayTestEnv(t, upstream.URL, `["kino-text"]`)
	env.addModel(t, model.ChannelModel{
		ID: "cm-text", ModelKey: "kino-text", ProviderModelKey: "upstream-real-model",
		DisplayName: "平台文本模型", Capability: "text", Protocol: model.ChannelInterfaceChatCompletion,
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ai/system/sys-relay/chat/completions", strings.NewReader(`{"model":"kino-text","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", "kinotv_session=browser-cookie")
	env.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), apiKey) {
		t.Fatal("response leaked the platform API key")
	}
	if !strings.Contains(recorder.Body.String(), "[REDACTED]") {
		t.Fatalf("upstream echo should be redacted, body = %s", recorder.Body.String())
	}
}

func TestSystemRelayAcceptsModelMissingFromLegacyCatalogCache(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")

	// 渠道表上的 ModelsJSON 是建渠道时的快照，模型后续增删不会同步；
	// 授权必须认 channel_models，否则后台加完模型前台仍然 403。
	env := newSystemRelayTestEnv(t, upstream.URL, `[]`)
	env.addModel(t, model.ChannelModel{
		ID: "cm-text", ModelKey: "kino-text", DisplayName: "平台文本模型",
		Capability: "text", Protocol: model.ChannelInterfaceChatCompletion,
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ai/system/sys-relay/chat/completions", strings.NewReader(`{"model":"kino-text","messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestSystemRelayRejectsUnapprovedModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be reached for an unapproved model")
	}))
	defer upstream.Close()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")

	env := newSystemRelayTestEnv(t, upstream.URL, `["kino-text"]`)
	env.addModel(t, model.ChannelModel{
		ID: "cm-text", ModelKey: "kino-text", DisplayName: "平台文本模型",
		Capability: "text", Protocol: model.ChannelInterfaceChatCompletion,
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ai/system/sys-relay/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestSystemRelayModelListComesFromPlatformCatalog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("model list must be answered from the platform catalog, not upstream")
	}))
	defer upstream.Close()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")

	env := newSystemRelayTestEnv(t, upstream.URL, `["kino-text"]`)
	env.addModel(t, model.ChannelModel{
		ID: "cm-text", ModelKey: "kino-text", ProviderModelKey: "upstream-real-model",
		DisplayName: "平台文本模型", Capability: "text", Protocol: model.ChannelInterfaceChatCompletion,
	})

	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ai/system/sys-relay/models", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "kino-text") {
		t.Fatalf("model list should publish aliases, body = %s", body)
	}
	if strings.Contains(body, "upstream-real-model") {
		t.Fatal("model list must not leak the upstream SKU")
	}
}

func TestSystemRelayGeminiPathUsesProtocolPrefixAndQuery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/upstream-gemini:streamGenerateContent" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		if r.URL.RawQuery != "alt=sse" {
			t.Errorf("upstream query = %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "sk-platform-secret" {
			t.Errorf("x-goog-api-key = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
	}))
	defer upstream.Close()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")

	env := newSystemRelayTestEnv(t, upstream.URL, `["kino-gemini"]`)
	env.addModel(t, model.ChannelModel{
		ID: "cm-gemini", ModelKey: "kino-gemini", ProviderModelKey: "upstream-gemini",
		DisplayName: "平台图片模型", Capability: "image", Protocol: model.ChannelInterfaceGeminiImage,
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ai/system/sys-relay/models/kino-gemini:streamGenerateContent?alt=sse", strings.NewReader(`{"contents":[]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	env.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "candidates") {
		t.Fatalf("stream body = %s", recorder.Body.String())
	}
}

func TestSystemRelayRejectsCredentialInQuery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be reached when the query carries a credential")
	}))
	defer upstream.Close()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")

	env := newSystemRelayTestEnv(t, upstream.URL, `["kino-text"]`)
	env.addModel(t, model.ChannelModel{
		ID: "cm-text", ModelKey: "kino-text", DisplayName: "平台文本模型",
		Capability: "text", Protocol: model.ChannelInterfaceChatCompletion,
	})

	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ai/system/sys-relay/models?api_key=stolen", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestSystemRelayDisabledWhenFrontendModelsEnabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be reached when the platform relay is disabled")
	}))
	defer upstream.Close()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")

	env := newSystemRelayTestEnv(t, upstream.URL, `["kino-text"]`)
	env.addModel(t, model.ChannelModel{
		ID: "cm-text", ModelKey: "kino-text", DisplayName: "平台文本模型",
		Capability: "text", Protocol: model.ChannelInterfaceChatCompletion,
	})
	if err := env.db.Create(&model.SystemSetting{Key: "feature_availability", ValueJSON: `{"frontendModelsEnabled":true}`}).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/ai/system/sys-relay/chat/completions", strings.NewReader(`{"model":"kino-text","messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}
