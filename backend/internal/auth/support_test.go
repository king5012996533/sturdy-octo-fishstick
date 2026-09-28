package auth

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// 工单域用例。数据源一律走真实 SQLite：工单号的唯一索引、撞号重试、状态机与
// 关键字 JOIN 都依赖 SQL 语义，用假存储测出来的"通过"证明不了这些行为。

type supportTestEnv struct {
	*billingTestEnv
}

// newSupportTestEnv 复用计费用例的装配（真实 SQLite + 固定时钟），再补上工单表。
func newSupportTestEnv(t *testing.T) *supportTestEnv {
	t.Helper()
	env := newBillingTestService(t)
	if err := EnsureSupportSchema(env.store.db); err != nil {
		t.Fatalf("初始化支持表失败: %v", err)
	}
	return &supportTestEnv{billingTestEnv: env}
}

var supportTicketNoPattern = regexp.MustCompile(`^T\d{6}[0-9A-F]{6}$`)

func seedSupportUser(t *testing.T, env *supportTestEnv, id string, name string, email string) {
	t.Helper()
	nameValue := name
	emailValue := email
	if err := env.store.CreateUser(&User{ID: id, Name: &nameValue, Email: &emailValue}); err != nil {
		t.Fatalf("写入账号失败: %v", err)
	}
}

func createSupportTicket(t *testing.T, env *supportTestEnv, userID string, title string) *SupportTicketView {
	t.Helper()
	view, err := env.service.CreateSupportTicket(userID, SupportTicketInput{
		Category: SupportCategoryBug,
		Title:    title,
		Body:     "详细描述正文内容",
		Contact:  "13800000000",
	})
	if err != nil {
		t.Fatalf("创建工单失败: %v", err)
	}
	return view
}

func assertSupportError(t *testing.T, err error, status int, message string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望报错（%d %s），实际成功", status, message)
	}
	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("期望模块错误，实际 %T: %v", err, err)
	}
	if authErr.Status != status {
		t.Fatalf("期望状态 %d，实际 %d（%s）", status, authErr.Status, authErr.Message)
	}
	if message != "" && authErr.Message != message {
		t.Fatalf("期望文案 %q，实际 %q", message, authErr.Message)
	}
}

// overrideSupportTicketNo 临时替换工单号生成器，返回恢复函数。
func overrideSupportTicketNo(t *testing.T, generator func(time.Time) (string, error)) {
	t.Helper()
	original := supportTicketNoGenerator
	supportTicketNoGenerator = generator
	t.Cleanup(func() { supportTicketNoGenerator = original })
}

// TestSupportTicketCreateValidation 覆盖提交工单的边界校验与工单号形态。
func TestSupportTicketCreateValidation(t *testing.T) {
	env := newSupportTestEnv(t)
	seedSupportUser(t, env, "user-a", "阿甲", "a@example.com")

	valid := SupportTicketInput{Category: SupportCategoryBug, Title: "支付失败", Body: "订单支付后没有生效"}
	cases := []struct {
		name   string
		mutate func(*SupportTicketInput)
	}{
		{"标题为空", func(in *SupportTicketInput) { in.Title = "   " }},
		{"标题超长", func(in *SupportTicketInput) { in.Title = strings.Repeat("标", supportTicketTitleMaxRunes+1) }},
		{"正文为空", func(in *SupportTicketInput) { in.Body = "" }},
		{"正文超长", func(in *SupportTicketInput) { in.Body = strings.Repeat("正", supportTicketBodyMaxRunes+1) }},
		{"分类为空", func(in *SupportTicketInput) { in.Category = "" }},
		{"分类非法", func(in *SupportTicketInput) { in.Category = "URGENT" }},
		{"联系方式超长", func(in *SupportTicketInput) { in.Contact = strings.Repeat("1", supportTicketContactMaxRunes+1) }},
	}
	for _, item := range cases {
		input := valid
		item.mutate(&input)
		if _, err := env.service.CreateSupportTicket("user-a", input); err == nil {
			t.Fatalf("%s：应被拒绝", item.name)
		} else {
			assertSupportError(t, err, 400, "")
		}
	}

	// 边界值本身合法：标题 80 字、正文 2000 字、联系方式 120 字都应通过。
	boundary := SupportTicketInput{
		Category: SupportCategoryFeature,
		Title:    strings.Repeat("标", supportTicketTitleMaxRunes),
		Body:     strings.Repeat("正", supportTicketBodyMaxRunes),
		Contact:  strings.Repeat("1", supportTicketContactMaxRunes),
	}
	view, err := env.service.CreateSupportTicket("user-a", boundary)
	if err != nil {
		t.Fatalf("边界值应通过校验: %v", err)
	}
	if !supportTicketNoPattern.MatchString(view.TicketNo) {
		t.Fatalf("工单号格式不符合契约：%q", view.TicketNo)
	}
	if view.Status != SupportStatusOpen {
		t.Fatalf("新建工单应为待处理，实际 %q", view.Status)
	}
	if view.Replies == nil || len(view.Replies) != 0 {
		t.Fatalf("新建工单不应有回复：%+v", view.Replies)
	}
	if view.UserName != "阿甲" || view.UserEmail != "a@example.com" {
		t.Fatalf("工单视图缺少账号信息：%+v", view)
	}
	if view.ClosedAt != nil || view.AssigneeID != "" {
		t.Fatalf("新建工单不应有关闭时间或处理人：%+v", view)
	}
}

// TestSupportTicketNoUniqueAndRetry 守住工单号的唯一索引与撞号重试。
func TestSupportTicketNoUniqueAndRetry(t *testing.T) {
	env := newSupportTestEnv(t)
	seedSupportUser(t, env, "user-a", "阿甲", "a@example.com")

	// 生成器的形态与唯一性：连续建 10 条，工单号互不相同且都符合契约。
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		view := createSupportTicket(t, env, "user-a", "工单")
		if !supportTicketNoPattern.MatchString(view.TicketNo) {
			t.Fatalf("工单号格式不符合契约：%q", view.TicketNo)
		}
		if seen[view.TicketNo] {
			t.Fatalf("工单号重复：%q", view.TicketNo)
		}
		seen[view.TicketNo] = true
	}
	// 直接钉住生成器的格式：T + yyMMdd + 6 位大写十六进制。
	clockTime := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	generated, err := NewSupportTicketNo(clockTime)
	if err != nil {
		t.Fatalf("生成工单号失败: %v", err)
	}
	if !strings.HasPrefix(generated, "T260928") || !supportTicketNoPattern.MatchString(generated) {
		t.Fatalf("工单号前缀应为提交日期：%q", generated)
	}

	occupied := createSupportTicket(t, env, "user-a", "占位工单")

	// 生成器持续吐出已占用的工单号：重试 2 次仍冲突，必须回 500 而不是静默重复。
	overrideSupportTicketNo(t, func(time.Time) (string, error) { return occupied.TicketNo, nil })
	if _, err := env.service.CreateSupportTicket("user-a", SupportTicketInput{Category: SupportCategoryOther, Title: "撞号", Body: "正文"}); err == nil {
		t.Fatalf("工单号持续冲突时应返回内部故障")
	} else {
		assertSupportError(t, err, 500, "")
	}

	// 第一次撞号、第二次换到空闲号：整次提交应当成功，且落库的是新工单号。
	sequence := []string{occupied.TicketNo, "T260928ABCDEF"}
	overrideSupportTicketNo(t, func(time.Time) (string, error) {
		if len(sequence) == 0 {
			return "", errors.New("测试工单号序列耗尽")
		}
		value := sequence[0]
		sequence = sequence[1:]
		return value, nil
	})
	view, err := env.service.CreateSupportTicket("user-a", SupportTicketInput{Category: SupportCategoryOther, Title: "换号重试", Body: "正文"})
	if err != nil {
		t.Fatalf("换号后应创建成功: %v", err)
	}
	if view.TicketNo != "T260928ABCDEF" {
		t.Fatalf("应使用重试后的工单号，实际 %q", view.TicketNo)
	}
}

// TestSupportTicketUserIsolation 确认越权与不存在给出同一文案的 403。
func TestSupportTicketUserIsolation(t *testing.T) {
	env := newSupportTestEnv(t)
	seedSupportUser(t, env, "user-a", "阿甲", "a@example.com")
	seedSupportUser(t, env, "user-b", "阿乙", "b@example.com")
	ticket := createSupportTicket(t, env, "user-a", "只有本人能看")

	_, err := env.service.SupportTicketForUser("user-b", ticket.ID)
	assertSupportError(t, err, 403, "工单不存在")

	_, err = env.service.SupportTicketForUser("user-b", "not-a-real-ticket")
	assertSupportError(t, err, 403, "工单不存在")

	_, err = env.service.ReplySupportTicket("user-b", ticket.ID, "越权回复")
	assertSupportError(t, err, 403, "工单不存在")

	// 同一账号自己读得到。
	if _, err := env.service.SupportTicketForUser("user-a", ticket.ID); err != nil {
		t.Fatalf("本人应能读取自己的工单: %v", err)
	}
}

// TestSupportTicketReplyAndClosed 覆盖用户回复、关闭后不可回复与重新打开。
func TestSupportTicketReplyAndClosed(t *testing.T) {
	env := newSupportTestEnv(t)
	seedSupportUser(t, env, "user-a", "阿甲", "a@example.com")
	ticket := createSupportTicket(t, env, "user-a", "回复与关闭")

	view, err := env.service.ReplySupportTicket("user-a", ticket.ID, "补充一下信息")
	if err != nil {
		t.Fatalf("本人回复失败: %v", err)
	}
	if len(view.Replies) != 1 {
		t.Fatalf("回复数应为 1，实际 %d", len(view.Replies))
	}
	if view.Replies[0].AuthorRole != SupportAuthorUser || view.Replies[0].AuthorID != "user-a" {
		t.Fatalf("用户回复的作者角色/标识错误：%+v", view.Replies[0])
	}
	if _, err := env.service.ReplySupportTicket("user-a", ticket.ID, "  "); err == nil {
		t.Fatalf("空回复应被拒绝")
	}

	closed, err := env.service.UpdateSupportTicketStatus(ticket.ID, SupportStatusClosed)
	if err != nil {
		t.Fatalf("关闭工单失败: %v", err)
	}
	if closed.ClosedAt == nil {
		t.Fatalf("转 CLOSED 必须写入 closed_at")
	}
	_, err = env.service.ReplySupportTicket("user-a", ticket.ID, "关了还能回吗")
	assertSupportError(t, err, 409, "工单已关闭")

	reopened, err := env.service.UpdateSupportTicketStatus(ticket.ID, SupportStatusProcessing)
	if err != nil {
		t.Fatalf("重新打开失败: %v", err)
	}
	if reopened.ClosedAt != nil {
		t.Fatalf("离开 CLOSED 必须清空 closed_at，实际 %v", reopened.ClosedAt)
	}
	if _, err := env.service.ReplySupportTicket("user-a", ticket.ID, "重新打开后可以回复"); err != nil {
		t.Fatalf("重新打开后应可回复: %v", err)
	}

	_, err = env.service.UpdateSupportTicketStatus(ticket.ID, "URGENT")
	assertSupportError(t, err, 400, "工单状态不合法")
	_, err = env.service.UpdateSupportTicketStatus("missing-ticket", SupportStatusResolved)
	assertSupportError(t, err, 404, "工单不存在")
}

// TestSupportTicketStaffReplyAdvancesStatus 确认客服回复把 OPEN 自动推进到 PROCESSING。
func TestSupportTicketStaffReplyAdvancesStatus(t *testing.T) {
	env := newSupportTestEnv(t)
	seedSupportUser(t, env, "user-a", "阿甲", "a@example.com")
	seedSupportUser(t, env, "staff-1", "客服小张", "staff@example.com")
	ticket := createSupportTicket(t, env, "user-a", "客服回复")

	view, err := env.service.AdminReplySupportTicket(ticket.ID, "staff-1", "已收到，正在排查")
	if err != nil {
		t.Fatalf("客服回复失败: %v", err)
	}
	if view.Status != SupportStatusProcessing {
		t.Fatalf("客服回复后 OPEN 应转为 PROCESSING，实际 %q", view.Status)
	}
	if len(view.Replies) != 1 {
		t.Fatalf("回复数应为 1，实际 %d", len(view.Replies))
	}
	last := view.Replies[len(view.Replies)-1]
	if last.AuthorRole != SupportAuthorStaff || last.AuthorID != "staff-1" || last.AuthorName != "客服小张" {
		t.Fatalf("客服回复的作者信息错误：%+v", last)
	}
	// 已经是 PROCESSING 的工单再回复不会回退状态。
	view, err = env.service.AdminReplySupportTicket(ticket.ID, "staff-1", "继续跟进")
	if err != nil {
		t.Fatalf("二次回复失败: %v", err)
	}
	if view.Status != SupportStatusProcessing {
		t.Fatalf("状态不应回退，实际 %q", view.Status)
	}
}

// TestSupportTicketAdminFilters 覆盖后台状态筛选与关键字命中工单号 / 标题 / 账号。
func TestSupportTicketAdminFilters(t *testing.T) {
	env := newSupportTestEnv(t)
	seedSupportUser(t, env, "user-a", "阿甲", "a@example.com")
	seedSupportUser(t, env, "user-b", "阿乙", "b@example.com")
	first := createSupportTicket(t, env, "user-a", "支付失败求助")
	second := createSupportTicket(t, env, "user-b", "界面卡顿")
	if _, err := env.service.UpdateSupportTicketStatus(second.ID, SupportStatusResolved); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}

	// 全量列表带上不受筛选影响的计数。
	all, err := env.service.AdminSupportTickets(SupportTicketFilter{})
	if err != nil {
		t.Fatalf("后台列表失败: %v", err)
	}
	if all.Total != 2 || all.Counts == nil || all.Counts.Total != 2 {
		t.Fatalf("全量读数错误：total=%d counts=%+v", all.Total, all.Counts)
	}
	if all.Counts.Open != 1 || all.Counts.Resolved != 1 {
		t.Fatalf("状态计数错误：%+v", all.Counts)
	}

	// status 过滤。
	resolved, err := env.service.AdminSupportTickets(SupportTicketFilter{Status: SupportStatusResolved})
	if err != nil {
		t.Fatalf("按状态筛选失败: %v", err)
	}
	if resolved.Total != 1 || len(resolved.Tickets) != 1 || resolved.Tickets[0].ID != second.ID {
		t.Fatalf("按状态筛选结果错误：%+v", resolved.Tickets)
	}
	// 筛选不该影响全局计数。
	if resolved.Counts == nil || resolved.Counts.Total != 2 {
		t.Fatalf("筛选后计数应保持全量：%+v", resolved.Counts)
	}

	keywordCases := []struct {
		name    string
		keyword string
		want    string
	}{
		{"命中工单号", first.TicketNo, first.ID},
		{"命中标题", "卡顿", second.ID},
		{"命中账号邮箱", "a@example.com", first.ID},
	}
	for _, item := range keywordCases {
		page, err := env.service.AdminSupportTickets(SupportTicketFilter{Keyword: item.keyword})
		if err != nil {
			t.Fatalf("%s：查询失败 %v", item.name, err)
		}
		if page.Total != 1 || len(page.Tickets) != 1 || page.Tickets[0].ID != item.want {
			t.Fatalf("%s：结果错误 %+v", item.name, page.Tickets)
		}
	}

	_, err = env.service.AdminSupportTickets(SupportTicketFilter{Status: "NOPE"})
	assertSupportError(t, err, 400, "工单状态不合法")
}

// TestSupportTicketUserListPaging 确认用户端列表按账号隔离并分页。
func TestSupportTicketUserListPaging(t *testing.T) {
	env := newSupportTestEnv(t)
	seedSupportUser(t, env, "user-a", "阿甲", "a@example.com")
	seedSupportUser(t, env, "user-b", "阿乙", "b@example.com")
	for i := 0; i < 3; i++ {
		createSupportTicket(t, env, "user-a", "我的工单")
	}
	createSupportTicket(t, env, "user-b", "别人的工单")

	firstPage, err := env.service.UserSupportTickets("user-a", 1, 2)
	if err != nil {
		t.Fatalf("读取我的工单失败: %v", err)
	}
	if firstPage.Total != 3 || len(firstPage.Tickets) != 2 {
		t.Fatalf("分页结果错误：total=%d len=%d", firstPage.Total, len(firstPage.Tickets))
	}
	for _, ticket := range firstPage.Tickets {
		if ticket.UserID != "user-a" {
			t.Fatalf("列表混入其它账号的工单：%+v", ticket)
		}
	}
	other, err := env.service.UserSupportTickets("user-b", 1, 20)
	if err != nil {
		t.Fatalf("读取我的工单失败: %v", err)
	}
	if other.Total != 1 {
		t.Fatalf("账号隔离失败：%d", other.Total)
	}

	if _, err := env.service.UserSupportTickets("", 1, 20); err == nil {
		t.Fatalf("缺少账号标识应被拒绝")
	} else {
		assertSupportError(t, err, 400, "")
	}
	if _, err := env.service.CreateSupportTicket("", SupportTicketInput{Category: SupportCategoryOther, Title: "无主", Body: "正文"}); err == nil {
		t.Fatalf("缺少账号标识应被拒绝")
	}
}
