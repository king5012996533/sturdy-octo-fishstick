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
	request, err := http.NewRequest(http.MethodGet, runtime.BaseURL()+"/health/ready", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Desktop-Token", runtime.LaunchToken())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ready status = %d", response.StatusCode)
	}
	var envelope struct {
		Data struct {
			Schema database.SchemaStatus `json:"schema"`
			Checks struct {
				Schema bool `json:"schema"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Schema.Current != database.CurrentSchemaVersion || envelope.Data.Schema.Expected != database.CurrentSchemaVersion {
		t.Fatalf("探活结构版本 = %d/%d，期望 %d/%d", envelope.Data.Schema.Current, envelope.Data.Schema.Expected, database.CurrentSchemaVersion, database.CurrentSchemaVersion)
	}
	if !envelope.Data.Schema.Ready || !envelope.Data.Checks.Schema {
		t.Fatal("探活结构检查未就绪")
	}
}
