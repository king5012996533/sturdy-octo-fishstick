package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 托管实例必须按 hosted 模式构造服务：本地守卫会拒绝系统渠道，前端选中平台模型后
// 每一次生成都会失败在"本地工作区不支持系统渠道配置"。
func TestHostedServiceAcceptsSystemChannelConfig(t *testing.T) {
	svc, db := newModelCatalogTestService(t)
	seedSystemChannel(t, db)

	resolved, err := svc.resolveProviderConfig(providerConfig{ChannelID: "platform-test", Model: "demo-image-1"})
	if err != nil {
		t.Fatalf("托管服务应当接受系统渠道配置，实际失败：%v", err)
	}
	if resolved.APIKey != "sk-platform" {
		t.Fatalf("系统渠道密钥必须由服务端补齐，实际 = %q", resolved.APIKey)
	}
	if resolved.BaseURL != "https://example.com/v1" {
		t.Fatalf("系统渠道 Base URL 必须取平台配置，实际 = %q", resolved.BaseURL)
	}
	if resolved.InterfaceType != string(model.ChannelInterfaceOpenAIImage) {
		t.Fatalf("协议必须来自渠道模型，实际 = %q", resolved.InterfaceType)
	}
}

func TestLocalServiceRejectsSystemChannelConfig(t *testing.T) {
	db := newSystemChannelTestDB(t)
	svc := NewLocal(repository.New(db), t.TempDir())
	seedSystemChannel(t, db)

	if _, err := svc.resolveProviderConfig(providerConfig{ChannelID: "platform-test", Model: "demo-image-1"}); err == nil {
		t.Fatal("本地/桌面服务必须拒绝系统渠道配置，避免把托管渠道 ID 当成可执行配置")
	}
}

// 托管首启写入"平台模式"默认值：自建渠道关闭，用户不能直连上游绕过计费。
func TestEnsureHostedFeatureDefaultsClosesCustomChannelsOnce(t *testing.T) {
	svc, db := newModelCatalogTestService(t)

	seeded, err := svc.EnsureHostedFeatureDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if !seeded {
		t.Fatal("首次启动应当写入托管默认值")
	}
	enabled, err := svc.FeatureEnabled(FeatureCustomChannels)
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("托管默认值必须关闭自建渠道")
	}

	// 幂等：已有配置时不再写入，否则运维每次重启都会被回滚成关闭。
	again, err := svc.EnsureHostedFeatureDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if again {
		t.Fatal("已存在功能开放配置时不得再次写入")
	}

	// 运维显式打开后，启动流程不得把它关回去。
	if err := db.Model(&model.SystemSetting{}).Where("key = ?", featureAvailabilitySettingKey).Update("value_json", `{"customChannelsEnabled":true}`).Error; err != nil {
		t.Fatal(err)
	}
	if reopened, err := svc.EnsureHostedFeatureDefaults(); err != nil || reopened {
		t.Fatalf("运维配置必须优先，seeded=%v err=%v", reopened, err)
	}
	enabled, err = svc.FeatureEnabled(FeatureCustomChannels)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("运维打开的开关被启动流程覆盖")
	}
}

func TestEnsureHostedFeatureDefaultsIsNoopForLocalWorkspace(t *testing.T) {
	db := newSystemChannelTestDB(t)
	svc := NewLocal(repository.New(db), t.TempDir())

	seeded, err := svc.EnsureHostedFeatureDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if seeded {
		t.Fatal("本地/桌面形态不得写入托管默认值")
	}
	enabled, err := svc.FeatureEnabled(FeatureCustomChannels)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("本地形态的自建渠道必须保持开启")
	}
	var count int64
	if err := db.Model(&model.SystemSetting{}).Where("key = ?", featureAvailabilitySettingKey).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("本地形态不该落库功能开放配置，实际 %d 行", count)
	}
}

func newSystemChannelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.AdminAuditEvent{}, &model.ModelChannel{}, &model.ChannelModel{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedSystemChannel(t *testing.T, db *gorm.DB) {
	t.Helper()
	channel := model.ModelChannel{
		ID: "platform-test", UserID: "platform", Scope: model.ChannelScopeSystem, Enabled: true,
		Name: "平台测试渠道", BaseURL: "https://example.com/v1", APIKey: "sk-platform", APIFormat: "openai",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	channelModel := model.ChannelModel{
		ID: "cm-platform-test", ChannelID: channel.ID, ModelKey: "demo-image-1", DisplayName: "演示生图模型",
		Capability: "image", Protocol: model.ChannelInterfaceOpenAIImage, Enabled: true,
		CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceOpenAIImage), "demo-image-1")),
	}
	if err := db.Create(&channelModel).Error; err != nil {
		t.Fatal(err)
	}
}
