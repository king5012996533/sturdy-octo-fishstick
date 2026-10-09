package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

// 托管形态的匿名白名单只放行 /api/health/live 与 /api/health/ready（见 internal/hosted）。
// 它们不能把版本与内部表结构版本送出去：匿名端点等于公开端点，版本号就是给攻击者的
// 一份"该用哪个漏洞"的索引。
func TestAnonymousHealthDoesNotLeakBuildOrSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: "file:anon-health?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateLocalSchema(db); err != nil {
		t.Fatal(err)
	}
	status := newSystemStatus(db, app.New(repository.New(db), t.TempDir()))
	status.markStarted()
	router := gin.New()
	registerSystemStatusRoutes(router.Group("/api"), status)

	for _, path := range []string{"/api/health", "/api/health/ready", "/api/health/startup", "/api/health/live"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, recorder.Code, recorder.Body.String())
		}
		body := recorder.Body.String()

		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("%s 解不开：%v", path, err)
		}
		// 顶层不得出现 build（版本/提交/构建时间/Go 版本）与 schema（内部表结构版本对象）。
		for _, forbidden := range []string{"build", "schema"} {
			if _, exists := envelope.Data[forbidden]; exists {
				t.Fatalf("%s 匿名探活泄露了 %q：%s", path, forbidden, body)
			}
		}
		// 版本指纹的具体字段名一个都不该出现在任何层级。
		// 注意 checks.schema 是布尔健康标志，不是表结构版本，不在禁止之列。
		for _, forbidden := range []string{"version", "commit", "buildTime", "goVersion"} {
			if strings.Contains(body, `"`+forbidden+`"`) {
				t.Fatalf("%s 响应文本里出现 %q：%s", path, forbidden, body)
			}
		}
		if _, ok := envelope.Data["status"]; !ok {
			t.Fatalf("%s 缺少 status，探活就没意义了：%s", path, body)
		}
	}
}

// /system/version 是运维链路，保留 build 与 schema；在托管形态下它要求登录。
// 这里只钉住"内容还在"，避免上面那轮收窄顺手把它也掏空。
func TestSystemVersionStillExposesBuildAndSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: "file:sys-version?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateLocalSchema(db); err != nil {
		t.Fatal(err)
	}
	status := newSystemStatus(db, app.New(repository.New(db), t.TempDir()))
	status.markStarted()
	router := gin.New()
	registerSystemStatusRoutes(router.Group("/api"), status)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/system/version", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, want := range []string{`"build"`, `"schema"`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("/system/version 丢了 %s：%s", want, recorder.Body.String())
		}
	}
}
