package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"infinite-canvas/backend/internal/database"
)

func TestDesktopSchemaExcludesHostedTables(t *testing.T) {
	runtime, err := Open(context.Background(), Config{Profile: ProfileDesktop, DataDir: t.TempDir(), ListenAddr: "127.0.0.1:0", AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	for _, table := range []string{
		"auth_sessions", "user_identities", "oauth_states", "email_verification_codes",
		"credit_accounts", "credit_ledger_entries", "billing_orders", "payment_orders",
		"payment_provider_configs", "redeem_codes", "user_oss_settings", "storage_locations",
		"announcements", "canvas_shares", "admin_audit_events", "canvas_moderation",
		"asset_moderation", "canvas_templates", "creation_inspirations", "model_vendors", "vendor_credentials",
	} {
		if runtime.db.Migrator().HasTable(table) {
			t.Fatalf("desktop schema contains hosted table %s", table)
		}
	}
}

// 探活报告的必须是真实结构版本。写死常量时，存量库漏跑迁移（真实版本落后于
// CurrentSchemaVersion）也会报 ready，这类故障只能从业务侧反推。
func TestHealthReportsRealSchemaVersion(t *testing.T) {
	runtime, err := Open(context.Background(), Config{
		Profile:         ProfileDesktop,
		DataDir:         t.TempDir(),
		ListenAddr:      "127.0.0.1:0",
		AutoMigrate:     true,
		ShutdownTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	if err := runtime.Start(); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())

	// 匿名就绪探针：只回健康状态与 checks，不回结构版本对象（版本指纹不该走匿名口）。
	response, err := desktopGet(t, runtime, "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ready status = %d", response.StatusCode)
	}
	var ready struct {
		Data struct {
			Schema json.RawMessage `json:"schema"`
			Checks struct {
				Schema bool `json:"schema"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	if len(ready.Data.Schema) != 0 {
		t.Fatalf("匿名探活不应回结构版本对象，实际 %s", ready.Data.Schema)
	}
	if !ready.Data.Checks.Schema {
		t.Fatal("探活结构检查未就绪")
	}

	// 真实结构版本仍然要在运维口可得，且必须是读库读出来的：写死成常量的话，存量库
	// 漏跑迁移（真实版本落后于 CurrentSchemaVersion）探活依然报 ready，故障只能从业务侧发现。
	versionResponse, err := desktopGet(t, runtime, "/system/version")
	if err != nil {
		t.Fatal(err)
	}
	defer versionResponse.Body.Close()
	if versionResponse.StatusCode != http.StatusOK {
		t.Fatalf("system/version status = %d", versionResponse.StatusCode)
	}
	var version struct {
		Data struct {
			Schema database.SchemaStatus `json:"schema"`
		} `json:"data"`
	}
	if err := json.NewDecoder(versionResponse.Body).Decode(&version); err != nil {
		t.Fatal(err)
	}
	if version.Data.Schema.Current != database.CurrentSchemaVersion || version.Data.Schema.Expected != database.CurrentSchemaVersion {
		t.Fatalf("探活结构版本 = %d/%d，期望 %d/%d", version.Data.Schema.Current, version.Data.Schema.Expected, database.CurrentSchemaVersion, database.CurrentSchemaVersion)
	}
	if !version.Data.Schema.Ready {
		t.Fatal("探活结构检查未就绪")
	}
}

// desktopGet 发一个带桌面令牌的 GET，并保证响应体由调用方关闭。
func desktopGet(t *testing.T, runtime *Runtime, path string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, runtime.BaseURL()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Desktop-Token", runtime.LaunchToken())
	return http.DefaultClient.Do(request)
}
