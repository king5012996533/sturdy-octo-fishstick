package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestAutoPermissionRequiresBudgetAndWhitelistsOnlyIrreversibleTools(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	createAgentBudgetTestCanvas(t, db)

	noBudget := agentTestRequest()
	noBudget.PermissionMode = "auto"
	noBudget.IdempotencyKey = "auto-without-budget"
	run, err := s.CreateCloudAgentRun("user", noBudget, "")
	if err != nil {
		t.Fatal(err)
	}
	if run.PermissionMode != "request_approval" {
		t.Fatalf("auto without budget = %q, want request_approval", run.PermissionMode)
	}

	withBudget := agentTestRequest()
	withBudget.PermissionMode = "auto"
	withBudget.Budget.MaxCredits = 1000
	withBudget.IdempotencyKey = "auto-with-budget"
	run, err = s.CreateCloudAgentRun("user", withBudget, "")
	if err != nil {
		t.Fatal(err)
	}
	if run.PermissionMode != "auto" {
		t.Fatalf("auto with budget = %q, want auto", run.PermissionMode)
	}
	for _, name := range []string{"canvas_apply_ops", "generate_media"} {
		if cloudAgentNeedsApproval("auto", name) {
			t.Fatalf("%s inside an authorized budget must run without per-call approval", name)
		}
	}
	for _, name := range []string{"canvas_delete_project", "canvas_history_restore", "publish"} {
		if !cloudAgentNeedsApproval("auto", name) {
			t.Fatalf("%s is irreversible and must still require approval in auto mode", name)
		}
	}
}

func TestThirtyShotsStayInsideOneRunBudgetWithoutApproval(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	ledger := &fakeCreditLedger{charges: 30}
	s.UseTaskCreditLedger(ledger)
	grant := createAgentBudgetTestGrant(t, db, "grant-thirty", 1000, 2000)

	admission := &taskAdmission{credit: &agentTaskCredit{
		RunID: "run-thirty", GrantID: grant.ID, GrantVersion: grant.Version,
		MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
	}}
	for index := 0; index < 30; index++ {
		task := &model.Task{ID: fmt.Sprintf("shot-%02d", index), UserID: "user", Type: "canvas_image"}
		if err := s.chargeTaskCreditsWithCredit(task, map[string]any{"mode": "image"}, admission); err != nil {
			t.Fatalf("shot %d must fit in the run budget: %v", index, err)
		}
	}
	var reservationCount int64
	if err := db.Model(&model.AgentCreditReservation{}).Where("run_id = ?", "run-thirty").Count(&reservationCount).Error; err != nil {
		t.Fatal(err)
	}
	if reservationCount != 30 {
		t.Fatalf("reservations = %d, want 30", reservationCount)
	}

	// 30 × 30 = 900；再来一笔 101 就会越过 1000，必须停在这里。
	ledger.charges = 101
	over := &model.Task{ID: "shot-over", UserID: "user", Type: "canvas_image"}
	err := s.chargeTaskCreditsWithCredit(over, map[string]any{"mode": "image"}, admission)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 402 || appErr.Reason != "agent_budget_exceeded" {
		t.Fatalf("over-budget err = %#v, want 402 agent_budget_exceeded", err)
	}
}

func TestAgentRunBreakerStopsAfterThreeConsecutiveToolFailures(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	createAgentBudgetTestCanvas(t, db)
	req := agentTestRequest()
	req.PermissionMode = "request_approval"
	req.IdempotencyKey = "breaker-run"
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	state.ConsecutiveFailures = cloudAgentFailureBreakerThreshold
	if err := s.repo.MutateCloudAgent("user", run.ID, execution.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	execution, err = s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.advanceCloudAgent(execution); err != nil {
		t.Fatal(err)
	}
	execution, err = s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != "failed" || !strings.Contains(execution.FailureMessage, "熔断") {
		t.Fatalf("wanted breaker failure, got status=%q message=%q", execution.Status, execution.FailureMessage)
	}
	if len(state.Events) == 0 || state.Events[len(state.Events)-1].Type != "run_failed" || !strings.Contains(stringValue(state.Events[len(state.Events)-1].Payload["text"]), "熔断") {
		t.Fatalf("missing run_failed breaker event: %+v", state.Events)
	}
}

func TestAgentBudgetRequestNeedsUserApprovalAndVersionInvalidatesOldRun(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	createAgentBudgetTestCanvas(t, db)
	grant := createAgentBudgetTestGrant(t, db, "grant-approval", 1000, 2000)
	token := &model.MCPToken{
		ID: "token-approval", UserID: "user", Name: "Codex", TokenHash: strings.Repeat("a", 64),
		Prefix: "ktv_mcp_test", GrantID: grant.ID, ScopesJSON: `["agent","canvas"]`,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.MCPTokenID = token.ID
	req.IdempotencyKey = "budget-approval-run"
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	if state.Credit.GrantID != grant.ID || state.Credit.GrantVersion != 1 {
		t.Fatalf("run credit snapshot = %+v, want grant %s v1", state.Credit, grant.ID)
	}

	request, err := s.RequestCloudAgentBudget("user", run.ID, 2000, "30 个镜头需要更多额度")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.repo.AgentGrant("user", grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MaxCreditsPerRun != 1000 || stored.Version != 1 {
		t.Fatalf("agent request must not change the grant: max=%d version=%d", stored.MaxCreditsPerRun, stored.Version)
	}

	updated, err := s.ApproveCloudAgentBudget("user", request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MaxCreditsPerRun != 2000 || updated.MaxCreditsPerDay != 3000 || updated.Version != 2 {
		t.Fatalf(
			"approved grant = run %d day %d v%d, want 2000/3000 v2",
			updated.MaxCreditsPerRun, updated.MaxCreditsPerDay, updated.Version,
		)
	}

	// 旧 Run 冻结的是 v1；用户批准后它不能继续用旧边界。
	err = s.repo.ReserveAgentCredits(repository.AgentCreditReserve{
		UserID: "user", GrantID: grant.ID, GrantVersion: 1,
		RunID: run.ID, TaskID: "old-run-task", Credits: 100,
		MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
	})
	if !errors.Is(err, repository.ErrAgentGrantStale) {
		t.Fatalf("old run reservation err = %v, want ErrAgentGrantStale", err)
	}
}

func TestAgentReservationIsReleasedWhenChargeFailsOrIsRefunded(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	grant := createAgentBudgetTestGrant(t, db, "grant-release", 1000, 2000)
	ledger := &fakeCreditLedger{charges: 100}
	s.UseTaskCreditLedger(ledger)
	admission := &taskAdmission{credit: &agentTaskCredit{
		RunID: "run-release", GrantID: grant.ID, GrantVersion: grant.Version,
		MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
	}}

	ledger.chargeErr = errors.New("积分账户扣款失败")
	failed := &model.Task{ID: "task-charge-failed", UserID: "user", Type: "canvas_image"}
	if err := s.chargeTaskCreditsWithCredit(failed, map[string]any{"mode": "image"}, admission); err == nil {
		t.Fatal("charge failure must surface")
	}
	reservation := agentBudgetTestReservation(t, db, failed.ID)
	if reservation.Status != model.AgentReservationReleased {
		t.Fatalf("reservation after failed charge = %q, want released", reservation.Status)
	}
	if used := agentBudgetTestBucket(t, db, model.AgentCreditScopeRun, "run-release", "all"); used != 0 {
		t.Fatalf("run bucket after failed charge = %d, want 0", used)
	}

	ledger.chargeErr = nil
	ledger.refunded = true
	ledger.refundCredits = 100
	charged := &model.Task{ID: "task-refunded", UserID: "user", Type: "canvas_image"}
	if err := s.chargeTaskCreditsWithCredit(charged, map[string]any{"mode": "image"}, admission); err != nil {
		t.Fatal(err)
	}
	if used := agentBudgetTestBucket(t, db, model.AgentCreditScopeRun, "run-release", "all"); used != 100 {
		t.Fatalf("run bucket after charge = %d, want 100", used)
	}
	s.refundTaskCreditsUnchecked(charged, "任务失败退回预扣")
	reservation = agentBudgetTestReservation(t, db, charged.ID)
	if reservation.Status != model.AgentReservationReleased {
		t.Fatalf("reservation after refund = %q, want released", reservation.Status)
	}
	if used := agentBudgetTestBucket(t, db, model.AgentCreditScopeRun, "run-release", "all"); used != 0 {
		t.Fatalf("run bucket after refund = %d, want 0", used)
	}
}

// 用户取消一个"请求还没发出去"的任务时，预扣会退回，Agent 额度预留必须同时释放；
// 否则一次取消就会把 Run 与当日额度永久占住。
func TestCancelledTaskReleasesAgentReservation(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	grant := createAgentBudgetTestGrant(t, db, "grant-cancel", 1000, 2000)
	ledger := &fakeCreditLedger{charges: 120, refunded: true, refundCredits: 120}
	s.UseTaskCreditLedger(ledger)

	task := &model.Task{
		ID:     "task-cancel",
		UserID: "user",
		Type:   "canvas_image",
		Status: model.TaskStatusQueued,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	admission := &taskAdmission{credit: &agentTaskCredit{
		RunID: "run-cancel", GrantID: grant.ID, GrantVersion: grant.Version,
		MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
	}}
	if err := s.chargeTaskCreditsWithCredit(task, map[string]any{"mode": "image"}, admission); err != nil {
		t.Fatalf("charge before cancel: %v", err)
	}
	day := model.AgentCreditDay(time.Now().UTC())
	if used := agentBudgetTestBucket(t, db, model.AgentCreditScopeRun, "run-cancel", "all"); used != 120 {
		t.Fatalf("run bucket after charge = %d, want 120", used)
	}

	cancelled, err := s.CancelTask(context.Background(), task.UserID, task.ID)
	if err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	if cancelled.Status != model.TaskStatusCancelled {
		t.Fatalf("CancelTask() status = %s, want cancelled", cancelled.Status)
	}
	reservation := agentBudgetTestReservation(t, db, task.ID)
	if reservation.Status != model.AgentReservationReleased {
		t.Fatalf("reservation after cancel = %q, want released", reservation.Status)
	}
	if used := agentBudgetTestBucket(t, db, model.AgentCreditScopeRun, "run-cancel", "all"); used != 0 {
		t.Fatalf("run bucket after cancel = %d, want 0", used)
	}
	if used := agentBudgetTestBucket(t, db, model.AgentCreditScopeDaily, "user", day); used != 0 {
		t.Fatalf("daily bucket after cancel = %d, want 0", used)
	}
}

func createAgentBudgetTestGrant(t *testing.T, db *gorm.DB, id string, runLimit, dayLimit int64) *model.AgentGrant {
	t.Helper()
	grant := &model.AgentGrant{
		ID: id, UserID: "user", Client: model.AgentGrantClientMCP,
		ScopesJSON: `["agent","canvas"]`, PermissionMode: "auto",
		MaxCreditsPerRun: runLimit, MaxCreditsPerDay: dayLimit,
		Status: model.AgentGrantStatusActive, Version: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := db.Create(grant).Error; err != nil {
		t.Fatal(err)
	}
	return grant
}

func createAgentBudgetTestCanvas(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.CanvasProject{
		ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[],"connections":[]}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func agentBudgetTestReservation(t *testing.T, db *gorm.DB, taskID string) model.AgentCreditReservation {
	t.Helper()
	var reservation model.AgentCreditReservation
	if err := db.First(&reservation, "task_id = ?", taskID).Error; err != nil {
		t.Fatal(err)
	}
	return reservation
}

func agentBudgetTestBucket(t *testing.T, db *gorm.DB, scope, key, period string) int64 {
	t.Helper()
	var bucket model.AgentCreditBucket
	if err := db.First(&bucket, "scope = ? AND scope_key = ? AND period = ?", scope, key, period).Error; err != nil {
		t.Fatal(err)
	}
	return bucket.ReservedCredits
}
