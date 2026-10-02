package main

import (
	"strings"
	"testing"
)

// 运行模式必须显式选择：升级时漏配变量就悄悄改变安全边界，比启动失败危险得多。

func TestEnvRequiredBoolRejectsUnsetSwitch(t *testing.T) {
	t.Setenv("CANVAS_HOSTED_AUTH", "")
	_, err := envRequiredBool("CANVAS_HOSTED_AUTH")
	if err == nil {
		t.Fatal("开关未设置时必须报错，不能回退到某个默认模式")
	}
	if !strings.Contains(err.Error(), "CANVAS_HOSTED_AUTH") || !strings.Contains(err.Error(), "未设置") {
		t.Fatalf("错误信息要指明缺哪个变量：%v", err)
	}
}

func TestEnvRequiredBoolRejectsInvalidValue(t *testing.T) {
	for _, value := range []string{"yes", "truee", "on", "enabled"} {
		t.Setenv("CANVAS_HOSTED_AUTH", value)
		if _, err := envRequiredBool("CANVAS_HOSTED_AUTH"); err == nil {
			t.Fatalf("%q 不是合法布尔值，应当报错", value)
		}
	}
}

// 1/0 与仓库其它开关（如 CANVAS_AUTO_MIGRATE=1）写法一致，按别名接受。
func TestEnvRequiredBoolAcceptsNumericAliases(t *testing.T) {
	t.Setenv("CANVAS_HOSTED_AUTH", "1")
	if enabled, err := envRequiredBool("CANVAS_HOSTED_AUTH"); err != nil || !enabled {
		t.Fatalf("1 应当解析为启用：enabled=%v err=%v", enabled, err)
	}
	t.Setenv("CANVAS_HOSTED_AUTH", "0")
	if enabled, err := envRequiredBool("CANVAS_HOSTED_AUTH"); err != nil || enabled {
		t.Fatalf("0 应当解析为关闭：enabled=%v err=%v", enabled, err)
	}
}

func TestEnvRequiredBoolParsesExplicitChoice(t *testing.T) {
	t.Setenv("CANVAS_HOSTED_AUTH", "true")
	if enabled, err := envRequiredBool("CANVAS_HOSTED_AUTH"); err != nil || !enabled {
		t.Fatalf("true 应当解析为启用：enabled=%v err=%v", enabled, err)
	}
	t.Setenv("CANVAS_HOSTED_AUTH", "false")
	if enabled, err := envRequiredBool("CANVAS_HOSTED_AUTH"); err != nil || enabled {
		t.Fatalf("false 应当解析为关闭：enabled=%v err=%v", enabled, err)
	}
}

func TestResolveListenAddrDefaultsToLoopbackInLocalMode(t *testing.T) {
	t.Setenv("CANVAS_BACKEND_ADDR", "")
	if addr := resolveListenAddr(false); addr != "127.0.0.1:8080" {
		t.Fatalf("单工作区模式默认只应监听回环地址，实际 %q", addr)
	}
	if addr := resolveListenAddr(true); addr != ":8080" {
		t.Fatalf("托管形态保持原默认监听地址，实际 %q", addr)
	}
	t.Setenv("CANVAS_BACKEND_ADDR", "127.0.0.1:8090")
	if addr := resolveListenAddr(false); addr != "127.0.0.1:8090" {
		t.Fatalf("显式配置的监听地址必须生效，实际 %q", addr)
	}
	if addr := resolveListenAddr(true); addr != "127.0.0.1:8090" {
		t.Fatalf("显式配置的监听地址必须生效，实际 %q", addr)
	}
}
