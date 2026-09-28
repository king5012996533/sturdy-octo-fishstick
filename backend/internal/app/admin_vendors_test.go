package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// vendorTestStatus 取错误里的 HTTP 状态；非结构化错误统一按 500 处理。
func vendorTestStatus(err error) int {
	if err == nil {
		return 0
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Status
	}
	return 500
}

func boolPointer(value bool) *bool { return &value }

// newVendorTestService 装配一套只含厂商层的服务。
//
// 渠道与模型表必须一起迁移：凭据创建会真的去建 system 渠道，不建表就只测到
// “接口存在”，测不到“渠道确实建出来了”。
func newVendorTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&model.ModelVendor{},
		&model.VendorCredential{},
		&model.ModelChannel{},
		&model.ChannelModel{},
		&model.ChannelModelVariant{},
		&model.IDSequence{},
	); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir()), db
}

func vendorTestActor() *model.User {
	return &model.User{ID: "admin-1", DisplayName: "运营", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
}

// TestVendorCatalogAggregatesRegistry 覆盖目录聚合：slug 化标识、能力去重与排序。
func TestVendorCatalogAggregatesRegistry(t *testing.T) {
	svc, _ := newVendorTestService(t)
	items, err := svc.VendorCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("厂商目录不应为空：协议注册表里自带多个厂商")
	}
	byCode := make(map[string]VendorCatalogItem, len(items))
	for _, item := range items {
		byCode[item.Code] = item
	}

	// slug 化：厂商名里的空格变成连字符。
	volcengine, ok := byCode["volcengine-ark"]
	if !ok {
		t.Fatalf("目录缺少 volcengine-ark：%v", vendorCodes(items))
	}
	if volcengine.Name != "Volcengine Ark" || !vendorContainsString(volcengine.Protocols, "volcengine-ark-video") {
		t.Fatalf("volcengine-ark 聚合结果不符：%#v", volcengine)
	}

	// OpenAI 注册了两个文本协议，能力去重后应只剩一个 TEXT。
	openai, ok := byCode["openai"]
	if !ok {
		t.Fatalf("目录缺少 openai：%v", vendorCodes(items))
	}
	if len(openai.Capabilities) != 1 || openai.Capabilities[0] != "TEXT" {
		t.Fatalf("openai 能力应去重为 [TEXT]：%#v", openai.Capabilities)
	}
	if !vendorContainsString(openai.Protocols, "chat-completion") || !vendorContainsString(openai.Protocols, "openai-response") {
		t.Fatalf("openai 协议清单不完整：%#v", openai.Protocols)
	}
	if !vendorContainsString(byCode["google"].Protocols, "gemini-veo") {
		t.Fatalf("目录缺少 google 的视频协议：%#v", byCode["google"])
	}

	// 空厂商名不产出条目，且结果按 Code 升序：后台下拉的顺序要稳定。
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" || item.Code == "" {
			t.Fatalf("目录条目缺少厂商名或标识：%#v", item)
		}
		if item.Capabilities == nil || item.Protocols == nil {
			t.Fatalf("目录条目的能力/协议列表不应为 nil：%#v", item)
		}
		if seen := map[string]bool{}; true {
			for _, capability := range item.Capabilities {
				if seen[capability] {
					t.Fatalf("能力未去重：%#v", item.Capabilities)
				}
				seen[capability] = true
			}
		}
	}
	for index := 1; index < len(items); index++ {
		if items[index-1].Code >= items[index].Code {
			t.Fatalf("目录未按 Code 升序：%s >= %s", items[index-1].Code, items[index].Code)
		}
	}
}

// TestVendorSaveValidationConflictAndKind 覆盖标识/名称校验、唯一性与内置判定。
func TestVendorSaveValidationConflictAndKind(t *testing.T) {
	svc, _ := newVendorTestService(t)

	invalidCases := []struct {
		name  string
		input VendorInput
	}{
		{"大写标识被拒", VendorInput{Code: "OpenAI", Name: "示例"}},
		{"下划线标识被拒", VendorInput{Code: "my_vendor", Name: "示例"}},
		{"单字符标识被拒", VendorInput{Code: "a", Name: "示例"}},
		{"空标识被拒", VendorInput{Code: "  ", Name: "示例"}},
		{"空名称被拒", VendorInput{Code: "valid-vendor", Name: "  "}},
	}
	for _, testCase := range invalidCases {
		if _, err := svc.SaveModelVendor(testCase.input, ""); vendorTestStatus(err) != 400 {
			t.Fatalf("%s：期望 400，实际 %v", testCase.name, err)
		}
	}

	builtin, err := svc.SaveModelVendor(VendorInput{Code: "openai", Name: "OpenAI"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if builtin.ID == "" || builtin.Kind != model.ModelVendorKindBuiltin {
		t.Fatalf("内置厂商应命中目录并标为 BUILTIN：%#v", builtin)
	}
	if len(builtin.Capabilities) != 1 || builtin.Capabilities[0] != "TEXT" || len(builtin.Protocols) == 0 {
		t.Fatalf("内置厂商应带出目录能力与协议：%#v", builtin)
	}
	if !builtin.Enabled {
		t.Fatal("新建厂商缺省应为启用")
	}

	custom, err := svc.SaveModelVendor(VendorInput{Code: "my-relay", Name: "自建中转"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if custom.Kind != model.ModelVendorKindCustom || len(custom.Capabilities) != 0 || len(custom.Protocols) != 0 {
		t.Fatalf("自定义厂商不应带出目录能力：%#v", custom)
	}

	// 重复标识必须 409，且提示可读。
	if _, err := svc.SaveModelVendor(VendorInput{Code: "openai", Name: "重复"}, ""); vendorTestStatus(err) != 409 {
		t.Fatalf("重复标识应返回 409，实际 %v", err)
	}

	// 自身编辑不误判冲突；改成别人占用的标识要 409。
	if _, err := svc.SaveModelVendor(VendorInput{Code: "openai", Name: "OpenAI 主链路"}, builtin.ID); err != nil {
		t.Fatalf("自身编辑不应冲突：%v", err)
	}
	if _, err := svc.SaveModelVendor(VendorInput{Code: "my-relay", Name: "改冲突"}, builtin.ID); vendorTestStatus(err) != 409 {
		t.Fatalf("改成已占用标识应返回 409，实际 %v", err)
	}

	if _, err := svc.SaveModelVendor(VendorInput{Code: "openai", Name: "不存在的 ID"}, "vendor-missing"); vendorTestStatus(err) != 404 {
		t.Fatalf("编辑不存在的厂商应返回 404，实际 %v", err)
	}
}

// TestVendorDeleteRequiresEmptyCredentials 覆盖“先清凭据再删厂商”的约束。
func TestVendorDeleteRequiresEmptyCredentials(t *testing.T) {
	svc, _ := newVendorTestService(t)
	allowLoopbackUpstream(t)
	vendor := mustSaveVendor(t, svc, VendorInput{Code: "volcengine-ark", Name: "火山方舟"})
	credential := mustSaveCredential(t, svc, vendor.ID, CredentialInput{
		Name:    "主账号",
		BaseURL: "http://127.0.0.1:18080/v1",
		APIKey:  "ark-key-0001",
	})

	if err := svc.DeleteModelVendor(vendor.ID); vendorTestStatus(err) != 409 {
		t.Fatalf("有凭据的厂商应拒绝删除，实际 %v", err)
	}
	if err := svc.DeleteVendorCredential(vendorTestActor(), vendor.ID, credential.ID); err != nil {
		t.Fatalf("删除凭据失败：%v", err)
	}
	if err := svc.DeleteModelVendor(vendor.ID); err != nil {
		t.Fatalf("清空凭据后应可删除厂商：%v", err)
	}
	if err := svc.DeleteModelVendor(vendor.ID); vendorTestStatus(err) != 404 {
		t.Fatalf("重复删除应返回 404，实际 %v", err)
	}
}

// TestVendorCredentialCreatesSystemChannel 覆盖凭据创建会真的落出一条 system 渠道。
func TestVendorCredentialCreatesSystemChannel(t *testing.T) {
	svc, db := newVendorTestService(t)
	allowLoopbackUpstream(t)
	vendor := mustSaveVendor(t, svc, VendorInput{Code: "openai", Name: "OpenAI"})
	weight := 300
	credential, err := svc.SaveVendorCredential(vendorTestActor(), vendor.ID, CredentialInput{
		Name:      "主账号",
		BaseURL:   "http://127.0.0.1:18080/v1",
		APIKey:    "sk-abcdef123456",
		SecretKey: "secret-value",
		Enabled:   boolPointer(true),
		Weight:    &weight,
		Models:    []string{"gpt-4o"},
	}, "")
	if err != nil {
		t.Fatalf("新建凭据失败：%v", err)
	}
	if credential.ChannelID == "" || !strings.HasPrefix(credential.ID, "CRED_") {
		t.Fatalf("凭据缺少渠道指针或主键：%#v", credential)
	}
	if credential.Weight != 300 || credential.KeyHint != "3456" {
		t.Fatalf("凭据权重或密钥尾号不符：%#v", credential)
	}
	if !credential.HasAPIKey || !credential.HasSecretKey {
		t.Fatalf("凭据应报告密钥已配置：%#v", credential)
	}
	if credential.ModelCount != 1 {
		t.Fatalf("凭据模型数应为 1，实际 %d", credential.ModelCount)
	}

	var channel model.ModelChannel
	if err := db.First(&channel, "id = ?", credential.ChannelID).Error; err != nil {
		t.Fatalf("凭据没有建出对应渠道：%v", err)
	}
	if channel.Scope != model.ChannelScopeSystem {
		t.Fatalf("凭据渠道必须是 system 作用域：%#v", channel.Scope)
	}
	if channel.Name != "OpenAI · 主账号" {
		t.Fatalf("渠道名应为「厂商名 · 凭据名」：%q", channel.Name)
	}
	if channel.APIKey == "sk-abcdef123456" || !strings.HasPrefix(channel.APIKey, "enc:v1:") {
		t.Fatalf("渠道密钥必须加密存储：%q", channel.APIKey)
	}

	// 解密读回，确认密钥没有被二次编码或截断。
	plain, err := svc.SystemChannel(credential.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if plain.APIKey != "sk-abcdef123456" || plain.SecretKey != "secret-value" {
		t.Fatalf("渠道密钥读回不一致：%#v", plain)
	}

	// 列表投影：凭据数与模型数都要出现在厂商视图里。
	vendors, err := svc.AdminModelVendors()
	if err != nil {
		t.Fatal(err)
	}
	if len(vendors) != 1 || vendors[0].CredentialCount != 1 || vendors[0].ModelCount != 1 {
		t.Fatalf("厂商读数不符：%#v", vendors)
	}
}

// TestVendorCredentialUpdateKeepsSecretWhenBlank 覆盖“留空表示不改密钥”。
func TestVendorCredentialUpdateKeepsSecretWhenBlank(t *testing.T) {
	svc, _ := newVendorTestService(t)
	allowLoopbackUpstream(t)
	vendor := mustSaveVendor(t, svc, VendorInput{Code: "anthropic", Name: "Anthropic"})
	credential := mustSaveCredential(t, svc, vendor.ID, CredentialInput{
		Name:    "主账号",
		BaseURL: "http://127.0.0.1:18080/v1",
		APIKey:  "sk-ant-000999",
	})
	if credential.KeyHint != "0999" {
		t.Fatalf("初始密钥尾号不符：%q", credential.KeyHint)
	}

	updated, err := svc.SaveVendorCredential(vendorTestActor(), vendor.ID, CredentialInput{
		Name:    "主账号 v2",
		BaseURL: "http://127.0.0.1:18081/v1",
	}, credential.ID)
	if err != nil {
		t.Fatalf("编辑凭据失败：%v", err)
	}
	if updated.KeyHint != credential.KeyHint {
		t.Fatalf("留空 API Key 不应改尾号：%q", updated.KeyHint)
	}
	if updated.Name != "主账号 v2" || updated.BaseURL != "http://127.0.0.1:18081/v1" {
		t.Fatalf("编辑未落到凭据行：%#v", updated)
	}
	plain, err := svc.SystemChannel(credential.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if plain.APIKey != "sk-ant-000999" {
		t.Fatalf("留空 API Key 不应改密钥：%q", plain.APIKey)
	}
	if plain.Name != "Anthropic · 主账号 v2" {
		t.Fatalf("渠道名未跟随凭据名：%q", plain.Name)
	}

	// 显式传新 Key 时才更新尾号。
	rotated, err := svc.SaveVendorCredential(vendorTestActor(), vendor.ID, CredentialInput{
		Name:    "主账号 v2",
		BaseURL: "http://127.0.0.1:18081/v1",
		APIKey:  "sk-ant-777888",
	}, credential.ID)
	if err != nil {
		t.Fatalf("轮换密钥失败：%v", err)
	}
	if rotated.KeyHint != "7888" {
		t.Fatalf("轮换后尾号应为 7888，实际 %q", rotated.KeyHint)
	}
}

// TestVendorCredentialValidation 覆盖权重越界、必填项与凭据属主校验。
func TestVendorCredentialValidation(t *testing.T) {
	svc, _ := newVendorTestService(t)
	allowLoopbackUpstream(t)
	vendor := mustSaveVendor(t, svc, VendorInput{Code: "openai", Name: "OpenAI"})
	other := mustSaveVendor(t, svc, VendorInput{Code: "google", Name: "Google"})

	// 新建必须带 API Key；缺 Key 的校验要挡在渠道创建之前。
	if _, err := svc.SaveVendorCredential(vendorTestActor(), vendor.ID, CredentialInput{
		Name:    "无密钥",
		BaseURL: "http://127.0.0.1:18080/v1",
	}, ""); vendorTestStatus(err) != 400 {
		t.Fatalf("缺少 API Key 应返回 400，实际 %v", err)
	}

	for _, weight := range []int{0, -3, 1001} {
		value := weight
		if _, err := svc.SaveVendorCredential(vendorTestActor(), vendor.ID, CredentialInput{
			Name:    "越界权重",
			BaseURL: "http://127.0.0.1:18080/v1",
			APIKey:  "sk-weight-test",
			Weight:  &value,
		}, ""); vendorTestStatus(err) != 400 {
			t.Fatalf("权重 %d 应返回 400，实际 %v", weight, err)
		}
	}

	credential := mustSaveCredential(t, svc, vendor.ID, CredentialInput{
		Name:    "主账号",
		BaseURL: "http://127.0.0.1:18080/v1",
		APIKey:  "sk-owner-test",
	})

	// 跨厂商引用凭据必须 404，而不是误操作到别人的接入点。
	if _, err := svc.VendorCredentialModels(vendorTestActor(), other.ID, credential.ID); vendorTestStatus(err) != 404 {
		t.Fatalf("跨厂商读模型应返回 404，实际 %v", err)
	}
	if err := svc.DeleteVendorCredential(vendorTestActor(), other.ID, credential.ID); vendorTestStatus(err) != 404 {
		t.Fatalf("跨厂商删凭据应返回 404，实际 %v", err)
	}
	if _, err := svc.SaveVendorCredential(vendorTestActor(), other.ID, CredentialInput{
		Name:    "越权编辑",
		BaseURL: "http://127.0.0.1:18080/v1",
		APIKey:  "sk-cross-vendor",
	}, credential.ID); vendorTestStatus(err) != 404 {
		t.Fatalf("跨厂商编辑凭据应返回 404，实际 %v", err)
	}
	// 探测同样先校验归属，必须在发起上游请求之前就失败。
	if _, err := svc.ProbeVendorCredentialModels(context.Background(), vendorTestActor(), other.ID, credential.ID); vendorTestStatus(err) != 404 {
		t.Fatalf("跨厂商探测应返回 404，实际 %v", err)
	}

	if _, err := svc.SaveVendorCredential(vendorTestActor(), vendor.ID, CredentialInput{
		Name:    "不存在的凭据",
		BaseURL: "http://127.0.0.1:18080/v1",
		APIKey:  "sk-missing",
	}, "cred-missing"); vendorTestStatus(err) != 404 {
		t.Fatalf("编辑不存在的凭据应返回 404，实际 %v", err)
	}
}

// mustSaveVendor 建一条厂商，失败即终止用例。
func mustSaveVendor(t *testing.T, svc *Service, input VendorInput) *VendorView {
	t.Helper()
	vendor, err := svc.SaveModelVendor(input, "")
	if err != nil {
		t.Fatalf("新建厂商失败：%v", err)
	}
	return vendor
}

// mustSaveCredential 建一条凭据；调用方需先通过 allowLoopbackUpstream 放行回环上游。
func mustSaveCredential(t *testing.T, svc *Service, vendorID string, input CredentialInput) *CredentialView {
	t.Helper()
	credential, err := svc.SaveVendorCredential(vendorTestActor(), vendorID, input, "")
	if err != nil {
		t.Fatalf("新建凭据失败：%v", err)
	}
	return credential
}

// allowLoopbackUpstream 把回环地址加入可信私网上游白名单。
//
// CreateSystemChannel 会校验出站域名可达性，用例里的地址固定指向 127.0.0.1；
// 只放行这一个字面量主机，既能走到真实建渠道路径，又不依赖 DNS 与网络。
// TestAddVendorCredentialModelsVerifiesUpstream 覆盖"目录翻不到时按标识登记"这条路径：
// 上游确认存在的标识才写库，且新行停用、不带协议，等运营在模型列表里逐条确认。
func TestAddVendorCredentialModelsVerifiesUpstream(t *testing.T) {
	allowLoopbackUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models/openai/whisper-large-v3" {
			_, _ = w.Write([]byte(`{"owner":"openai","name":"whisper-large-v3"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"not found"}`))
	}))
	defer upstream.Close()

	svc, db := newVendorTestService(t)
	vendor := mustSaveVendor(t, svc, VendorInput{Code: "replicate", Name: "Replicate"})
	credential := mustSaveCredential(t, svc, vendor.ID, CredentialInput{
		Name: "主账号", BaseURL: upstream.URL, APIKey: "r8-token", APIFormat: "openai",
	})

	result, err := svc.AddVendorCredentialModels(context.Background(), vendorTestActor(), vendor.ID, credential.ID, []string{"openai/whisper-large-v3"})
	if err != nil {
		t.Fatalf("手动登记失败：%v", err)
	}
	if result.Added != 1 {
		t.Fatalf("新增条数应为 1，实际 %d", result.Added)
	}
	var registered model.ChannelModel
	if err := db.First(&registered, "channel_id = ? AND model_key = ?", credential.ChannelID, "openai/whisper-large-v3").Error; err != nil {
		t.Fatalf("模型未落库：%v", err)
	}
	if registered.Enabled || registered.Protocol != "" {
		t.Fatalf("登记行应为停用且无协议：%#v", registered)
	}

	// 上游 404 的标识必须被拒绝，且不能留下任何行：拼错的 ID 进路由表只会在用户
	// 发起任务时才报错，那时已经收不到清晰的原因了。
	_, err = svc.AddVendorCredentialModels(context.Background(), vendorTestActor(), vendor.ID, credential.ID, []string{"openai/typo-model"})
	if err == nil || !strings.Contains(err.Error(), "上游没有模型") {
		t.Fatalf("上游不存在的标识应被拒绝，实际 %v", err)
	}
	var count int64
	if err := db.Model(&model.ChannelModel{}).Where("channel_id = ?", credential.ChannelID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("被拒绝的标识不得写库，当前模型数 %d", count)
	}
}

// TestAddVendorCredentialModelsRejectsMalformedIdentity 覆盖标识形态校验：带空格或
// 查询串的输入会改变请求路径的含义，必须在发请求之前就挡住。
func TestAddVendorCredentialModelsRejectsMalformedIdentity(t *testing.T) {
	allowLoopbackUpstream(t)
	svc, _ := newVendorTestService(t)
	vendor := mustSaveVendor(t, svc, VendorInput{Code: "replicate", Name: "Replicate"})
	credential := mustSaveCredential(t, svc, vendor.ID, CredentialInput{
		Name: "主账号", BaseURL: "http://127.0.0.1:18080", APIKey: "r8-token", APIFormat: "openai",
	})
	for _, bad := range []string{"openai/whisper large-v3", "openai/whisper?limit=1", "../../etc/passwd"} {
		if _, err := svc.AddVendorCredentialModels(context.Background(), vendorTestActor(), vendor.ID, credential.ID, []string{bad}); err == nil {
			t.Fatalf("非法标识 %q 应被拒绝", bad)
		}
	}
}

func allowLoopbackUpstream(t *testing.T) {
	t.Helper()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
}

func vendorCodes(items []VendorCatalogItem) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

func vendorContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// TestVendorCatalogIncludesPluginProviders 覆盖"插件厂商要进目录"。
//
// 官方厂商里有八十多家是声明式插件包，目录只读 protocol.Builtins() 时它们整批
// 消失——后台挑不到 Replicate，代码里其实早就写着它的请求协议。插件包又不带厂商
// 归属（author 全是同一个），所以这里同时钉住按 provider ID 首段归组、以及
// 短标识折进内置长标识这两条规则。
func TestVendorCatalogIncludesPluginProviders(t *testing.T) {
	svc, _ := newVendorTestService(t)
	manifest := []byte(`{
		"apiVersion": "beeftv.plugin/v2",
		"id": "replicate-prediction",
		"name": "Replicate Predictions",
		"version": "1.0.0",
		"author": "BeefTV Contributors",
		"runtime": {"backend": ""},
		"contributes": {"providers": [
			{"id": "replicate-prediction-image", "label": "Replicate Predictions Image", "capabilities": ["image"], "scopes": ["admin.system-channel"], "baseUrl": "https://api.replicate.com", "auth": {"type": "bearer", "field": "apiKey"}, "create": {"method": "POST", "path": "/v1/predictions", "contentType": "application/json"}, "poll": {"method": "GET", "path": "/v1/predictions/{{taskId}}"}},
			{"id": "replicate-prediction-video", "label": "Replicate Predictions Video", "capabilities": ["video"], "scopes": ["admin.system-channel"], "baseUrl": "https://api.replicate.com", "auth": {"type": "bearer", "field": "apiKey"}, "create": {"method": "POST", "path": "/v1/predictions", "contentType": "application/json"}, "poll": {"method": "GET", "path": "/v1/predictions/{{taskId}}"}},
			{"id": "volcengine-jimeng-video", "label": "即梦视频", "capabilities": ["video"], "scopes": ["admin.system-channel"], "baseUrl": "https://visual.volcengineapi.com", "auth": {"type": "bearer", "field": "apiKey"}, "create": {"method": "POST", "path": "/", "contentType": "application/json"}},
			{"id": "claude-api", "label": "Anthropic Messages", "capabilities": ["text"], "scopes": ["admin.system-channel"], "baseUrl": "https://api.anthropic.com", "auth": {"type": "bearer", "field": "apiKey"}, "create": {"method": "POST", "path": "/v1/messages", "contentType": "application/json"}},
			{"id": "gemini-image", "label": "Gemini Image", "capabilities": ["image"], "scopes": ["admin.system-channel"], "baseUrl": "https://generativelanguage.googleapis.com", "auth": {"type": "bearer", "field": "apiKey"}, "create": {"method": "POST", "path": "/v1beta/models/{{model}}:generateContent", "contentType": "application/json"}}
		]}
	}`)
	if _, err := svc.pluginRuntime.install(testPluginPackage(t, manifest), "replicate-prediction.beeftv-plugin"); err != nil {
		t.Fatalf("安装测试插件失败：%v", err)
	}
	if err := svc.pluginRuntime.reload(); err != nil {
		t.Fatalf("重载插件注册表失败：%v", err)
	}

	items, err := svc.VendorCatalog()
	if err != nil {
		t.Fatal(err)
	}
	byCode := make(map[string]VendorCatalogItem, len(items))
	for _, item := range items {
		byCode[item.Code] = item
	}

	replicate, ok := byCode["replicate"]
	if !ok {
		t.Fatalf("插件厂商未进目录：%v", vendorCodes(items))
	}
	if replicate.Name != "Replicate" {
		t.Fatalf("厂商展示名应取 label 首段：%q", replicate.Name)
	}
	if !vendorContainsString(replicate.Capabilities, "IMAGE") || !vendorContainsString(replicate.Capabilities, "VIDEO") {
		t.Fatalf("同一厂商下的能力应合并：%#v", replicate.Capabilities)
	}
	if !vendorContainsString(replicate.Protocols, "replicate-prediction-image") || !vendorContainsString(replicate.Protocols, "replicate-prediction-video") {
		t.Fatalf("同一厂商下的协议应合并：%#v", replicate.Protocols)
	}

	// 声明式插件重新实现内置协议时，厂商要跟内置走：claude-api 属于 Anthropic，
	// 按 ID 首段猜会凭空多出一个 claude 假厂商。
	if !vendorContainsString(byCode["anthropic"].Protocols, "claude-api") {
		t.Fatalf("重新声明内置协议的插件应并进内置厂商：%#v", byCode["anthropic"])
	}
	if _, exists := byCode["claude"]; exists {
		t.Fatalf("不应出现按 ID 首段猜出来的假厂商 claude")
	}

	// 模型家族名要归到厂商：gemini-* 属于 Google，否则后台会同时出现 google 与 gemini。
	if !vendorContainsString(byCode["google"].Protocols, "gemini-image") {
		t.Fatalf("gemini 系列应并进 google：%#v", byCode["google"])
	}
	if _, exists := byCode["gemini"]; exists {
		t.Fatalf("不应出现按模型家族名猜出来的厂商 gemini")
	}

	// 插件的 volcengine-jimeng-video 要并进内置的 volcengine-jimeng，而不是新起一行。
	if !vendorContainsString(byCode["volcengine-jimeng"].Protocols, "volcengine-jimeng-video") {
		t.Fatalf("短标识未折进内置厂商：%#v", byCode["volcengine-jimeng"])
	}
	for code := range byCode {
		if code == "volcengine" {
			t.Fatalf("volcengine 应折进 volcengine-ark/jimeng，不应单独成行")
		}
	}

	// 插件厂商按内置处理：运营在后台看到它属于"平台预置"。
	view, err := svc.SaveModelVendor(VendorInput{Code: "replicate", Name: "Replicate"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Kind != "BUILTIN" {
		t.Fatalf("插件来源的厂商应判定为 BUILTIN：%q", view.Kind)
	}
	if !vendorContainsString(view.Capabilities, "IMAGE") {
		t.Fatalf("厂商应带出目录里的能力清单：%#v", view.Capabilities)
	}
}
