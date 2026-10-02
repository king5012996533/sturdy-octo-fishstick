package app

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// fakeCreditLedger 记录计费端口的调用，用来断言"任务域到底把什么交给了计费域"。
type fakeCreditLedger struct {
	requests      []TaskChargeRequest
	chargeErr     error
	charges       int64
	refunds       []string
	refundCredits int64
	refunded      bool
	refundErr     error
}

func (f *fakeCreditLedger) ChargeTask(request TaskChargeRequest) (TaskChargeOutcome, error) {
	if f.chargeErr != nil {
		return TaskChargeOutcome{}, f.chargeErr
	}
	f.requests = append(f.requests, request)
	return TaskChargeOutcome{Credits: f.charges}, nil
}

// QuoteTask 试算复用与 ChargeTask 同一份假数据：试算用例关心的是"编排层有没有把同一组
// 入参交出去"，金额由账号域决定，这里没有可分辨的差异。
func (f *fakeCreditLedger) QuoteTask(request TaskChargeRequest) (TaskChargeOutcome, error) {
	if f.chargeErr != nil {
		return TaskChargeOutcome{}, f.chargeErr
	}
	f.requests = append(f.requests, request)
	return TaskChargeOutcome{Credits: f.charges, Priced: true, Quantity: 1}, nil
}

func (f *fakeCreditLedger) RefundTask(userID string, taskID string, note string) (int64, bool, error) {
	if f.refundErr != nil {
		return 0, false, f.refundErr
	}
	f.refunds = append(f.refunds, taskID+"|"+note)
	return f.refundCredits, f.refunded, nil
}

// newTaskCreditTestService 建一个带计费端口的托管服务；其余依赖留空，
// 这些用例只覆盖端口编排，不碰存储。
func newTaskCreditTestService(t *testing.T) (*Service, *fakeCreditLedger) {
	t.Helper()
	svc, db := newFeatureAvailabilityTestService(t)
	// 退款判据要读提交记录才能证明"上游没受理"，用例库必须建出这张表；
	// 否则读取失败会按偏向平台的方式拒绝退款，用例就测不到真正想测的分支。
	if err := db.AutoMigrate(&model.RouteAttempt{}); err != nil {
		t.Fatal(err)
	}
	ledger := &fakeCreditLedger{charges: 450}
	svc.UseTaskCreditLedger(ledger)
	return svc, ledger
}

// newTaskCreditTestTask 造一条可以直接进入退款判据的任务行。
func newTaskCreditTestTask(id string) *model.Task {
	return &model.Task{ID: id, UserID: "user-1"}
}

// TestChargeTaskCreditsDerivesModelKeyCapabilityAndQuantity 覆盖交给计费域的三个关键字段。
//
// 模型标识取「渠道::模型」而不是裸模型名：不同渠道可能上架同名 SKU，裸名会让两家
// 的价目表串在一起。用量取视频秒数，图片取张数。
func TestChargeTaskCreditsDerivesModelKeyCapabilityAndQuantity(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	task := &model.Task{ID: "task-1", UserID: "user-1", Type: "canvas_video", Operation: "image_to_video"}
	input := map[string]any{
		"mode":              "video",
		"config":            map[string]any{"channelId": "CHANNEL_000007", "channelModelKey": "seedance-2.5", "model": "seedance-2.5", "videoSeconds": "30"},
		"capabilityOptions": map[string]any{"videoSeconds": "30", "size": "16:9"},
	}

	if err := svc.chargeTaskCredits(task, input); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
	if len(ledger.requests) != 1 {
		t.Fatalf("应只调用一次计费，实际 %d 次", len(ledger.requests))
	}
	request := ledger.requests[0]
	if request.ModelKey != "CHANNEL_000007::seedance-2.5" {
		t.Fatalf("模型标识应为 渠道::模型，实际 %q", request.ModelKey)
	}
	if request.Capability != "video" {
		t.Fatalf("能力应为 video，实际 %q", request.Capability)
	}
	if request.Quantity != 30 {
		t.Fatalf("用量应为 30 秒，实际 %d", request.Quantity)
	}
	if request.UserID != "user-1" || request.TaskID != "task-1" {
		t.Fatalf("账号与任务标识应原样透传，实际 %#v", request)
	}
}

// TestChargeTaskCreditsCountsImagesByOutputCount 覆盖图片按张计费。
func TestChargeTaskCreditsCountsImagesByOutputCount(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	task := &model.Task{ID: "task-2", UserID: "user-1", Type: "canvas_image", Operation: "text_to_image"}
	input := map[string]any{
		"mode":              "image",
		"config":            map[string]any{"channelId": "CHANNEL_000009", "channelModelKey": "gpt-image-2", "model": "gpt-image-2"},
		"capabilityOptions": map[string]any{"count": 4},
	}

	if err := svc.chargeTaskCredits(task, input); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
	if ledger.requests[0].ModelKey != "CHANNEL_000009::gpt-image-2" || ledger.requests[0].Quantity != 4 {
		t.Fatalf("图片应扣 4 张的价，实际 %#v", ledger.requests[0])
	}
}

// TestQuoteTaskCreditsSharesChargeInputs 覆盖"试算与预扣交出同一组入参"。
//
// 生成前的预计消耗与实际扣款必须是同一笔账。两条路径各写一份取价入参，报价会在
// 渠道、档位或用量上与实扣分叉，而这类偏差不会报错，只会让用户看到两个不一样的数。
func TestQuoteTaskCreditsSharesChargeInputs(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	task := &model.Task{ID: "task-6", UserID: "user-1", Type: "canvas_video", Operation: "image_to_video"}
	input := map[string]any{
		"mode":              "video",
		"config":            map[string]any{"channelId": "CHANNEL_000007", "channelModelKey": "seedance-2.5", "model": "seedance-2.5", "videoSeconds": "15"},
		"capabilityOptions": map[string]any{"videoSeconds": "15", "size": "16:9"},
	}

	if err := svc.chargeTaskCredits(task, input); err != nil {
		t.Fatalf("预扣失败: %v", err)
	}
	quote, err := svc.quoteTaskCredits(task, input)
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if quote == nil || quote.Credits != 450 {
		t.Fatalf("试算应透出计费域给的金额，实际 %#v", quote)
	}
	if len(ledger.requests) != 2 {
		t.Fatalf("预扣与试算各应调用一次端口，实际 %d 次", len(ledger.requests))
	}
	if ledger.requests[0] != ledger.requests[1] {
		t.Fatalf("两个入口的计费入参应完全一致，实际 %#v 与 %#v", ledger.requests[0], ledger.requests[1])
	}
}

// TestQuoteTaskChargeRequiresBillingLedger 覆盖未接计费端口时不报价。
//
// 返回一个 0 会让前端显示"本次免费"，而真相是这个实例根本没有计费能力；
// "不知道价格"与"价格是零"必须可区分。
func TestQuoteTaskChargeRequiresBillingLedger(t *testing.T) {
	svc, _ := newFeatureAvailabilityTestService(t)
	_, err := svc.QuoteTaskCharge("user-1", CreateTaskRequest{Type: "canvas_image", Prompt: "一只猫", Input: map[string]any{"mode": "image"}})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 503 {
		t.Fatalf("未接计费端口应回 503，实际 %v", err)
	}
}

// TestChargeTaskCreditsIsNoOpWithoutLedger 覆盖本地/桌面装载：没有计费端口就不该报错。
func TestChargeTaskCreditsIsNoOpWithoutLedger(t *testing.T) {
	svc, _ := newFeatureAvailabilityTestService(t)
	task := &model.Task{ID: "task-3", UserID: "user-1", Type: "canvas_image"}
	if err := svc.chargeTaskCredits(task, map[string]any{"mode": "image"}); err != nil {
		t.Fatalf("未接计费端口时不该报错: %v", err)
	}
}

// TestChargeTaskCreditsPropagatesLedgerError 覆盖余额不足等失败必须挡住任务创建。
//
// 吞掉这个错误就等于"余额不足也照样出片"，而这正是计费体系存在的理由。
func TestChargeTaskCreditsPropagatesLedgerError(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	ledger.chargeErr = errors.New("积分不足，请先充值后再试")

	task := &model.Task{ID: "task-4", UserID: "user-1", Type: "canvas_image"}
	err := svc.chargeTaskCredits(task, map[string]any{"mode": "image"})
	if err == nil || err.Error() != "积分不足，请先充值后再试" {
		t.Fatalf("预扣失败应原样上抛，实际 %v", err)
	}
}

// TestRefundTaskCreditsSwallowsLedgerFailure 覆盖退回失败不改变任务终态。
//
// 退回失败只能留下痕迹：任务已经失败是既成事实，把错误上抛会让 worker 反复重试终态写入。
func TestRefundTaskCreditsSwallowsLedgerFailure(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	ledger.refundErr = errors.New("账号库不可用")

	svc.refundTaskCredits(newTaskCreditTestTask("task-5"), nil, "任务失败退回预扣")
	if len(ledger.refunds) != 0 {
		t.Fatal("退回失败时不该记录成功")
	}

	// 没有可退的东西（免费任务或已退过）同样不该报错。
	ledger.refundErr = nil
	ledger.refunded = false
	svc.refundTaskCredits(newTaskCreditTestTask("task-5"), nil, "任务失败退回预扣")
}

// TestTaskChargeQuantityFallsBackForUnknownQuantity 覆盖用量缺失时交给计费域处理。
//
// 这里刻意返回 0 而不是 1：端口对 0 的定义是"用量未知按一个单位计"，回退规则只该
// 有一处，落在计费域里。
func TestTaskChargeQuantityFallsBackForUnknownQuantity(t *testing.T) {
	if got := taskChargeQuantity(ModelRequestIntent{Capability: "video"}); got != 0 {
		t.Fatalf("用量未知应回 0，实际 %d", got)
	}
	if got := taskChargeQuantity(ModelRequestIntent{Capability: "text"}); got != 0 {
		t.Fatalf("文本无法在提交时得知 token 数，应回 0，实际 %d", got)
	}
	if got := taskChargeQuantity(ModelRequestIntent{Capability: "image", Options: map[string]any{"count": "不是数字"}}); got != 0 {
		t.Fatalf("解析失败应回 0，实际 %d", got)
	}
	if got := taskChargeQuantity(ModelRequestIntent{Capability: "video", Options: map[string]any{"videoSeconds": "15"}}); got != 15 {
		t.Fatalf("字符串秒数应解析成 15，实际 %d", got)
	}
}

// TestTaskChargeQuantityForAudioIsPerRequest 覆盖音频按次。
//
// 音频与视频在这里分道：视频的秒数来自用户选择，音频的时长提交时还不存在。所以音频返回
// 1 而不是 0——0 的含义是"量未知、请计费域兜底"，而音频的用量是确定的：一次调用。
// 两者的区别落在账单上就是"30 分/次 × 1"与"30 分/秒 × 1"，后者会让用户以为这是按秒收的。
func TestTaskChargeQuantityForAudioIsPerRequest(t *testing.T) {
	if got := taskChargeQuantity(ModelRequestIntent{Capability: "audio"}); got != 1 {
		t.Fatalf("音频应按次回 1，实际 %d", got)
	}
	// 即使误传了 videoSeconds 也不参与计价：音频不是按秒结算的。
	if got := taskChargeQuantity(ModelRequestIntent{Capability: "audio", Options: map[string]any{"videoSeconds": "60"}}); got != 1 {
		t.Fatalf("音频不应读 videoSeconds，实际 %d", got)
	}
}

// TestTaskChargeModelKeyFallsBackToTaskModel 覆盖前台模型模式（没有渠道）。
func TestTaskChargeModelKeyFallsBackToTaskModel(t *testing.T) {
	task := &model.Task{Model: "seedance-v25-logical"}
	got := taskChargeModelKey(map[string]any{"config": map[string]any{}}, task)
	if got != "seedance-v25-logical" {
		t.Fatalf("没有渠道时应退回逻辑模型 code，实际 %q", got)
	}
}

// TestTaskChargeTierFollowsUpstreamImageQuality 覆盖图片按质量档取价。
//
// 上游对 gpt-image-2 的 low / medium / high 分别定价（差价 10.7 倍），档位是取价的第三
// 个维度，认错就等于按另一个成本出货。
func TestTaskChargeTierFollowsUpstreamImageQuality(t *testing.T) {
	cases := []struct {
		name   string
		intent ModelRequestIntent
		want   string
	}{
		{name: "小写档位归一成大写", intent: ModelRequestIntent{Capability: "image", Options: map[string]any{"quality": "low"}}, want: "LOW"},
		{name: "中档", intent: ModelRequestIntent{Capability: "image", Options: map[string]any{"quality": "medium"}}, want: "MEDIUM"},
		{name: "高档", intent: ModelRequestIntent{Capability: "image", Options: map[string]any{"quality": "high"}}, want: "HIGH"},
		// 面板选 auto 时不会带 quality，这时必须是空档而不是某个具体档：回落到低档就等于
		// 用 low 的成本去卖一次 high 的调用。
		{name: "面板选 auto 时为空白档", intent: ModelRequestIntent{Capability: "image", Options: map[string]any{"count": "1"}}, want: ""},
		{name: "认不出的档位不当成低价档", intent: ModelRequestIntent{Capability: "image", Options: map[string]any{"quality": "standard"}}, want: ""},
		{name: "文本在提交时还不知道 token 档位", intent: ModelRequestIntent{Capability: "text"}, want: ""},
		{name: "视频只有一档价", intent: ModelRequestIntent{Capability: "video", Options: map[string]any{"quality": "high"}}, want: ""},
	}
	for _, test := range cases {
		if got := taskChargeTier(test.intent); got != test.want {
			t.Fatalf("%s：档位应为 %q，实际 %q", test.name, test.want, got)
		}
	}
}

// TestChargeTaskCreditsSendsImageQualityTier 覆盖档位真的进了计费端口，而不只是算出来。
func TestChargeTaskCreditsSendsImageQualityTier(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	task := &model.Task{ID: "task-image", UserID: "user-1", Type: "canvas_image"}
	input := map[string]any{
		"mode":              "image",
		"config":            map[string]any{"channelId": "CHANNEL_000003", "channelModelKey": "openai/gpt-image-2", "model": "openai/gpt-image-2"},
		"capabilityOptions": map[string]any{"quality": "high", "count": "4"},
	}

	if err := svc.chargeTaskCredits(task, input); err != nil {
		t.Fatalf("扣费不应失败：%v", err)
	}
	if len(ledger.requests) != 1 {
		t.Fatalf("应只提交一次计费，实际 %d", len(ledger.requests))
	}
	request := ledger.requests[0]
	if request.Capability != "image" || request.Tier != "HIGH" || request.Quantity != 4 {
		t.Fatalf("计费入参应为 image/HIGH/4，实际 %s/%q/%d", request.Capability, request.Tier, request.Quantity)
	}
	if request.ModelKey != "CHANNEL_000003::openai/gpt-image-2" {
		t.Fatalf("模型标识应为渠道::模型，实际 %q", request.ModelKey)
	}
}

// TestTaskRefundVerdictFollowsUpstreamSubmissionEvidence 覆盖退款判据。
//
// 这是计费里最贵的一条判断：判成"该退"就是平台替上游买单，判成"不该退"就是用户白付。
// 判据因此不看任务成没成，只看能不能证明上游没有受理这次请求。
func TestTaskRefundVerdictFollowsUpstreamSubmissionEvidence(t *testing.T) {
	cases := []struct {
		name       string
		task       *model.Task
		attempts   []model.RouteAttempt
		taskErr    error
		wantRefund bool
	}{
		{
			name:       "没有提交记录表示请求从未离开平台",
			task:       &model.Task{ID: "task-a", UserID: "user-1"},
			wantRefund: true,
		},
		{
			name:       "提交记录停在未发出",
			task:       &model.Task{ID: "task-b", UserID: "user-1"},
			attempts:   []model.RouteAttempt{{DispatchState: "not_sent"}},
			wantRefund: true,
		},
		{
			name:       "上游明确拒绝且没有建任务",
			task:       &model.Task{ID: "task-c", UserID: "user-1"},
			attempts:   []model.RouteAttempt{{DispatchState: "rejected_no_job"}},
			wantRefund: true,
		},
		{
			name:       "提交结果不明：上游可能已经受理并计费",
			task:       &model.Task{ID: "task-d", UserID: "user-1"},
			attempts:   []model.RouteAttempt{{DispatchState: "submission_unknown"}},
			wantRefund: false,
		},
		{
			name:       "已经拿到上游任务 ID",
			task:       &model.Task{ID: "task-e", UserID: "user-1", ProviderRequestID: "pred-1"},
			wantRefund: false,
		},
		{
			name:       "提交记录里带着上游任务 ID",
			task:       &model.Task{ID: "task-f", UserID: "user-1"},
			attempts:   []model.RouteAttempt{{DispatchState: "not_sent", ProviderRequestID: "pred-2"}},
			wantRefund: false,
		},
		{
			name:       "上游明确回执审核驳回，没有产出也不计费",
			task:       &model.Task{ID: "task-g", UserID: "user-1", ProviderRequestID: "pred-3"},
			taskErr:    errors.New("blocked_reason_safety"),
			wantRefund: true,
		},
		{
			name:       "上游已受理后超时：钱已经花出去了",
			task:       &model.Task{ID: "task-h", UserID: "user-1", ProviderRequestID: "pred-4"},
			taskErr:    context.DeadlineExceeded,
			wantRefund: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			svc, _ := newTaskCreditTestService(t)
			for index := range testCase.attempts {
				attempt := testCase.attempts[index]
				attempt.ID = "ATTEMPT" + strconv.Itoa(index+1)
				attempt.TaskID = testCase.task.ID
				attempt.AttemptNumber = index + 1
				if err := svc.repo.CreateRouteAttempt(&attempt); err != nil {
					t.Fatalf("写入提交记录失败: %v", err)
				}
			}
			refundable, reason := svc.taskRefundVerdict(testCase.task, testCase.taskErr)
			if refundable != testCase.wantRefund {
				t.Fatalf("退款判定 = %v（%s），期望 %v", refundable, reason, testCase.wantRefund)
			}
			if !refundable && reason == "" {
				t.Fatal("拒绝退款必须给出可查的原因")
			}
		})
	}
}

// TestTaskRefundVerdictRefusesWhenSubmissionEvidenceUnreadable 覆盖读不到提交记录时的取向。
//
// 读不到证据就无法证明上游没受理。这里必须偏向平台并留下可查原因：悄悄按"该退"处理会
// 变成净资损，而多留一笔可以通过申诉和后台手工补退纠正。
func TestTaskRefundVerdictRefusesWhenSubmissionEvidenceUnreadable(t *testing.T) {
	svc, _ := newTaskCreditTestService(t)
	svc.repo = nil
	refundable, reason := svc.taskRefundVerdict(&model.Task{ID: "task-1", UserID: "user-1", ProviderRequestID: "pred-9"}, nil)
	if refundable {
		t.Fatal("读不到提交记录时不应退款")
	}
	if reason == "" {
		t.Fatal("拒绝退款必须给出可查的原因")
	}
}

// TestRefundTaskCreditsSkipsLedgerWhenUpstreamAccepted 覆盖"上游已受理就不动账"。
//
// 端到端口径：任务取消或失败时，只要请求确实发出去过，计费端口一次都不该被调用。
func TestRefundTaskCreditsSkipsLedgerWhenUpstreamAccepted(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	ledger.refunds = nil
	if err := svc.repo.CreateRouteAttempt(&model.RouteAttempt{ID: "ATTEMPT1", TaskID: "task-9", AttemptNumber: 1, DispatchState: "dispatching"}); err != nil {
		t.Fatalf("写入提交记录失败: %v", err)
	}
	svc.refundTaskCredits(&model.Task{ID: "task-9", UserID: "user-1"}, nil, "任务取消退回预扣")
	if len(ledger.refunds) != 0 {
		t.Fatalf("上游已受理时不该动账，实际 %v", ledger.refunds)
	}
}

// TestRefundTaskCreditsRefundsBeforeAnyDispatch 覆盖"请求没发出去就该退"。
func TestRefundTaskCreditsRefundsBeforeAnyDispatch(t *testing.T) {
	svc, ledger := newTaskCreditTestService(t)
	ledger.refundCredits, ledger.refunded = 450, true
	svc.refundTaskCredits(&model.Task{ID: "task-10", UserID: "user-1"}, nil, "任务取消退回预扣")
	if len(ledger.refunds) != 1 || ledger.refunds[0] != "task-10|任务取消退回预扣" {
		t.Fatalf("请求未发出时应退回预扣，实际 %v", ledger.refunds)
	}
}
