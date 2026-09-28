package hosted

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/bootstrap"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// registerAccount 走真实注册链路建号并返回会话 Cookie。
//
// 管理端用例必须建在真实账号上：直接在账号库里插一行会绕过协议留痕与密码哈希，
// 让"重置密码/强制下线"这些用例失去意义。
func registerAccount(t *testing.T, router *gin.Engine, authDB *gorm.DB, target string) (*http.Cookie, string) {
	t.Helper()
	var agreements struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	recorder := perform(router, http.MethodGet, "/api/auth/agreements", "", nil)
	if err := json.Unmarshal(recorder.Body.Bytes(), &agreements); err != nil {
		t.Fatalf("解析协议响应失败: %v %s", err, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodPost, "/api/auth/verification-code",
		`{"methodType":"EMAIL_CODE","target":"`+target+`"}`, nil); recorder.Code != http.StatusOK {
		t.Fatalf("下发验证码失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var record struct {
		Code string `gorm:"column:code"`
	}
	if err := authDB.Table("auth_verification_codes").
		Where("target = ? AND used_at IS NULL", target).
		Order("created_at desc").First(&record).Error; err != nil {
		t.Fatalf("账号库里没有验证码记录: %v", err)
	}
	recorder = perform(router, http.MethodPost, "/api/auth/register",
		`{"methodType":"EMAIL_CODE","target":"`+target+`","code":"`+record.Code+`","agreementVersion":"`+agreements.Data.Version+`"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("注册失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var login struct {
		Data struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &login); err != nil || login.Data.User.ID == "" {
		t.Fatalf("解析注册响应失败: %v %s", err, recorder.Body.String())
	}
	return sessionCookie(t, recorder), login.Data.User.ID
}

func promoteToAdmin(t *testing.T, authDB *gorm.DB, userID string) {
	t.Helper()
	if err := authDB.Table("app_users").Where("id = ?", userID).Update("role", "ADMIN").Error; err != nil {
		t.Fatalf("提升管理员失败: %v", err)
	}
}

func newAdminRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	return extension, newTestRouter(extension, service), authDB, canvasDB
}

// TestHostedAdminRequiresAdminRole 确认账号侧管理端不是"登录即可访问"。
func TestHostedAdminRequiresAdminRole(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	if recorder := perform(router, http.MethodGet, "/api/admin/users", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问管理端应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	cookie, _ := registerAccount(t, router, authDB, "plain@example.com")
	if recorder := perform(router, http.MethodGet, "/api/admin/users", "", cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号访问管理端应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// TestHostedAdminUserLifecycle 覆盖「列表 → 封禁并踢下线 → 审计留痕」。
func TestHostedAdminUserLifecycle(t *testing.T) {
	extension, router, authDB, canvasDB := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	victimCookie, victimID := registerAccount(t, router, authDB, "victim@example.com")

	recorder := perform(router, http.MethodGet, "/api/admin/users", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("用户列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Data struct {
			Users []struct {
				ID       string `json:"id"`
				Role     string `json:"role"`
				Status   string `json:"status"`
				Password string `json:"password"`
			} `json:"users"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析用户列表失败: %v %s", err, recorder.Body.String())
	}
	if page.Data.Total < 2 {
		t.Fatalf("用户总数应至少 2，实际 %d：%s", page.Data.Total, recorder.Body.String())
	}
	found := false
	for _, user := range page.Data.Users {
		if user.ID == victimID {
			found = true
		}
	}
	if !found {
		t.Fatalf("用户列表缺少刚注册的账号：%s", recorder.Body.String())
	}
	// 读模型不得带出任何凭据字段。
	if recorder := perform(router, http.MethodGet, "/api/admin/users", "", adminCookie); recorder.Code == http.StatusOK && containsAnySecret(recorder.Body.String()) {
		t.Fatalf("用户列表泄漏凭据字段：%s", recorder.Body.String())
	}

	// 仪表盘读数来自两套库：账号计数与画布计数都要在。
	recorder = perform(router, http.MethodGet, "/api/admin/analytics/overview?days=7", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("仪表盘失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var overview struct {
		Data struct {
			Users struct {
				Total int64 `json:"total"`
			} `json:"users"`
			Canvases int64 `json:"canvases"`
			Trend    []struct {
				Day string `json:"day"`
			} `json:"trend"`
			Days int `json:"days"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatalf("解析仪表盘失败: %v %s", err, recorder.Body.String())
	}
	if overview.Data.Users.Total < 2 || overview.Data.Days != 7 || len(overview.Data.Trend) != 7 {
		t.Fatalf("仪表盘读数不完整：%s", recorder.Body.String())
	}

	// 管理员不能封自己：否则一个误操作就能把整个后台锁死。
	if recorder := perform(router, http.MethodPatch, "/api/admin/users/"+adminID+"/status", `{"status":"DISABLED"}`, adminCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("管理员封自己应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	recorder = perform(router, http.MethodPatch, "/api/admin/users/"+victimID+"/status", `{"status":"DISABLED"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("封禁失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 封禁必须同时踢下线：只改状态会让被封的浏览器继续握着一枚"看起来有效"的 Cookie。
	// /api/auth/session 是"我是谁"的探测接口，未登录时以 200 + user:null 作答。
	if recorder := perform(router, http.MethodGet, "/api/auth/session", "", victimCookie); !strings.Contains(recorder.Body.String(), `"user":null`) {
		t.Fatalf("被封账号的会话仍然可用：%s", recorder.Body.String())
	}

	var auditCount int64
	if err := canvasDB.Table("admin_audit_events").Where("action = ?", "user.status").Count(&auditCount).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("封禁操作应留下 1 条审计，实际 %d 条", auditCount)
	}

	// 解封 + 改角色 + 重置密码 + 强制下线：每一步都要能用且留痕。
	if recorder := perform(router, http.MethodPatch, "/api/admin/users/"+victimID+"/status", `{"status":"ACTIVE"}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("解封失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPatch, "/api/admin/users/"+victimID+"/role", `{"role":"ADMIN"}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("改角色失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/users/"+victimID+"/password", `{"password":"kino12345"}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("重置密码失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/users/"+victimID+"/logout", `{}`, adminCookie); recorder.Code != http.StatusOK || !containsAll(recorder.Body.String(), "revokedSessions") {
		t.Fatalf("强制下线失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 审计日志本身要能读回，且不包含重置密码的明文。
	recorder = perform(router, http.MethodGet, "/api/admin/audit-events", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("审计列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if containsAll(recorder.Body.String(), "kino12345") {
		t.Fatalf("审计日志泄漏密码明文：%s", recorder.Body.String())
	}
}

// TestHostedAdminLoginMethodsKeepOneChannel 确认运营不能把登录通道全部关掉。
func TestHostedAdminLoginMethodsKeepOneChannel(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin2@example.com")
	promoteToAdmin(t, authDB, adminID)

	recorder := perform(router, http.MethodGet, "/api/admin/login-methods", "", adminCookie)
	if recorder.Code != http.StatusOK || !containsAll(recorder.Body.String(), "EMAIL_CODE") {
		t.Fatalf("登录方式列表异常：%d %s", recorder.Code, recorder.Body.String())
	}

	var methods struct {
		Data struct {
			Methods []struct {
				MethodType string `json:"methodType"`
				IsEnabled  bool   `json:"isEnabled"`
			} `json:"methods"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &methods); err != nil {
		t.Fatalf("解析登录方式失败: %v %s", err, recorder.Body.String())
	}
	enabled := make([]string, 0, len(methods.Data.Methods))
	for _, method := range methods.Data.Methods {
		if method.IsEnabled {
			enabled = append(enabled, method.MethodType)
		}
	}
	if len(enabled) < 2 {
		t.Fatalf("开发库应至少启用两种登录方式，实际 %v", enabled)
	}
	// 关到只剩一种为止：每一步都应成功。
	for _, method := range enabled[:len(enabled)-1] {
		if recorder := perform(router, http.MethodPatch, "/api/admin/login-methods/"+method, `{"isEnabled":false}`, adminCookie); recorder.Code != http.StatusOK {
			t.Fatalf("关闭 %s 失败：%d %s", method, recorder.Code, recorder.Body.String())
		}
	}
	// 关最后一种必须被拒：全关等于把所有人挡在门外。
	last := enabled[len(enabled)-1]
	if recorder := perform(router, http.MethodPatch, "/api/admin/login-methods/"+last, `{"isEnabled":false}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("关闭最后一种登录方式应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}

// containsAnySecret 检查用户读模型里是否混进了凭据字段名。
func containsAnySecret(body string) bool {
	for _, field := range []string{"passwordHash", "password_hash", "tokenHash"} {
		if strings.Contains(body, field) {
			return true
		}
	}
	return false
}

// TestHostedAccountOverview 覆盖用户自己的账户读数：身份、用量与协议留痕。
func TestHostedAccountOverview(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, _ := registerAccount(t, router, authDB, "self@example.com")
	recorder := perform(router, http.MethodGet, "/api/finance/account", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("账户读数失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Account struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"account"`
			Usage struct {
				Canvases int64 `json:"canvases"`
				Days     int   `json:"days"`
			} `json:"usage"`
			Agreements []struct {
				AgreementType string `json:"agreementType"`
				Version       string `json:"version"`
			} `json:"agreements"`
			Billing struct {
				Mode    string `json:"mode"`
				Balance *int64 `json:"balance"`
			} `json:"billing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析账户读数失败: %v %s", err, recorder.Body.String())
	}
	if payload.Data.Account.Email != "self@example.com" || payload.Data.Usage.Days != 30 {
		t.Fatalf("账户读数不完整：%s", recorder.Body.String())
	}
	// 协议留痕必须回放到用户面前：注册时点了同意，账户页却查不到，等于没有举证能力。
	if len(payload.Data.Agreements) != 2 {
		t.Fatalf("协议留痕应为 2 条，实际 %d 条：%s", len(payload.Data.Agreements), recorder.Body.String())
	}
	// 未接入充值时余额必须是 null：0 会被读成"用户真的一分钱没有"。
	if payload.Data.Billing.Mode != "platform" || payload.Data.Billing.Balance != nil {
		t.Fatalf("计费段不应给出虚假余额：%s", recorder.Body.String())
	}

	if recorder := perform(router, http.MethodGet, "/api/finance/account", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取账户应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
