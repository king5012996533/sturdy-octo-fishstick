package hosted

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 积分接口的用例。重点盯三件事：
//   - 余额与流水只能读自己的（主体恒为会话账号，查询串里的 userId 不是凭据）；
//   - 充值要真的贯通"后台建积分包 → 用户下单 → 支付成功 → 积分进钱包 → 用户看到余额"；
//   - 余额不足必须带机器可读的 reason，否则前端只能靠匹配中文文案决定要不要弹充值。

func TestHostedCreditRequiresSession(t *testing.T) {
	extension, router, _, _ := newAdminRouter(t)
	defer extension.Close()

	for _, path := range []string{"/api/finance/wallet", "/api/finance/ledger"} {
		if recorder := perform(router, http.MethodGet, path, "", nil); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("未登录访问 %s 应返回 401，实际 %d：%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

// TestHostedCreditTopUpReachesWallet 覆盖完整的充值链路。
//
// 这条链路横跨"后台货架 → 用户下单 → 支付成功 → 积分到账 → 用户看到余额"，
// 任何一环的字段名或单位对不上，用户付了钱余额还是 0，而这只能在用户投诉时才发现。
func TestHostedCreditTopUpReachesWallet(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "credit-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	userCookie, _ := registerAccount(t, router, authDB, "credit-user@example.com")

	// 纯积分包：周期 0、只到账积分。
	if recorder := perform(router, http.MethodPost, "/api/admin/plans",
		`{"code":"topup-100","name":"100 元积分包","priceFen":10000,"periodDays":0,"credits":10000,"giftCredits":1000,"enabled":true}`,
		adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("建积分包失败：%d %s", recorder.Code, recorder.Body.String())
	}

	wallet := readWallet(t, router, userCookie, "")
	// 没充过钱的账号就是零，不是 404：把零余额做成错误会让新用户的积分卡片显示成加载失败。
	if wallet.Balance != 0 || wallet.UserID == "" {
		t.Fatalf("新账号应返回零余额账户，实际 %#v", wallet)
	}

	recorder := perform(router, http.MethodPost, "/api/payments/orders", `{"planCode":"topup-100"}`, userCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("下单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Data struct {
			Order struct {
				ID          string `json:"id"`
				Credits     int64  `json:"credits"`
				GiftCredits int64  `json:"giftCredits"`
			} `json:"order"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析下单响应失败: %v %s", err, recorder.Body.String())
	}
	if created.Data.Order.Credits != 10000 || created.Data.Order.GiftCredits != 1000 {
		t.Fatalf("订单应快照积分 10000/1000，实际 %#v", created.Data.Order)
	}

	if recorder := perform(router, http.MethodPost, "/api/admin/orders/"+created.Data.Order.ID+"/mark-paid",
		`{"remark":"线下转账"}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("补单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if wallet := readWallet(t, router, userCookie, ""); wallet.Balance != 11000 {
		t.Fatalf("到账后余额应为 11000，实际 %d", wallet.Balance)
	}

	// 按种类筛选只回本金那一条：本金与赠送分成两条流水，账单里才能分别统计。
	recorder = perform(router, http.MethodGet, "/api/finance/ledger?kind=TOPUP", "", userCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读流水失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var ledger struct {
		Data struct {
			Entries []auth.CreditLedgerEntryView `json:"entries"`
			Total   int64                        `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &ledger); err != nil {
		t.Fatalf("解析流水响应失败: %v %s", err, recorder.Body.String())
	}
	if ledger.Data.Total != 1 || len(ledger.Data.Entries) != 1 || ledger.Data.Entries[0].Amount != 10000 {
		t.Fatalf("按 TOPUP 筛选应只回本金那一条，实际 %#v", ledger.Data)
	}
}

// TestHostedCreditLedgerIgnoresForeignUserID 确认查询串里的 userId 不是凭据。
//
// 一旦能被指定，查流水就变成了"遍历别人的账单"，而余额可以换算出这个人充过多少钱。
func TestHostedCreditLedgerIgnoresForeignUserID(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	_, victimID := registerAccount(t, router, authDB, "credit-victim@example.com")
	envyCookie, _ := registerAccount(t, router, authDB, "credit-envy@example.com")

	if wallet := readWallet(t, router, envyCookie, ""); wallet.UserID == victimID {
		t.Fatalf("余额主体应恒为会话账号，实际返回了 %s", victimID)
	}

	recorder := perform(router, http.MethodGet, "/api/finance/ledger?userId="+victimID, "", envyCookie)
	var ledger struct {
		Data struct {
			Entries []auth.CreditLedgerEntryView `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &ledger); err != nil {
		t.Fatalf("解析流水响应失败: %v %s", err, recorder.Body.String())
	}
	if len(ledger.Data.Entries) != 0 {
		t.Fatalf("查询串里的 userId 不应生效，实际返回了 %d 条", len(ledger.Data.Entries))
	}
}

// TestHostedAdminCreditAdjustRequiresNote 盯住手工调整必须能解释。
//
// 调整是这一组里唯一会改余额的接口，而这条流水就是唯一的记录；没有原因的加减分
// 在被追问时没有任何依据。
func TestHostedAdminCreditAdjustRequiresNote(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "adjust-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, targetID := registerAccount(t, router, authDB, "adjust-target@example.com")

	recorder := perform(router, http.MethodPost, "/api/admin/credits/adjust",
		`{"userId":"`+targetID+`","amount":500}`, adminCookie)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("缺少原因应被拒，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	recorder = perform(router, http.MethodPost, "/api/admin/credits/adjust",
		`{"userId":"`+targetID+`","amount":500,"note":"客服补偿"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("带原因的补偿应成功，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if wallet := readWalletFor(t, router, adminCookie, targetID); wallet.Balance != 500 {
		t.Fatalf("补偿后余额应为 500，实际 %d", wallet.Balance)
	}
}

// TestHostedAdminCreditRejectsOverdraft 确认后台也扣不出负余额。
//
// 后台能把余额扣成负数，等于平台替用户垫钱；余额校验放在 UPDATE 的条件里，
// 这条路径与用户侧扣费是同一段代码。
func TestHostedAdminCreditRejectsOverdraft(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "overdraft-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, targetID := registerAccount(t, router, authDB, "overdraft-target@example.com")

	recorder := perform(router, http.MethodPost, "/api/admin/credits/adjust",
		`{"userId":"`+targetID+`","amount":-500,"note":"扣错的分"}`, adminCookie)
	if recorder.Code != http.StatusPaymentRequired {
		t.Fatalf("扣成负数应返回 402，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if reason := readReason(t, recorder.Body.Bytes()); reason != "insufficient_credits" {
		t.Fatalf("余额不足应带 reason=insufficient_credits，实际 %q", reason)
	}
}

// TestHostedAdminCreditAccountsSortsByBalance 覆盖后台积分列表：余额降序 + 带上账号资料。
func TestHostedAdminCreditAccountsSortsByBalance(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "rank-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, smallID := registerAccount(t, router, authDB, "rank-small@example.com")
	_, bigID := registerAccount(t, router, authDB, "rank-big@example.com")

	for _, item := range []struct {
		userID string
		amount int64
	}{{smallID, 100}, {bigID, 90000}} {
		body := `{"userId":"` + item.userID + `","amount":` + strconv.FormatInt(item.amount, 10) + `,"note":"测试充值"}`
		if recorder := perform(router, http.MethodPost, "/api/admin/credits/adjust", body, adminCookie); recorder.Code != http.StatusOK {
			t.Fatalf("加分失败：%d %s", recorder.Code, recorder.Body.String())
		}
	}

	recorder := perform(router, http.MethodGet, "/api/admin/credits/accounts", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读积分列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Data struct {
			Accounts []auth.CreditAccountRowView `json:"accounts"`
			Total    int64                       `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析积分列表失败: %v %s", err, recorder.Body.String())
	}
	if page.Data.Total != 2 || len(page.Data.Accounts) != 2 {
		t.Fatalf("应有两个积分账户，实际 %#v", page.Data)
	}
	if page.Data.Accounts[0].UserID != bigID || page.Data.Accounts[0].Balance != 90000 {
		t.Fatalf("应按余额降序，实际首行 %#v", page.Data.Accounts[0])
	}
	if page.Data.Accounts[0].Email != "rank-big@example.com" {
		t.Fatalf("应带上账号资料，实际 %#v", page.Data.Accounts[0])
	}

	// 关键字筛选走的是账号列表的同一段 where：两边口径不一致时，后台账号列表能搜到的人
	// 在积分列表里搜不到，运维会以为这是两套账号体系。
	recorder = perform(router, http.MethodGet, "/api/admin/credits/accounts?keyword=rank-small", "", adminCookie)
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析筛选结果失败: %v %s", err, recorder.Body.String())
	}
	if page.Data.Total != 1 || page.Data.Accounts[0].UserID != smallID {
		t.Fatalf("关键字应只命中 rank-small，实际 %#v", page.Data)
	}
}

// TestHostedAdminCreditLedgerRequiresUser 确认后台看流水必须点名账号。
//
// "全部流水"在一张几十万条的表上既慢又没有意义：运营永远是从某个人进来的。
func TestHostedAdminCreditLedgerRequiresUser(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "ledger-admin@example.com")
	promoteToAdmin(t, authDB, adminID)

	if recorder := perform(router, http.MethodGet, "/api/admin/credits/ledger", "", adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("不带账号应被拒，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// TestRespondServiceErrorKeepsReason 确认结构化错误的原因码不会被 HTTP 层丢掉。
//
// 前端 request() 已经会读信封里的 reason，后端不写它就等于这个字段永远不存在，
// 前端只能退化成匹配中文文案——改一次文案就会静默失效。
func TestRespondServiceErrorKeepsReason(t *testing.T) {
	extension, _, _, _ := newTestExtension(t)
	defer extension.Close()

	router := gin.New()
	router.GET("/probe", func(c *gin.Context) {
		// 用真实的余额不足路径产生错误，而不是手搓一个 auth.Error：手搓的用例只能
		// 证明"这个分支会写 reason"，证明不了扣费真的会走到它。
		_, _, err := extension.(*Extension).service.ChargeTaskCredits("no-such-user", "task-1", 100, "测试")
		respondServiceError(c, err)
	})

	recorder := perform(router, http.MethodGet, "/probe", "", nil)
	if recorder.Code != http.StatusPaymentRequired {
		t.Fatalf("余额不足应返回 402，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if reason := readReason(t, recorder.Body.Bytes()); reason != "insufficient_credits" {
		t.Fatalf("reason 应为 insufficient_credits，实际 %q", reason)
	}
}

// readWallet 读某个会话自己的钱包。
func readWallet(t *testing.T, router *gin.Engine, cookie *http.Cookie, query string) auth.CreditWalletView {
	t.Helper()
	recorder := perform(router, http.MethodGet, "/api/finance/wallet"+query, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读余额失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Wallet auth.CreditWalletView `json:"wallet"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("解析余额响应失败: %v %s", err, recorder.Body.String())
	}
	return envelope.Data.Wallet
}

// readWalletFor 用管理端接口读指定账号的钱包，用于断言调整后的余额。
func readWalletFor(t *testing.T, router *gin.Engine, adminCookie *http.Cookie, userID string) auth.CreditWalletView {
	t.Helper()
	return readWalletByAdmin(t, router, adminCookie, userID)
}

// readWalletByAdmin 通过管理端积分列表读取单个账号的余额读数。
func readWalletByAdmin(t *testing.T, router *gin.Engine, adminCookie *http.Cookie, userID string) auth.CreditWalletView {
	t.Helper()
	recorder := perform(router, http.MethodGet, "/api/admin/credits/accounts", "", adminCookie)
	var page struct {
		Data struct {
			Accounts []auth.CreditAccountRowView `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析积分列表失败: %v %s", err, recorder.Body.String())
	}
	for _, account := range page.Data.Accounts {
		if account.UserID == userID {
			return auth.CreditWalletView{UserID: account.UserID, Balance: account.Balance, LifetimeIn: account.LifetimeIn, LifetimeOut: account.LifetimeOut}
		}
	}
	t.Fatalf("积分列表里没有账号 %s", userID)
	return auth.CreditWalletView{}
}

// readReason 从失败信封里取机器可读的原因码。
func readReason(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("解析失败信封失败: %v %s", err, string(body))
	}
	return envelope.Reason
}
