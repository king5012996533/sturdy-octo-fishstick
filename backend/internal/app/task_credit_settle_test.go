package app

import (
	"encoding/json"
	"testing"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// newTextSettleTestService 建一个带计费端口的托管服务，并备好用量表的库。
func newTextSettleTestService(t *testing.T) (*Service, *fakeCreditLedger, *gorm.DB) {
	t.Helper()
	svc, db := newFeatureAvailabilityTestService(t)
	if err := db.AutoMigrate(&model.ApiCallLog{}); err != nil {
		t.Fatalf("建用量表失败: %v", err)
	}
	ledger := &fakeCreditLedger{
		charges:      1,
		settleResult: TaskTextSettleOutcome{Credits: 17, Charged: 1, Delta: 16, Priced: true, Note: "结算说明"},
	}
	svc.UseTaskCreditLedger(ledger)
	return svc, ledger, db
}

// newTextSettleTask 造一条已经成功收尾的文本任务，输入与真实提交同形。
func newTextSettleTask(t *testing.T, input map[string]any) *model.Task {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("序列化任务输入失败: %v", err)
	}
	return &model.Task{ID: "task-1", UserID: "user-1", Type: "canvas_text", Operation: "cloud_agent", InputJSON: string(encoded)}
}

// seedUsageLogs 往用量表里塞调用记录，ID 显式给出以便断言聚合结果可复现。
func seedUsageLogs(t *testing.T, db *gorm.DB, logs []model.ApiCallLog) {
	t.Helper()
	for index := range logs {
		entry := logs[index]
		entry.TaskID = "task-1"
		entry.UserID = "user-1"
		if err := db.Create(&entry).Error; err != nil {
			t.Fatalf("写入用量记录失败: %v", err)
		}
	}
}

// TestSettleTextTaskCreditsForwardsAggregatedUsage 覆盖结算编排的主路径。
//
// 任务域要交对三件东西：真实的 token 用量、与预扣同源的模型标识、以及"这次是文本任务"。
// 前两项错了，账号域会按错误的价算出错误的金额；第三项错了，图片与视频会被重复结算。
func TestSettleTextTaskCreditsForwardsAggregatedUsage(t *testing.T) {
	svc, ledger, db := newTextSettleTestService(t)
	task := newTextSettleTask(t, map[string]any{
		"mode":   "text",
		"config": map[string]any{"channelId": "CHANNEL_000011", "channelModelKey": "gpt-6-sol"},
	})
	seedUsageLogs(t, db, []model.ApiCallLog{
		{ID: "log-1", Capability: "text", RequestKind: "create", InputTokens: 20_000, CachedTokens: 12_000, OutputTokens: 1_000, UsageAvailable: true},
		// 一次会话会连打多轮，用量必须求和而不是只取最后一轮。
		{ID: "log-2", Capability: "text", RequestKind: "create", InputTokens: 5_000, CachedTokens: 4_000, OutputTokens: 500, UsageAvailable: true},
		// 取件与轮询不产生 token，必须排除。
		{ID: "log-3", Capability: "text", RequestKind: "poll", InputTokens: 9_999_999, OutputTokens: 9_999_999},
		{ID: "log-4", Capability: "text", RequestKind: "download", InputTokens: 9_999_999, OutputTokens: 9_999_999},
		// 别的能力的调用不该算进文本账。
		{ID: "log-5", Capability: "image", RequestKind: "create", InputTokens: 9_999_999, OutputTokens: 9_999_999},
	})

	svc.settleTextTaskCredits(task)

	if len(ledger.settles) != 1 {
		t.Fatalf("应结算一次，实际 %d 次", len(ledger.settles))
	}
	request := ledger.settles[0]
	if request.ModelKey != "CHANNEL_000011::gpt-6-sol" {
		t.Fatalf("模型标识应为「渠道::模型」，实际 %q", request.ModelKey)
	}
	if request.InputTokens != 25_000 || request.CachedTokens != 16_000 || request.OutputTokens != 1_500 {
		t.Fatalf("用量聚合错误：input=%d cached=%d output=%d", request.InputTokens, request.CachedTokens, request.OutputTokens)
	}
	if request.UserID != "user-1" || request.TaskID != "task-1" {
		t.Fatalf("账号与任务标识应原样传出，实际 %q/%q", request.UserID, request.TaskID)
	}
}

// TestSettleTextTaskCreditsSkipsTasksWithoutUpstreamCalls 覆盖文本回放草稿。
//
// 前端自管的持久化任务也叫 canvas_text，但它从不调上游，也就没有任何可结算的用量。
// 不跳过的话，用户每存一次草稿都会被补扣一次。
func TestSettleTextTaskCreditsSkipsTasksWithoutUpstreamCalls(t *testing.T) {
	svc, ledger, _ := newTextSettleTestService(t)
	task := newTextSettleTask(t, map[string]any{
		"mode":   "text",
		"config": map[string]any{"channelId": "CHANNEL_000011", "channelModelKey": "gpt-6-sol"},
	})

	svc.settleTextTaskCredits(task)

	if len(ledger.settles) != 0 {
		t.Fatalf("没有上游调用不应结算，实际 %d 次", len(ledger.settles))
	}
}

// TestSettleTextTaskCreditsSkipsNonTextCapability 覆盖非文本任务不重复结算。
//
// 图片按张、视频按秒在提交时已经按足额预扣，再走一遍按用量补扣就是重复收费。
func TestSettleTextTaskCreditsSkipsNonTextCapability(t *testing.T) {
	svc, ledger, db := newTextSettleTestService(t)
	task := newTextSettleTask(t, map[string]any{
		"mode":   "image",
		"config": map[string]any{"channelId": "CHANNEL_000011", "channelModelKey": "gpt-image-2"},
	})
	seedUsageLogs(t, db, []model.ApiCallLog{
		{ID: "log-1", Capability: "text", RequestKind: "create", InputTokens: 20_000, OutputTokens: 1_000, UsageAvailable: true},
	})

	svc.settleTextTaskCredits(task)

	if len(ledger.settles) != 0 {
		t.Fatalf("非文本任务不应结算，实际 %d 次", len(ledger.settles))
	}
}

// TestSettleTextTaskCreditsSkipsWhenNoUsageReceipt 覆盖上游没回用量的情形。
//
// 有调用却按 0 结算等于白送，但报错会把一条已经成功的任务翻成失败。正确做法是留痕并
// 不落账，由运维对着上游账单人工核对。
func TestSettleTextTaskCreditsSkipsWhenNoUsageReceipt(t *testing.T) {
	svc, ledger, db := newTextSettleTestService(t)
	task := newTextSettleTask(t, map[string]any{
		"mode":   "text",
		"config": map[string]any{"channelId": "CHANNEL_000011", "channelModelKey": "gpt-6-sol"},
	})
	seedUsageLogs(t, db, []model.ApiCallLog{
		{ID: "log-1", Capability: "text", RequestKind: "create", Status: model.ApiCallStatusFailed},
	})

	svc.settleTextTaskCredits(task)

	if len(ledger.settles) != 0 {
		t.Fatalf("没有用量回执不应结算，实际 %d 次", len(ledger.settles))
	}
}

// TestHandleSuccessSettlesTextTaskCredits 覆盖成功收尾确实会触发结算。
//
// 结算挂在终态协调器上而不是 worker 的某条分支里：失败、取消、成功三条路径必须各自只
// 触发对应的资金动作，散开写迟早会出现某条分支漏扣或重复扣。
func TestHandleSuccessSettlesTextTaskCredits(t *testing.T) {
	svc, ledger, db := newTextSettleTestService(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatalf("建任务表失败: %v", err)
	}
	task := newTextSettleTask(t, map[string]any{
		"mode":   "text",
		"config": map[string]any{"channelId": "CHANNEL_000011", "channelModelKey": "gpt-6-sol"},
	})
	task.LeaseOwner = "worker-1"
	task.Status = model.TaskStatusSucceeded
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("写入任务失败: %v", err)
	}
	seedUsageLogs(t, db, []model.ApiCallLog{
		{ID: "log-1", Capability: "text", RequestKind: "create", InputTokens: 25_000, OutputTokens: 1_500, UsageAvailable: true},
	})

	if err := svc.terminalCoordinator().handleSuccess(task); err != nil {
		t.Fatalf("成功收尾不应报错: %v", err)
	}
	if len(ledger.settles) != 1 {
		t.Fatalf("成功收尾应触发一次结算，实际 %d 次", len(ledger.settles))
	}
	if ledger.settles[0].InputTokens != 25_000 {
		t.Fatalf("结算用量应取自用量表，实际 %d", ledger.settles[0].InputTokens)
	}
}

// TestSettleTextTaskCreditsSwallowsLedgerFailure 覆盖结算失败不改变任务终态。
//
// 结果已经落库、用户已经拿到东西，此时把结算错误抛上去只会让 worker 把成功的任务当失败。
func TestSettleTextTaskCreditsSwallowsLedgerFailure(t *testing.T) {
	svc, ledger, db := newTextSettleTestService(t)
	ledger.settleErr = &AppError{Status: 500, Message: "账号库不可用"}
	task := newTextSettleTask(t, map[string]any{
		"mode":   "text",
		"config": map[string]any{"channelId": "CHANNEL_000011", "channelModelKey": "gpt-6-sol"},
	})
	seedUsageLogs(t, db, []model.ApiCallLog{
		{ID: "log-1", Capability: "text", RequestKind: "create", InputTokens: 25_000, OutputTokens: 1_500, UsageAvailable: true},
	})

	svc.settleTextTaskCredits(task)
	if len(ledger.settles) != 1 {
		t.Fatalf("结算应被尝试一次（失败由日志留痕），实际 %d 次", len(ledger.settles))
	}
}

// textChargeInput 造一份文本任务的输入，模型标识取「渠道::模型」。
func textChargeInput() map[string]any {
	return map[string]any{
		"mode":   "text",
		"config": map[string]any{"channelId": "CHANNEL_000011", "channelModelKey": "gpt-6-sol"},
	}
}

// TestChargeTaskCreditsChecksTextMinimumBalance 覆盖"用户主动发起的那一轮要过水位"。
//
// 水位只在用户按下发送时校验：普通文本任务要过，会话进行中的续跑步骤与后台记忆压缩不过
// （否则一条跑到一半的会话会因为余额降到水位以下而断掉）。
func TestChargeTaskCreditsChecksTextMinimumBalance(t *testing.T) {
	cases := []struct {
		name      string
		taskType  string
		operation string
		expected  int
	}{
		{name: "用户发起的文本会话", taskType: "canvas_text", operation: "cloud_agent", expected: 1},
		{name: "创作页的文本任务", taskType: "canvas_text", operation: "text", expected: 1},
		{name: "会话续跑步骤", taskType: "canvas_text", operation: "cloud_agent_step", expected: 0},
		{name: "后台记忆压缩", taskType: "canvas_text", operation: "agent_memory_compact", expected: 0},
		{name: "图片任务", taskType: "canvas_image", operation: "text_to_image", expected: 0},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			svc, ledger := newTaskCreditTestService(t)
			task := &model.Task{ID: "task-1", UserID: "user-1", Type: item.taskType, Operation: item.operation}
			input := textChargeInput()
			if item.taskType == "canvas_image" {
				input = map[string]any{"mode": "image", "config": map[string]any{"channelId": "CHANNEL_000011", "channelModelKey": "gpt-image-2"}}
			}

			if err := svc.chargeTaskCredits(task, input); err != nil {
				t.Fatalf("预扣失败: %v", err)
			}
			if len(ledger.balanceChecks) != item.expected {
				t.Fatalf("水位校验次数应为 %d，实际 %d", item.expected, len(ledger.balanceChecks))
			}
			if item.expected == 1 && ledger.balanceChecks[0] != "user-1|CHANNEL_000011::gpt-6-sol" {
				t.Fatalf("水位校验要带上与预扣同源的模型标识，实际 %q", ledger.balanceChecks[0])
			}
			// 水位不过就是提交不成立：预扣也不能发生。
			if len(ledger.requests) != 1 {
				t.Fatalf("预扣应照常调用一次，实际 %d 次", len(ledger.requests))
			}
		})
	}
}

// TestChargeTaskCreditsBlocksWhenBalanceBelowWatermark 覆盖水位不足时整笔提交被拒。
//
// 水位不是"提醒"而是"不准开始"：放行之后再靠结算补扣，补不回来就只能记欠款。
func TestChargeTaskCreditsBlocksWhenBalanceBelowWatermark(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	ledger.balanceErr = &AppError{Status: 402, Code: 40201, Reason: "insufficient_credits", Message: "余额需保持在 40 积分以上"}
	task := &model.Task{ID: "task-1", UserID: "user-1", Type: "canvas_text", Operation: "cloud_agent"}

	err := svc.chargeTaskCredits(task, textChargeInput())
	if err == nil {
		t.Fatal("水位不足应拒绝提交")
	}
	if len(ledger.requests) != 0 {
		t.Fatalf("水位不过时不应预扣，实际 %d 次", len(ledger.requests))
	}
}
