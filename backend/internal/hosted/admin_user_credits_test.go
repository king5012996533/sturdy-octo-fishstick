package hosted

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 用户管理页的积分读数。
//
// 运营在用户管理里要回答的问题是"这个人还有多少积分、一共用掉多少"，所以列表本身
// 就必须带余额——需要跳到另一个页面才能看到余额时，用户管理这一页就只剩账号资料，
// 遇到"用户说充不上钱"这类问题还得再查一遍。
//
// 这里同时盯住补齐方式：整页一次查询，而不是逐行查。逐行查在二十行的页面上看不出
// 差别，但它是那种"上线时才变慢"的写法。

func TestHostedAdminUserListCarriesCreditBalance(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "user-credit-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, richID := registerAccount(t, router, authDB, "user-credit-rich@example.com")
	_, freshID := registerAccount(t, router, authDB, "user-credit-fresh@example.com")

	adjust := `{"userId":"` + richID + `","amount":1200,"note":"测试充值"}`
	if recorder := perform(router, http.MethodPost, "/api/admin/credits/adjust", adjust, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("充值失败：%d %s", recorder.Code, recorder.Body.String())
	}

	rows := readAdminUserRows(t, router, adminCookie)
	rich, ok := rows[richID]
	if !ok {
		t.Fatalf("用户列表里没有充值过的账号，实际 %#v", rows)
	}
	if rich.Credit == nil || rich.Credit.Balance != 1200 || rich.Credit.LifetimeIn != 1200 {
		t.Fatalf("列表应带出余额与累计获得，实际 %#v", rich.Credit)
	}
	// 没充过钱的账号是最常见的一类，必须给出零余额视图而不是缺字段或报错。
	fresh, ok := rows[freshID]
	if !ok {
		t.Fatalf("用户列表里没有新注册的账号，实际 %#v", rows)
	}
	if fresh.Credit == nil || fresh.Credit.Balance != 0 {
		t.Fatalf("未注册积分账户的账号应显示零余额，实际 %#v", fresh.Credit)
	}
}

func TestHostedAdminUserDetailCarriesCreditBalance(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "user-detail-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, targetID := registerAccount(t, router, authDB, "user-detail-target@example.com")

	adjust := `{"userId":"` + targetID + `","amount":300,"note":"测试充值"}`
	if recorder := perform(router, http.MethodPost, "/api/admin/credits/adjust", adjust, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("充值失败：%d %s", recorder.Code, recorder.Body.String())
	}

	recorder := perform(router, http.MethodGet, "/api/admin/users/"+targetID, "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读用户详情失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data adminUserRow `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析用户详情失败: %v %s", err, recorder.Body.String())
	}
	if envelope.Data.Credit == nil || envelope.Data.Credit.Balance != 300 {
		t.Fatalf("详情应带出余额，实际 %#v", envelope.Data.Credit)
	}
}

func TestHostedAdminCreditAccountReadsSingleWallet(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "wallet-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, targetID := registerAccount(t, router, authDB, "wallet-target@example.com")

	adjust := `{"userId":"` + targetID + `","amount":` + strconv.Itoa(640) + `,"note":"测试充值"}`
	if recorder := perform(router, http.MethodPost, "/api/admin/credits/adjust", adjust, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("充值失败：%d %s", recorder.Code, recorder.Body.String())
	}

	recorder := perform(router, http.MethodGet, "/api/admin/credits/accounts/"+targetID, "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读单账号余额失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data auth.CreditWalletView `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析余额失败: %v %s", err, recorder.Body.String())
	}
	if envelope.Data.Balance != 640 || envelope.Data.LifetimeIn != 640 {
		t.Fatalf("单账号余额应为 640，实际 %#v", envelope.Data)
	}

	// 抽屉要对一个从没充过钱的账号也给出零余额，而不是让运营看到一次失败。
	_, emptyID := registerAccount(t, router, authDB, "wallet-empty@example.com")
	recorder = perform(router, http.MethodGet, "/api/admin/credits/accounts/"+emptyID, "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读零余额账号失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析零余额失败: %v %s", err, recorder.Body.String())
	}
	if envelope.Data.Balance != 0 {
		t.Fatalf("未充值账号应为零余额，实际 %d", envelope.Data.Balance)
	}
}

func TestHostedAdminUserCreditRequiresAdmin(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	userCookie, userID := registerAccount(t, router, authDB, "user-credit-plain@example.com")

	for _, path := range []string{"/api/admin/users", "/api/admin/credits/accounts/" + userID} {
		if recorder := perform(router, http.MethodGet, path, "", userCookie); recorder.Code != http.StatusForbidden {
			t.Fatalf("普通账号访问 %s 应 403，实际 %d：%s", path, recorder.Code, recorder.Body.String())
		}
		if recorder := perform(router, http.MethodGet, path, "", nil); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("匿名访问 %s 应 401，实际 %d：%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

// readAdminUserRows 读用户列表并按账号 ID 建索引。
func readAdminUserRows(t *testing.T, router *gin.Engine, adminCookie *http.Cookie) map[string]adminUserRow {
	t.Helper()
	recorder := perform(router, http.MethodGet, "/api/admin/users?pageSize=100", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读用户列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Data struct {
			Users []adminUserRow `json:"users"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析用户列表失败: %v %s", err, recorder.Body.String())
	}
	rows := make(map[string]adminUserRow, len(page.Data.Users))
	for _, row := range page.Data.Users {
		rows[row.ID] = row
	}
	return rows
}
