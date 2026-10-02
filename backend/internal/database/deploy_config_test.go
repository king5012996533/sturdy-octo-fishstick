package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 部署清单里声明的数据库驱动必须真的被编译进二进制：写成 postgres 而后端只有
// sqlite/mysql 时，症状不是某条链路降级，而是服务根本起不来。
const composeDriverOptOutMarker = "x-beeftv-unsupported-database-driver"

// 既认写死的驱动名，也认 ${VAR:-默认值} 形式：默认值才是没人覆盖时真正生效的驱动。
var composeDriverPattern = regexp.MustCompile(
	`(?m)^\s*CANVAS_DATABASE_DRIVER:\s*(?:\$\{[A-Za-z_][A-Za-z0-9_]*:-([A-Za-z0-9_-]+)\}|["']?([A-Za-z0-9_-]+))`)

func TestComposeDatabaseDriversAreSupported(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	files, err := filepath.Glob(filepath.Join(root, "docker-compose*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("没有找到任何 Compose 文件，这条自检会变成空转")
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		content := string(body)
		// 明确标注"这份清单不是本仓库部署路径"的文件豁免，但标注本身要写出驱动名。
		if strings.Contains(content, composeDriverOptOutMarker) {
			continue
		}
		for _, match := range composeDriverPattern.FindAllStringSubmatch(content, -1) {
			driver := match[1]
			if driver == "" {
				driver = match[2]
			}
			if !SupportedDriver(driver) {
				t.Fatalf("%s 声明了后端不支持的驱动 %q；要么改成支持驱动，要么加上 %s 标注说明原因",
					filepath.Base(file), driver, composeDriverOptOutMarker)
			}
		}
	}
}

func TestSupportedDriverMatchesOpenBehaviour(t *testing.T) {
	for _, driver := range []string{"sqlite", "SQLite", " mysql ", ""} {
		if !SupportedDriver(driver) {
			t.Fatalf("%q 应当被判定为支持", driver)
		}
	}
	for _, driver := range []string{"postgres", "postgresql", "oracle"} {
		if SupportedDriver(driver) {
			t.Fatalf("%q 不应被判定为支持", driver)
		}
	}
	// Open 的拒绝路径要与 SupportedDriver 保持一致，否则自检会放行一个起不来的驱动。
	if _, err := Open(Config{Driver: "postgres", DSN: "host=127.0.0.1"}); err == nil {
		t.Fatal("Open 应当拒绝 postgres")
	}
}
