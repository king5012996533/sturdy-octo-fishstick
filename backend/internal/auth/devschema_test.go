package auth

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GitHub 登录的启用条件来自环境变量，而种子逻辑只在缺行时插入。若不在每次启动
// 重算，先跑过一次的库里补上凭据也不会生效，user 会以为代码没写完。这个用例把
// 「补凭据 → 生效」「撤凭据 → 置灰」两个方向都钉住。
func TestSeedDevMethodConfigsRefreshesGithubCredentials(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}

	t.Setenv("BEEFTV_GITHUB_CLIENT_ID", "")
	t.Setenv("BEEFTV_GITHUB_CLIENT_SECRET", "")
	t.Setenv("BEEFTV_GITHUB_REDIRECT_URI", "")
	if err := EnsureDevSchema(db); err != nil {
		t.Fatalf("初始化测试表失败: %v", err)
	}
	assertGithubMethodState(t, db, false, "")

	t.Setenv("BEEFTV_GITHUB_CLIENT_ID", "test-client-id")
	t.Setenv("BEEFTV_GITHUB_CLIENT_SECRET", "test-client-secret")
	t.Setenv("BEEFTV_GITHUB_REDIRECT_URI", "http://localhost:3000/auth/oauth/callback")
	if err := EnsureDevSchema(db); err != nil {
		t.Fatalf("带凭据重新初始化失败: %v", err)
	}
	assertGithubMethodState(t, db, true, "test-client-id")

	t.Setenv("BEEFTV_GITHUB_CLIENT_ID", "")
	t.Setenv("BEEFTV_GITHUB_CLIENT_SECRET", "")
	if err := EnsureDevSchema(db); err != nil {
		t.Fatalf("撤掉凭据重新初始化失败: %v", err)
	}
	assertGithubMethodState(t, db, false, "")
}

func assertGithubMethodState(t *testing.T, db *gorm.DB, enabled bool, clientID string) {
	t.Helper()
	var record MethodConfig
	if err := db.Where("method_type = ?", string(MethodGithubOAuth)).First(&record).Error; err != nil {
		t.Fatalf("读取 GitHub 登录配置失败: %v", err)
	}
	if record.IsEnabled != enabled {
		t.Fatalf("GitHub 登录启用状态 = %v，期望 %v", record.IsEnabled, enabled)
	}
	config, err := ParseOAuthConfig(record.ConfigJSON)
	if err != nil {
		t.Fatalf("解析 GitHub 登录配置失败: %v", err)
	}
	if config.ClientID != clientID {
		t.Fatalf("GitHub clientId = %q，期望 %q", config.ClientID, clientID)
	}
}
