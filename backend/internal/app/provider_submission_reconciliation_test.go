package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/generation"
	"infinite-canvas/backend/internal/model"
)

// submissionReconciliationFixture 造一条"提交结果未确认"的失败视频任务。
//
// 账单身份、阶段与时间都直接落库：对账循环是围绕"任务行 + 上游"的调度逻辑，
// 用例要控制的是"这条任务有多旧、手里有没有上游任务号"。
type submissionReconciliationFixture struct {
	service *Service
	ledger  *fakeCreditLedger
	db      *gorm.DB
	task    model.Task
}

func newSubmissionReconciliationFixture(t *testing.T, age time.Duration) submissionReconciliationFixture {
	t.Helper()
	svc, db := newFeatureAvailabilityTestService(t)
	if err := db.AutoMigrate(&model.Task{}, &model.RouteAttempt{}); err != nil {
		t.Fatal(err)
	}
	ledger := &fakeCreditLedger{charges: 180, refunded: true, refundCredits: 180}
	svc.UseTaskCreditLedger(ledger)
	task := model.Task{
		ID: "task-reconcile", UserID: "user-1", Type: "canvas_video", Operation: "text_to_video",
		Status: model.TaskStatusFailed, Stage: "submission_unknown", Progress: 100,
		Error:     "提交结果尚未确认，上游任务可能仍在执行。请先查询原任务状态，不要立即重新提交。",
		CreatedAt: time.Now().Add(-age), UpdatedAt: time.Now().Add(-age),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	return submissionReconciliationFixture{service: svc, ledger: ledger, db: db, task: task}
}

func (f submissionReconciliationFixture) claim(t *testing.T) *model.Task {
	t.Helper()
	claimed, err := f.service.repo.ClaimNextSubmissionUncertainTask("manual-recovery:test", providerSubmissionReconcileLease, time.Now().Add(-providerSubmissionReconcileGrace))
	if err != nil {
		t.Fatal(err)
	}
	return claimed
}

// logLines 在断言失败时把任务日志带出来：对账的中间结论都写在日志里，
// 只报"状态不对"会让失败原因无从查起。
func (f submissionReconciliationFixture) logLines(t *testing.T) []string {
	t.Helper()
	var logs []model.TaskLog
	if err := f.db.Where("task_id = ?", f.task.ID).Order("created_at asc").Find(&logs).Error; err != nil {
		return []string{"读取任务日志失败：" + err.Error()}
	}
	lines := make([]string, 0, len(logs))
	for _, item := range logs {
		lines = append(lines, item.Level+" "+item.Message+" "+item.Payload)
	}
	return lines
}

func (f submissionReconciliationFixture) reload(t *testing.T) model.Task {
	t.Helper()
	var latest model.Task
	if err := f.db.First(&latest, "id = ?", f.task.ID).Error; err != nil {
		t.Fatal(err)
	}
	return latest
}

// 上游任务号始终捞不到：过了兜底期限就按"无产出"退预扣，并留下结清标记，
// 否则下一轮扫描会把同一条任务反复捞起来。
func TestSubmissionReconciliationRefundsWhenProviderTaskIsUnrecoverable(t *testing.T) {
	fixture := newSubmissionReconciliationFixture(t, 25*time.Hour)
	claimed := fixture.claim(t)
	if claimed == nil {
		t.Fatal("超过兜底期限的任务应当被领取")
	}
	if err := fixture.service.reconcileSubmissionUncertainTask(t.Context(), claimed); err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if len(fixture.ledger.refunds) != 1 || !strings.Contains(fixture.ledger.refunds[0], unreconciledSubmissionRefundNote) {
		t.Fatalf("应按无产出退回预扣，实际 %v", fixture.ledger.refunds)
	}
	latest := fixture.reload(t)
	if latest.Stage != "任务失败" || latest.Error != unreconciledSubmissionMessage {
		t.Fatalf("结清后应落在普通失败态，实际 stage=%q error=%q", latest.Stage, latest.Error)
	}
	if latest.PollStage != submissionReconciledPollStage || latest.LeaseOwner != "" || latest.NextPollAt != nil {
		t.Fatalf("结清后应停止对账并释放租约，实际 pollStage=%q lease=%q nextPollAt=%v", latest.PollStage, latest.LeaseOwner, latest.NextPollAt)
	}
	if again := fixture.claim(t); again != nil {
		t.Fatalf("已结清的任务不应再次被领取: %#v", again)
	}
}

// 兜底期限之前不退钱：上游可能还在跑，也可能下一秒就出片。
func TestSubmissionReconciliationWaitsUntilRefundDeadline(t *testing.T) {
	fixture := newSubmissionReconciliationFixture(t, 3*time.Hour)
	claimed := fixture.claim(t)
	if claimed == nil {
		t.Fatal("失败后过了宽限期的任务应当被领取")
	}
	if err := fixture.service.reconcileSubmissionUncertainTask(t.Context(), claimed); err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if len(fixture.ledger.refunds) != 0 {
		t.Fatalf("兜底期限前不应退款，实际 %v", fixture.ledger.refunds)
	}
	latest := fixture.reload(t)
	if latest.Stage != "submission_unknown" || latest.NextPollAt == nil {
		t.Fatalf("应保留原阶段并排下一轮，实际 stage=%q nextPollAt=%v", latest.Stage, latest.NextPollAt)
	}
	if !latest.NextPollAt.After(time.Now().Add(providerSubmissionReconcileInterval - time.Minute)) {
		t.Fatalf("下一轮应排在一个对账间隔之后，实际 %v", latest.NextPollAt)
	}
}

// 能把上游任务号捞回来时，定性交给上游：上游说这次生成没有产出就退预扣。
func TestSubmissionReconciliationRefundsWhenUpstreamReportsNoOutput(t *testing.T) {
	allowLoopbackProviderTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"vid-1","status":"failed","error":{"message":"内容审核未通过"}}}`))
	}))
	defer server.Close()

	fixture := newSubmissionReconciliationFixture(t, 3*time.Hour)
	fixture.attachProviderTask(t, server.URL, "vid-1")
	claimed := fixture.claim(t)
	if claimed == nil {
		t.Fatal("任务应当被领取")
	}
	if err := fixture.service.reconcileSubmissionUncertainTask(t.Context(), claimed); err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if len(fixture.ledger.refunds) != 1 || !strings.Contains(fixture.ledger.refunds[0], providerNoOutputSubmissionNote) {
		t.Fatalf("上游确认无产出时应退回预扣，实际 %v", fixture.ledger.refunds)
	}
	if latest := fixture.reload(t); latest.Stage != "任务失败" || latest.PollStage != submissionReconciledPollStage {
		t.Fatalf("结清后应落在普通失败态，实际 stage=%q pollStage=%q", latest.Stage, latest.PollStage)
	}
}

// 上游还在跑就不动账：这条任务还有产出，退了等于白送。
func TestSubmissionReconciliationKeepsWaitingWhileUpstreamRuns(t *testing.T) {
	allowLoopbackProviderTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"vid-1","status":"running"}}`))
	}))
	defer server.Close()

	fixture := newSubmissionReconciliationFixture(t, 3*time.Hour)
	fixture.attachProviderTask(t, server.URL, "vid-1")
	claimed := fixture.claim(t)
	if claimed == nil {
		t.Fatal("任务应当被领取")
	}
	if err := fixture.service.reconcileSubmissionUncertainTask(t.Context(), claimed); err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if len(fixture.ledger.refunds) != 0 {
		t.Fatalf("上游仍在处理时不应退款，实际 %v", fixture.ledger.refunds)
	}
	latest := fixture.reload(t)
	if latest.Stage != "submission_unknown" || latest.NextPollAt == nil || latest.PollStage == submissionReconciledPollStage {
		t.Fatalf("应保留原阶段并排下一轮，实际 stage=%q pollStage=%q nextPollAt=%v", latest.Stage, latest.PollStage, latest.NextPollAt)
	}
}

// 领取条件要卡在"失败 + 提交结果未确认 + 生成类 + 过了宽限期"上，否则要么漏对账，
// 要么把还在等回执的新任务和根本不会调上游的文本任务一起卷进来。
func TestSubmissionReconciliationClaimFiltersCandidates(t *testing.T) {
	fixture := newSubmissionReconciliationFixture(t, 3*time.Hour)
	fixture.deferFixtureTask(t)
	if claimed := fixture.claim(t); claimed != nil {
		t.Fatalf("排到未来的任务不应被领取: %#v", claimed)
	}

	fresh := fixture.task
	fresh.ID, fresh.Stage, fresh.CreatedAt = "task-fresh", "submission_unknown", time.Now()
	if err := fixture.db.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	text := fixture.task
	text.ID, text.Type, text.CreatedAt = "task-text", "canvas_text", time.Now().Add(-3*time.Hour)
	if err := fixture.db.Create(&text).Error; err != nil {
		t.Fatal(err)
	}
	if claimed := fixture.claim(t); claimed != nil {
		t.Fatalf("只应领取过了宽限期的生成任务: %#v", claimed)
	}
}

func TestSubmissionReconciliationClaimAcceptsNonCanvasVideoTask(t *testing.T) {
	fixture := newSubmissionReconciliationFixture(t, 3*time.Hour)
	fixture.deferFixtureTask(t)
	task := fixture.task
	task.ID, task.Type = "task-video-legacy", "video_generation"
	if err := fixture.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	claimed := fixture.claim(t)
	if claimed == nil || claimed.ID != task.ID {
		t.Fatalf("旧前缀视频任务也应参与对账，实际 %#v", claimed)
	}
}

// deferFixtureTask 把夹具自带的那条任务排到未来，让用例可以只考察后加进来的候选。
func (f submissionReconciliationFixture) deferFixtureTask(t *testing.T) {
	t.Helper()
	if err := f.db.Model(&model.Task{}).Where("id = ?", f.task.ID).
		Updates(map[string]any{"next_poll_at": time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
}

// attachProviderTask 让任务带上可查询的上游任务号，并把上游地址指到用例服务器。
func (f submissionReconciliationFixture) attachProviderTask(t *testing.T, baseURL string, providerRequestID string) {
	t.Helper()
	previous := beefAPIVideoBaseURLForTest
	beefAPIVideoBaseURLForTest = baseURL
	t.Cleanup(func() { beefAPIVideoBaseURLForTest = previous })

	input := canvasGenerationInput{
		Mode:   "video",
		Prompt: "make it move",
		Config: providerConfig{
			BaseURL: baseURL, APIKey: "test-key", Model: "seedance-2.5",
			InterfaceType: string(model.ChannelInterfaceNewAPIVideo), Size: "16:9",
			VideoSeconds: "5", VQuality: "720p",
		},
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Task{}).Where("id = ?", f.task.ID).Updates(map[string]any{
		"input_json": encoded, "provider_request_id": providerRequestID,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// 上游其实出了片：自动对账要把产物接回来，而不是把它当成"没有结果"退钱。
func TestSubmissionReconciliationRestoresOutputWhenUpstreamCompleted(t *testing.T) {
	allowLoopbackProviderTest(t)
	clip := syntheticVideoMP4(1280, 720, 3200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(clip)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"vid-1","status":"completed"}}`))
	}))
	defer server.Close()

	fixture := newSubmissionReconciliationFixture(t, 3*time.Hour)
	if err := fixture.db.AutoMigrate(&model.Resource{}, &model.UserDailyUploadUsage{}, &model.Asset{}, &model.AssetFolder{}, &model.CanvasProject{}, &model.TaskLog{}, &model.Result{}, &model.ApiCallLog{}, &model.TaskTextDelta{}); err != nil {
		t.Fatal(err)
	}
	fixture.attachProviderTask(t, server.URL, "vid-1")
	claimed := fixture.claim(t)
	if claimed == nil {
		t.Fatal("任务应当被领取")
	}
	if err := fixture.service.reconcileSubmissionUncertainTask(t.Context(), claimed); err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if len(fixture.ledger.refunds) != 0 {
		t.Fatalf("上游出了片不应退款，实际 %v", fixture.ledger.refunds)
	}
	latest := fixture.reload(t)
	if latest.Status != model.TaskStatusSucceeded || latest.ResultJSON == "" {
		t.Fatalf("应恢复成成功任务并落库产物，实际 status=%q result=%q error=%q 日志=%v", latest.Status, latest.ResultJSON, latest.Error, fixture.logLines(t))
	}
	if latest.PollStage == submissionReconciledPollStage {
		t.Fatalf("恢复成功不是「无产出结清」，不应打结清标记")
	}
}

// 上游任务还在、但长期不出结果：自动对账到此为止并转人工，而且不能顺手放行重试——
// 上游那条任务还活着，用户手一抖重发就是又花一笔上游的钱。
func TestSubmissionReconciliationHandsOffLongRunningUpstreamTask(t *testing.T) {
	allowLoopbackProviderTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"id":"vid-1","status":"running"}}`))
	}))
	defer server.Close()

	fixture := newSubmissionReconciliationFixture(t, 8*24*time.Hour)
	fixture.attachProviderTask(t, server.URL, "vid-1")
	claimed := fixture.claim(t)
	if claimed == nil {
		t.Fatal("任务应当被领取")
	}
	if err := fixture.service.reconcileSubmissionUncertainTask(t.Context(), claimed); err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if len(fixture.ledger.refunds) != 0 {
		t.Fatalf("上游任务还活着，不应退款，实际 %v", fixture.ledger.refunds)
	}
	latest := fixture.reload(t)
	if latest.Stage != "submission_unknown" || latest.PollStage != submissionReconciledPollStage {
		t.Fatalf("应交人工并停掉自动对账，实际 stage=%q pollStage=%q", latest.Stage, latest.PollStage)
	}
	if !persistedFailureBlocksRetry(latest.Error, latest.Stage) {
		t.Fatal("上游任务仍在跑，重试必须继续被挡住")
	}
	if again := fixture.claim(t); again != nil {
		t.Fatalf("已转人工的任务不应再由自动对账领取: %#v", again)
	}
}

// 结清之后任务落回普通失败态，用户看到的文案不能再是"上游可能仍在执行、不要重新提交"：
// 这时候平台已经查证过没有产出，钱也退了，那句提示既误导又堵着重试入口。
func TestSubmissionReconciliationSettledCopyLeavesUncertainState(t *testing.T) {
	for _, message := range []string{unreconciledSubmissionMessage, providerNoOutputSubmissionMessage} {
		shown := generation.ClassifyText(message).UserMessage()
		if strings.Contains(shown, "不要立即重新提交") || strings.Contains(shown, "上游任务可能仍在执行") {
			t.Fatalf("结清文案仍被归类成提交未确认：%q → %q", message, shown)
		}
		if persistedFailureErrorCode(message, "任务失败") == string(generation.CategorySubmissionUncertain) {
			t.Fatalf("结清文案的错误码仍是提交未确认：%q", message)
		}
	}
}
