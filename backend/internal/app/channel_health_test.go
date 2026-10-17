package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReconcileAdminChannelModelsFlagsMissingUpstreamModel(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			// Fseadanco2.0mini 还在授权目录里，kksedanco2.5 已经消失——这正是要抓的换货。
			_, _ = w.Write([]byte(`{"data":[{"id":"Fseadanco2.0mini"},{"id":"other-model"}]}`))
		case "/v1/models/kksedanco2.5":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer upstream.Close()

	svc, db := newChannelHealthTestService(t)
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	channel := model.ModelChannel{ID: "channel-1", Scope: model.ChannelScopeSystem, Enabled: true, Name: "纵横科技 · 主账号", BaseURL: upstream.URL + "/v1", APIKey: "key", APIFormat: "openai"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	items := []model.ChannelModel{
		{ID: "model-mini", ChannelID: channel.ID, ModelKey: "seadanco2.0mini", ProviderModelKey: "Fseadanco2.0mini", Enabled: true},
		{ID: "model-25", ChannelID: channel.ID, ModelKey: "seadanco2.5", ProviderModelKey: "kksedanco2.5", Enabled: true},
		{ID: "model-paused", ChannelID: channel.ID, ModelKey: "paused-model", ProviderModelKey: "gone-but-disabled", Enabled: false},
	}
	if err := db.Create(&items).Error; err != nil {
		t.Fatal(err)
	}

	result, err := svc.ReconcileAdminChannelModels(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(result.Rows))
	}
	row := result.Rows[0]
	if row.Status != ChannelReconcileDrift || row.MissingCount != 1 {
		t.Fatalf("row status=%s missing=%d, want drift/1", row.Status, row.MissingCount)
	}
	if result.Summary.Drifted != 1 || result.Summary.Checked != 1 {
		t.Fatalf("summary = %+v", result.Summary)
	}
	statuses := map[string]string{}
	for _, item := range row.Models {
		statuses[item.ModelKey] = item.Status
	}
	if statuses["seadanco2.0mini"] != ChannelReconcileModelOK {
		t.Fatalf("mini status = %q, want ok", statuses["seadanco2.0mini"])
	}
	if statuses["seadanco2.5"] != ChannelReconcileModelMissing {
		t.Fatalf("2.5 status = %q, want missing", statuses["seadanco2.5"])
	}
	if _, ok := statuses["paused-model"]; ok {
		t.Fatal("停用的模型不该进入对账结果")
	}
}

func TestReconcileAdminChannelModelsChecksVariantKeys(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"TTP-grok"}]}`))
	}))
	defer upstream.Close()

	svc, db := newChannelHealthTestService(t)
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	channel := model.ModelChannel{ID: "channel-2", Scope: model.ChannelScopeSystem, Enabled: true, Name: "grok 渠道", BaseURL: upstream.URL + "/v1", APIKey: "key", APIFormat: "openai"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	item := model.ChannelModel{ID: "model-grok", ChannelID: channel.ID, ModelKey: "grok-imagine-video/v1.5", ProviderModelKey: "", Enabled: true}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	variants := []model.ChannelModelVariant{
		{ID: "variant-480", ChannelModelID: item.ID, SelectorKey: `{"vquality":"480p"}`, SelectorJSON: `{"vquality":"480p"}`, ProviderModelKey: "TTP-grok", Enabled: true},
		{ID: "variant-720", ChannelModelID: item.ID, SelectorKey: `{"vquality":"720p"}`, SelectorJSON: `{"vquality":"720p"}`, ProviderModelKey: "TTP-grok", Enabled: true},
	}
	if err := db.Create(&variants).Error; err != nil {
		t.Fatal(err)
	}

	result, err := svc.ReconcileAdminChannelModels(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0].Status != ChannelReconcileOK {
		t.Fatalf("rows = %+v, want one ok row", result.Rows)
	}
}

// 目录接口分页（Replicate 一页 25 条）会把没翻到的模型报成缺失；单模型接口
// 说它还在，就不能算漂移。
func TestReconcileAdminChannelModelsIgnoresPaginatedCatalogGaps(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"results":[{"owner":"black-forest-labs","name":"flux-schnell"}]}`))
			return
		}
		// 单模型接口命中：说明只是目录分页没覆盖到。
		_, _ = w.Write([]byte(`{"id":"openai/gpt-image-2"}`))
	}))
	defer upstream.Close()

	svc, db := newChannelHealthTestService(t)
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	channel := model.ModelChannel{ID: "channel-3", Scope: model.ChannelScopeSystem, Enabled: true, Name: "Replicate", BaseURL: upstream.URL + "/v1", APIKey: "key", APIFormat: "openai"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	item := model.ChannelModel{ID: "model-gpt-image", ChannelID: channel.ID, ModelKey: "openai/gpt-image-2", ProviderModelKey: "openai/gpt-image-2", Enabled: true}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}

	result, err := svc.ReconcileAdminChannelModels(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0].Status != ChannelReconcileOK || result.Rows[0].MissingCount != 0 {
		t.Fatalf("rows = %+v, want one ok row without drift", result.Rows)
	}
}

// 上游根本没有 /models 接口（fal）时是"不支持对账"，不是渠道故障。
func TestReconcileAdminChannelModelsMarksMissingCatalogAsUnsupported(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"not found"}`))
	}))
	defer upstream.Close()

	svc, db := newChannelHealthTestService(t)
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	channel := model.ModelChannel{ID: "channel-4", Scope: model.ChannelScopeSystem, Enabled: true, Name: "fal.ai", BaseURL: upstream.URL + "/v1", APIKey: "key", APIFormat: "openai"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	item := model.ChannelModel{ID: "model-fal", ChannelID: channel.ID, ModelKey: "openai/gpt-image-2", ProviderModelKey: "openai/gpt-image-2", Enabled: true}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}

	result, err := svc.ReconcileAdminChannelModels(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0].Status != ChannelReconcileUnsupported {
		t.Fatalf("rows = %+v, want unsupported", result.Rows)
	}
	if result.Summary.Unsupported != 1 || result.Summary.Failed != 0 {
		t.Fatalf("summary = %+v, want unsupported=1 failed=0", result.Summary)
	}
}

func TestAdminChannelHealthGroupsOnlyCreateCalls(t *testing.T) {
	svc, db := newChannelHealthTestService(t)
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	now := time.Now()
	logs := []model.ApiCallLog{
		{ID: "ok-1", ChannelID: "CHANNEL_A", Model: "seadanco2.0mini", RequestKind: "create", Status: model.ApiCallStatusSucceeded, CreatedAt: now.Add(-time.Hour)},
		{ID: "fail-1", ChannelID: "CHANNEL_A", Model: "seadanco2.0mini", RequestKind: "create", Status: model.ApiCallStatusFailed, ErrorCode: "unknown", StatusCode: 0, Error: "提交结果未确认", CreatedAt: now.Add(-30 * time.Minute)},
		{ID: "fail-2", ChannelID: "CHANNEL_A", Model: "seadanco2.0mini", RequestKind: "create", Status: model.ApiCallStatusFailed, ErrorCode: "video_service_unavailable", StatusCode: 502, Error: "上游 502", CreatedAt: now.Add(-10 * time.Minute)},
		// poll/download 不是一次新的提交，混进来会把失败率算大。
		{ID: "poll-1", ChannelID: "CHANNEL_A", Model: "seadanco2.0mini", RequestKind: "poll", Status: model.ApiCallStatusFailed, CreatedAt: now.Add(-5 * time.Minute)},
		{ID: "download-1", ChannelID: "CHANNEL_A", Model: "seadanco2.0mini", RequestKind: "download", Status: model.ApiCallStatusFailed, CreatedAt: now.Add(-4 * time.Minute)},
		// 已经过去的窗口不该被计入。
		{ID: "old-1", ChannelID: "CHANNEL_A", Model: "seadanco2.0mini", RequestKind: "create", Status: model.ApiCallStatusFailed, CreatedAt: now.AddDate(0, 0, -40)},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}

	result, err := svc.AdminChannelHealth(admin, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(result.Rows))
	}
	row := result.Rows[0]
	if row.Total != 3 || row.Succeeded != 1 || row.Failed != 2 {
		t.Fatalf("row = %+v, want total=3 succeeded=1 failed=2", row)
	}
	if row.LastError != "上游 502" {
		t.Fatalf("lastError = %q, want 上游 502", row.LastError)
	}
	if len(row.ErrorCodes) != 2 {
		t.Fatalf("errorCodes = %+v, want 2", row.ErrorCodes)
	}
	if result.Totals.Total != 3 || result.Totals.Failed != 2 {
		t.Fatalf("totals = %+v", result.Totals)
	}
}

func newChannelHealthTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+newID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ModelChannel{}, &model.ChannelModel{}, &model.ChannelModelVariant{}, &model.ApiCallLog{}); err != nil {
		t.Fatal(err)
	}
	return &Service{repo: repository.New(db)}, db
}
