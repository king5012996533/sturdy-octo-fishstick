package hosted

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 用户中心接口的用例。
//
// 规则本身（越权、冻结账号、验证码窗口、改密后保留当前设备）在 auth 域的用例里已经逐条
// 覆盖，这一层只盯三件 HTTP 契约上的事：
//   - 十条路由都挂在 /api 下、都要登录；
//   - 每组返回的字段名与前端 services/api/account-*.ts 一致——字段名漂移不会让任何一侧
//     编译失败，只会让某一页安静地空着；
//   - 请求体里的用户标识不参与鉴权：主体只来自会话。

func accountRouteCases() []struct{ method, path, body string } {
	return []struct{ method, path, body string }{
		{http.MethodGet, "/api/finance/account", ""},
		{http.MethodGet, "/api/finance/account/profile", ""},
		{http.MethodPatch, "/api/finance/account/profile", `{"name":"n","avatarUrl":""}`},
		{http.MethodGet, "/api/finance/account/password", ""},
		{http.MethodPost, "/api/finance/account/password", `{"methodType":"EMAIL_CODE","code":"000000","newPassword":"12345678"}`},
		{http.MethodPut, "/api/finance/account/password", `{"currentPassword":"x","newPassword":"12345678"}`},
		{http.MethodGet, "/api/finance/account/sessions", ""},
		{http.MethodDelete, "/api/finance/account/sessions/some-id", ""},
		{http.MethodPost, "/api/finance/account/sessions/revoke-others", ""},
		{http.MethodGet, "/api/finance/account/bindings", ""},
		{http.MethodPost, "/api/finance/account/bindings/code", `{"channel":"EMAIL","target":"new@example.com"}`},
		{http.MethodPost, "/api/finance/account/bindings", `{"channel":"EMAIL","target":"new@example.com","code":"000000"}`},
	}
}

// TestHostedAccountRoutesRequireSession 确认这一组没有漏挂鉴权：任何一条在未登录时
// 都必须 401，而不是带着空 userId 去查库、再把"查不到"当成"没有"。
func TestHostedAccountRoutesRequireSession(t *testing.T) {
	extension, router, _, _ := newAdminRouter(t)
	defer extension.Close()

	for _, tc := range accountRouteCases() {
		recorder := perform(router, tc.method, tc.path, tc.body, nil)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("未登录 %s %s 应返回 401，实际 %d：%s", tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
	}
}

// TestHostedAccountProfileRoundTrip 覆盖昵称与头像的读写，以及"主体只来自会话"。
func TestHostedAccountProfileRoundTrip(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, _ := registerAccount(t, router, authDB, "profile@example.com")
	otherCookie, otherID := registerAccount(t, router, authDB, "profile-other@example.com")

	var profile struct {
		Data struct {
			UserID      string `json:"userId"`
			Name        string `json:"name"`
			AvatarURL   string `json:"avatarUrl"`
			Email       string `json:"email"`
			HasPassword bool   `json:"hasPassword"`
		} `json:"data"`
	}
	recorder := perform(router, http.MethodGet, "/api/finance/account/profile", "", cookie)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &profile) != nil {
		t.Fatalf("读取资料失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if profile.Data.UserID == "" || profile.Data.Email != "profile@example.com" || profile.Data.HasPassword {
		t.Fatalf("资料初值不符合预期：%#v", profile.Data)
	}

	recorder = perform(router, http.MethodPatch, "/api/finance/account/profile",
		`{"name":"  光启云科  ","avatarUrl":"https://cdn.example.com/a.png"}`, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("更新资料失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 昵称两端空白由服务端裁掉，前端拿到的就是落库后的值，不必自己再 trim 一次。
	if json.Unmarshal(recorder.Body.Bytes(), &profile) != nil || profile.Data.Name != "光启云科" {
		t.Fatalf("昵称未按预期落库：%s", recorder.Body.String())
	}

	// 头像最终会进 <img src>：非 http(s) 协议必须被拒，而不是原样存下来。
	if recorder = perform(router, http.MethodPatch, "/api/finance/account/profile",
		`{"name":"光启云科","avatarUrl":"javascript:alert(1)"}`, cookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非 http(s) 头像应被拒，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	// 空昵称会让界面回落到邮箱，等于把账号名暴露给所有人。
	if recorder = perform(router, http.MethodPatch, "/api/finance/account/profile",
		`{"name":"   ","avatarUrl":""}`, cookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("空昵称应被拒，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 请求体里塞别人的 userId 不构成任何凭据：改的仍然只能是自己的账号。
	perform(router, http.MethodPatch, "/api/finance/account/profile",
		`{"userId":"`+otherID+`","name":"被冒充","avatarUrl":""}`, otherCookie)
	recorder = perform(router, http.MethodGet, "/api/finance/account/profile", "", cookie)
	if json.Unmarshal(recorder.Body.Bytes(), &profile) != nil || profile.Data.Name != "光启云科" {
		t.Fatalf("另一个账号的请求体不应改到本账号：%s", recorder.Body.String())
	}
}

// TestHostedAccountPasswordSetThenChange 覆盖「验证码设置 → 旧密码修改」两条分支的契约。
func TestHostedAccountPasswordSetThenChange(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, _ := registerAccount(t, router, authDB, "password@example.com")

	var state struct {
		Data struct {
			HasPassword bool `json:"hasPassword"`
			MinLength   int  `json:"minLength"`
			MaxLength   int  `json:"maxLength"`
		} `json:"data"`
	}
	recorder := perform(router, http.MethodGet, "/api/finance/account/password", "", cookie)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &state) != nil {
		t.Fatalf("读取密码状态失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 上下限必须由服务端下发：表单写死之后，改策略会先拦住用户再被服务端拒一次。
	if state.Data.HasPassword || state.Data.MinLength < 8 || state.Data.MaxLength < state.Data.MinLength {
		t.Fatalf("密码状态不符合预期：%#v", state.Data)
	}

	code := issueAccountCode(t, router, authDB, "password@example.com")
	recorder = perform(router, http.MethodPost, "/api/finance/account/password",
		`{"methodType":"EMAIL_CODE","code":"`+code+`","newPassword":"kinotv-2026"}`, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("设置密码失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 写接口回的是 {state, revokedSessions}：前端据此写"已从 N 台设备退出登录"。
	var update struct {
		Data struct {
			State           struct{ HasPassword bool `json:"hasPassword"` } `json:"state"`
			RevokedSessions int64                                            `json:"revokedSessions"`
		} `json:"data"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &update) != nil || !update.Data.State.HasPassword {
		t.Fatalf("设置后状态应为已有密码：%s", recorder.Body.String())
	}

	// 已经设置过的账号再走设置分支是冲突，不是"再设一次"：两条入口的证明方式不同，
	// 允许覆盖等于让一个会话把密码改成攻击者知道的值。
	recorder = perform(router, http.MethodPost, "/api/finance/account/password",
		`{"methodType":"EMAIL_CODE","code":"000000","newPassword":"kinotv-2027"}`, cookie)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("重复设置密码应返回 409，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	if recorder = perform(router, http.MethodPut, "/api/finance/account/password",
		`{"currentPassword":"not-my-password","newPassword":"kinotv-2027"}`, cookie); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("旧密码错误应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodPut, "/api/finance/account/password",
		`{"currentPassword":"kinotv-2026","newPassword":"kinotv-2027"}`, cookie); recorder.Code != http.StatusOK {
		t.Fatalf("修改密码失败：%d %s", recorder.Code, recorder.Body.String())
	}
}

// TestHostedAccountSessionsListAndRevoke 覆盖设备列表与远程下线。
func TestHostedAccountSessionsListAndRevoke(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, _ := registerAccount(t, router, authDB, "sessions@example.com")
	second := loginWithCode(t, router, authDB, "sessions@example.com")

	type sessionView struct {
		ID      string `json:"id"`
		Current bool   `json:"current"`
		Device  string `json:"device"`
	}
	type sessionList struct {
		Data struct {
			Sessions []sessionView `json:"sessions"`
			Revoked  int64         `json:"revoked"`
		} `json:"data"`
	}
	var list sessionList
	readSessions := func(cookie *http.Cookie) sessionList {
		t.Helper()
		recorder := perform(router, http.MethodGet, "/api/finance/account/sessions", "", cookie)
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &list) != nil {
			t.Fatalf("读取设备列表失败：%d %s", recorder.Code, recorder.Body.String())
		}
		return list
	}

	list = readSessions(cookie)
	if len(list.Data.Sessions) != 2 {
		t.Fatalf("两次登录应有两条会话，实际 %#v", list.Data.Sessions)
	}
	currentCount := 0
	var other string
	for _, session := range list.Data.Sessions {
		if session.Current {
			currentCount++
			continue
		}
		other = session.ID
	}
	// "当前设备"必须有且只有一条：标错之后，用户第一个动作就是把唯一有效的会话踢掉。
	if currentCount != 1 || other == "" {
		t.Fatalf("当前设备标记不符合预期：%#v", list.Data.Sessions)
	}

	recorder := perform(router, http.MethodDelete, "/api/finance/account/sessions/"+other, "", cookie)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &list) != nil || len(list.Data.Sessions) != 1 {
		t.Fatalf("下线设备失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 已经被踢掉的那条会话不能再访问：列表接口本身也要 401。
	if recorder = perform(router, http.MethodGet, "/api/finance/account/sessions", "", second); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("被下线的会话应失效，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	recorder = perform(router, http.MethodPost, "/api/finance/account/sessions/revoke-others", "", cookie)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &list) != nil {
		t.Fatalf("下线其他设备失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 没有其他设备时是 0 而不是报错：这个动作本来就会在只剩一台时被点到。
	if list.Data.Revoked != 0 || len(list.Data.Sessions) != 1 {
		t.Fatalf("只剩当前设备时结果不符合预期：%#v", list.Data)
	}
}

// TestHostedAccountBindingMovesAddress 覆盖「先给新地址发码 → 带码确认」的换绑契约。
func TestHostedAccountBindingMovesAddress(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, _ := registerAccount(t, router, authDB, "old@example.com")

	type bindingsView struct {
		Data struct {
			Email    string `json:"email"`
			Phone    string `json:"phone"`
			Bindings []struct {
				MethodType string `json:"methodType"`
				Identifier string `json:"identifier"`
				Primary    bool   `json:"primary"`
			} `json:"bindings"`
		} `json:"data"`
	}
	var view bindingsView
	recorder := perform(router, http.MethodGet, "/api/finance/account/bindings", "", cookie)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &view) != nil || view.Data.Email != "old@example.com" {
		t.Fatalf("读取绑定失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 码只能发给即将生效的那个地址：这里的目标是 new@example.com，拿到的码也只对它有效。
	recorder = perform(router, http.MethodPost, "/api/finance/account/bindings/code",
		`{"channel":"EMAIL","target":"new@example.com"}`, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("下发换绑验证码失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var challenge struct {
		Data struct {
			Target   string `json:"target"`
			Cooldown int    `json:"cooldown"`
		} `json:"data"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &challenge) != nil || challenge.Data.Target != "new@example.com" || challenge.Data.Cooldown <= 0 {
		t.Fatalf("换绑验证码响应不符合预期：%s", recorder.Body.String())
	}
	code := latestCode(t, authDB, "new@example.com")

	// 旧地址的码不能顶用：换绑认的是新地址那条记录。
	if recorder = perform(router, http.MethodPost, "/api/finance/account/bindings",
		`{"channel":"EMAIL","target":"new@example.com","code":"000000"}`, cookie); recorder.Code == http.StatusOK {
		t.Fatalf("错误验证码不应换绑成功：%s", recorder.Body.String())
	}
	recorder = perform(router, http.MethodPost, "/api/finance/account/bindings",
		`{"channel":"EMAIL","target":"new@example.com","code":"`+code+`"}`, cookie)
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &view) != nil || view.Data.Email != "new@example.com" {
		t.Fatalf("换绑失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if len(view.Data.Bindings) != 1 || view.Data.Bindings[0].Identifier != "new@example.com" || !view.Data.Bindings[0].Primary {
		t.Fatalf("换绑后应只剩一条新的主绑定：%#v", view.Data.Bindings)
	}
}

// issueAccountCode 走真实下发接口取一个可用验证码。
//
// 下发接口对同一目标有 60 秒冷却，而注册刚刚用过同一个地址；把已有记录的签发时间往前
// 推一分钟等价于"等了一会儿再来一次"，比直接往库里插一条码更贴近真实链路。
func issueAccountCode(t *testing.T, router *gin.Engine, authDB *gorm.DB, target string) string {
	t.Helper()
	backdateCodes(t, authDB, target)
	if recorder := perform(router, http.MethodPost, "/api/auth/verification-code",
		`{"methodType":"EMAIL_CODE","target":"`+target+`"}`, nil); recorder.Code != http.StatusOK {
		t.Fatalf("下发验证码失败：%d %s", recorder.Code, recorder.Body.String())
	}
	return latestCode(t, authDB, target)
}

// loginWithCode 用验证码再登一次，造出第二台设备的会话。
func loginWithCode(t *testing.T, router *gin.Engine, authDB *gorm.DB, target string) *http.Cookie {
	t.Helper()
	code := issueAccountCode(t, router, authDB, target)
	recorder := perform(router, http.MethodPost, "/api/auth/login",
		`{"methodType":"EMAIL_CODE","target":"`+target+`","code":"`+code+`"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("二次登录失败：%d %s", recorder.Code, recorder.Body.String())
	}
	return sessionCookie(t, recorder)
}

func backdateCodes(t *testing.T, authDB *gorm.DB, target string) {
	t.Helper()
	if err := authDB.Table("auth_verification_codes").Where("target = ?", target).
		Update("created_at", time.Now().Add(-2*time.Minute)).Error; err != nil {
		t.Fatalf("回拨验证码签发时间失败: %v", err)
	}
}

func latestCode(t *testing.T, authDB *gorm.DB, target string) string {
	t.Helper()
	var record struct {
		Code string `gorm:"column:code"`
	}
	if err := authDB.Table("auth_verification_codes").
		Where("target = ? AND used_at IS NULL", target).
		Order("created_at desc").First(&record).Error; err != nil {
		t.Fatalf("账号库里没有可用验证码: %v", err)
	}
	return record.Code
}
