package main

import (
	"path/filepath"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/hosted"
)

// 托管开关必须 fail-fast：缺账号库时退回单工作区模式，等于在公网上开一个不需要登录、
// 却能照常调用上游渠道的共享工作区。

func TestHostedFactoryFailsFastWhenAuthDatabaseMissing(t *testing.T) {
	factory, err := hostedFactory(hosted.Options{StateSecret: "super-secret-state"}, true)
	if err == nil {
		t.Fatal("启用托管但缺账号库时必须返回启动错误")
	}
	if factory != nil {
		t.Fatal("启动失败时不应返回工厂")
	}
	if !strings.Contains(err.Error(), "CANVAS_AUTH_DATABASE_URL") {
		t.Fatalf("错误信息要指明缺失的配置项：%v", err)
	}
	if !strings.Contains(err.Error(), "CANVAS_HOSTED_AUTH") {
		t.Fatalf("错误信息要指明是哪个开关打开的：%v", err)
	}
	if strings.Contains(err.Error(), "super-secret-state") {
		t.Fatalf("启动错误不得回显密钥：%v", err)
	}
}

func TestHostedFactoryDisabledKeepsLocalMode(t *testing.T) {
	factory, err := hostedFactory(hosted.Options{}, false)
	if err != nil {
		t.Fatalf("未启用托管时不应报错：%v", err)
	}
	if factory != nil {
		t.Fatal("未启用托管时必须返回 nil 工厂，运行时才会停在单工作区模式")
	}
	// 关闭开关时，即使配了账号库也不能偷偷启用。
	withDatabase, err := hostedFactory(hosted.Options{DatabaseURL: "/tmp/should-not-be-used.db"}, false)
	if err != nil || withDatabase != nil {
		t.Fatalf("关闭开关时不得装配托管能力：factory=%v err=%v", withDatabase, err)
	}
}

func TestHostedFactoryEnabledWithAuthDatabaseStartsHostedAuth(t *testing.T) {
	dir := t.TempDir()
	factory, err := hostedFactory(hosted.Options{
		DatabaseDriver: "sqlite",
		DatabaseURL:    filepath.Join(dir, "auth.db"),
		StateSecret:    "test-state-secret",
	}, true)
	if err != nil {
		t.Fatalf("配置完整时不应报错：%v", err)
	}
	if factory == nil {
		t.Fatal("启用托管时应当返回工厂")
	}
	extension, err := factory(bootstrap.HostedDeps{DataDir: dir})
	if err != nil {
		t.Fatalf("托管能力装配失败：%v", err)
	}
	if extension == nil {
		t.Fatal("托管能力为空")
	}
	if err := extension.Close(); err != nil {
		t.Fatalf("释放托管能力失败：%v", err)
	}
}
