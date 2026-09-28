package hosted

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/bootstrap"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 工单与反馈的 HTTP 用例。
//
// 走真实注册链路建号 + 真实路由：登录校验、管理员守卫、响应信封字段名与审计落点
// 都只有在这一层才会暴露。

var hostedTicketNoPattern = regexp.MustCompile(`^T\d{6}[0-9A-F]{6}$`)

type hostedTicket struct {
	ID        string  `json:"id"`
	TicketNo  string  `json:"ticketNo"`
	UserID    string  `json:"userId"`
	UserName  string  `json:"userName"`
	UserEmail string  `json:"userEmail"`
	Title     string  `json:"title"`
	Category  string  `json:"category"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	ClosedAt  *string `json:"closedAt"`
	Replies   []struct {
		ID         string `json:"id"`
		AuthorRole string `json:"authorRole"`
		AuthorName string `json:"authorName"`
		Body       string `json:"body"`
	} `json:"replies"`
}

// newTicketRouter 复用标准托管路由（工单路由已由 RegisterRoutes/registerAdminRoutes 挂载）。
func newTicketRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	if err := auth.EnsureSupportSchema(authDB); err != nil {
		t.Fatalf("初始化工单表失败: %v", err)
	}
	router := newTestRouter(extension, service)
	return extension, router, authDB, canvasDB
}

func decodeTicket(t *testing.T, recorder *httptest.ResponseRecorder) hostedTicket {
	t.Helper()
	var payload struct {
		Data struct {
			Ticket hostedTicket `json:"ticket"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析工单响应失败: %v %s", err, recorder.Body.String())
	}
	return payload.Data.Ticket
}

func createHostedTicket(t *testing.T, router *gin.Engine, cookie *http.Cookie, title string) hostedTicket {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"category": "BUG",
		"title":    title,
		"body":     "详细描述正文内容",
		"contact":  "13800000000",
	})
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	recorder := perform(router, http.MethodPost, "/api/support/tickets", string(body), cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("创建工单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	ticket := decodeTicket(t, recorder)
	if ticket.ID == "" {
		t.Fatalf("创建工单响应缺少 id：%s", recorder.Body.String())
	}
	return ticket
}

// TestHostedSupportTicketFlow 覆盖用户端提交 → 列表 → 详情 → 回复，以及未登录 401。
func TestHostedSupportTicketFlow(t *testing.T) {
	extension, router, authDB, _ := newTicketRouter(t)
	defer extension.Close()

	// 未登录访问用户端工单接口必须被拦下。
	if recorder := perform(router, http.MethodGet, "/api/support/tickets", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取工单应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/support/tickets", `{"category":"BUG","title":"x","body":"y"}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录提交工单应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	cookie, userID := registerAccount(t, router, authDB, "support-user@example.com")

	// 非法分类在 HTTP 层就要变成 400。
	if recorder := perform(router, http.MethodPost, "/api/support/tickets",
		`{"category":"URGENT","title":"登录不了","body":"验证码收不到"}`, cookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法分类应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	ticket := createHostedTicket(t, router, cookie, "登录不了")
	if !hostedTicketNoPattern.MatchString(ticket.TicketNo) {
		t.Fatalf("工单号格式不符合契约：%q", ticket.TicketNo)
	}
	if ticket.Status != "OPEN" || ticket.UserID != userID {
		t.Fatalf("新建工单状态/归属错误：%+v", ticket)
	}
	if ticket.UserName == "" || ticket.UserEmail != "support-user@example.com" {
		t.Fatalf("工单视图缺少账号标识：%+v", ticket)
	}
	if len(ticket.Replies) != 0 {
		t.Fatalf("新建工单不应有回复：%+v", ticket.Replies)
	}

	recorder := perform(router, http.MethodGet, "/api/support/tickets", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取我的工单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Data struct {
			Tickets []hostedTicket `json:"tickets"`
			Total   int            `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("解析工单列表失败: %v %s", err, recorder.Body.String())
	}
	if page.Data.Total != 1 || len(page.Data.Tickets) != 1 || page.Data.Tickets[0].ID != ticket.ID {
		t.Fatalf("工单列表结果错误：%s", recorder.Body.String())
	}

	recorder = perform(router, http.MethodGet, "/api/support/tickets/"+ticket.ID, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取工单详情失败：%d %s", recorder.Code, recorder.Body.String())
	}

	recorder = perform(router, http.MethodPost, "/api/support/tickets/"+ticket.ID+"/replies",
		`{"body":"补充：换了浏览器也不行"}`, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("用户回复失败：%d %s", recorder.Code, recorder.Body.String())
	}
	replied := decodeTicket(t, recorder)
	if len(replied.Replies) != 1 || replied.Replies[0].AuthorRole != "USER" {
		t.Fatalf("用户回复作者角色错误：%+v", replied.Replies)
	}
}

// TestHostedSupportTicketIsolation 确认 A 账号读/回 B 的工单与"不存在"同文案。
func TestHostedSupportTicketIsolation(t *testing.T) {
	extension, router, authDB, _ := newTicketRouter(t)
	defer extension.Close()

	cookieA, _ := registerAccount(t, router, authDB, "ticket-owner@example.com")
	cookieB, _ := registerAccount(t, router, authDB, "ticket-other@example.com")
	ticket := createHostedTicket(t, router, cookieA, "只有本人能看")

	foreign := perform(router, http.MethodGet, "/api/support/tickets/"+ticket.ID, "", cookieB)
	missing := perform(router, http.MethodGet, "/api/support/tickets/not-a-real-ticket", "", cookieB)
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("越权读取应返回 403，实际 %d：%s", foreign.Code, foreign.Body.String())
	}
	if foreign.Body.String() != missing.Body.String() {
		t.Fatalf("越权与不存在的响应必须一致，避免工单号被枚举：\n%s\n%s", foreign.Body.String(), missing.Body.String())
	}

	reply := perform(router, http.MethodPost, "/api/support/tickets/"+ticket.ID+"/replies", `{"body":"越权回复"}`, cookieB)
	if reply.Code != http.StatusForbidden {
		t.Fatalf("越权回复应返回 403，实际 %d：%s", reply.Code, reply.Body.String())
	}
}

// TestHostedAdminTicketWorkflow 覆盖后台列表、客服回复推进状态、关闭与审计留痕。
func TestHostedAdminTicketWorkflow(t *testing.T) {
	extension, router, authDB, canvasDB := newTicketRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "ticket-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	userCookie, _ := registerAccount(t, router, authDB, "ticket-customer@example.com")
	ticket := createHostedTicket(t, router, userCookie, "订单没有生效")

	// 未登录 / 非管理员都不能进后台工单。
	if recorder := perform(router, http.MethodGet, "/api/admin/tickets", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问后台工单应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/admin/tickets", "", userCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("非管理员访问后台工单应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	recorder := perform(router, http.MethodGet, "/api/admin/tickets", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("后台工单列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var listPayload struct {
		Data struct {
			Tickets []hostedTicket `json:"tickets"`
			Total   int            `json:"total"`
			Counts  struct {
				Total int `json:"total"`
				Open  int `json:"open"`
			} `json:"counts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("解析后台工单列表失败: %v %s", err, recorder.Body.String())
	}
	if listPayload.Data.Total != 1 || listPayload.Data.Counts.Total != 1 || listPayload.Data.Counts.Open != 1 {
		t.Fatalf("后台工单读数错误：%s", recorder.Body.String())
	}

	// 客服首次回复：OPEN 自动推进到 PROCESSING。
	recorder = perform(router, http.MethodPost, "/api/admin/tickets/"+ticket.ID+"/replies", `{"body":"已收到，正在补单"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("客服回复失败：%d %s", recorder.Code, recorder.Body.String())
	}
	replied := decodeTicket(t, recorder)
	if replied.Status != "PROCESSING" {
		t.Fatalf("客服回复后应转为 PROCESSING，实际 %q", replied.Status)
	}
	if len(replied.Replies) != 1 || replied.Replies[0].AuthorRole != "STAFF" {
		t.Fatalf("客服回复作者角色错误：%+v", replied.Replies)
	}

	// 非法状态必须是 400。
	recorder = perform(router, http.MethodPatch, "/api/admin/tickets/"+ticket.ID+"/status", `{"status":"URGENT"}`, adminCookie)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法状态应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 关闭工单要写入 closed_at。
	recorder = perform(router, http.MethodPatch, "/api/admin/tickets/"+ticket.ID+"/status", `{"status":"CLOSED"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("关闭工单失败：%d %s", recorder.Code, recorder.Body.String())
	}
	closed := decodeTicket(t, recorder)
	if closed.Status != "CLOSED" || closed.ClosedAt == nil || *closed.ClosedAt == "" {
		t.Fatalf("关闭工单必须写入 closedAt：%s", recorder.Body.String())
	}

	// 已关闭的工单用户不能再回复。
	recorder = perform(router, http.MethodPost, "/api/support/tickets/"+ticket.ID+"/replies", `{"body":"还想补充"}`, userCookie)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("已关闭工单回复应返回 409，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 两个写操作都要留痕。
	for _, action := range []string{"ticket.reply", "ticket.status"} {
		var count int64
		if err := canvasDB.Table("admin_audit_events").Where("action = ?", action).Count(&count).Error; err != nil {
			t.Fatalf("读取审计失败: %v", err)
		}
		if count != 1 {
			t.Fatalf("动作 %s 应留下 1 条审计，实际 %d 条", action, count)
		}
	}
}

// TestHostedAdminTicketFilters 覆盖后台按状态与关键字（工单号 / 标题）筛选。
func TestHostedAdminTicketFilters(t *testing.T) {
	extension, router, authDB, _ := newTicketRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "ticket-filter-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	userCookie, _ := registerAccount(t, router, authDB, "ticket-filter-user@example.com")

	paidTicket := createHostedTicket(t, router, userCookie, "支付没有生效")
	bugTicket := createHostedTicket(t, router, userCookie, "页面卡顿打不开")
	if recorder := perform(router, http.MethodPatch, "/api/admin/tickets/"+paidTicket.ID+"/status", `{"status":"RESOLVED"}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("更新状态失败：%d %s", recorder.Code, recorder.Body.String())
	}

	assertTotal := func(query string, want int) {
		t.Helper()
		recorder := perform(router, http.MethodGet, "/api/admin/tickets"+query, "", adminCookie)
		if recorder.Code != http.StatusOK {
			t.Fatalf("后台筛选失败：%d %s", recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Data struct {
				Tickets []hostedTicket `json:"tickets"`
				Total   int            `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("解析筛选结果失败: %v %s", err, recorder.Body.String())
		}
		if payload.Data.Total != want || len(payload.Data.Tickets) != want {
			t.Fatalf("筛选 %q 期望 %d 条，实际 total=%d len=%d：%s", query, want, payload.Data.Total, len(payload.Data.Tickets), recorder.Body.String())
		}
	}

	assertTotal("?status=RESOLVED", 1)
	assertTotal("?keyword="+paidTicket.TicketNo, 1)
	assertTotal("?keyword=完全不存在的关键字", 0)
	assertTotal("?keyword=卡顿", 1)
	assertTotal("?keyword="+bugTicket.TicketNo, 1)
}
