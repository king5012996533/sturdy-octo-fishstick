package repository

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"infinite-canvas/backend/internal/model"
)

func newAgentCreditTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:agent-credit-%d?mode=memory&cache=shared&_busy_timeout=5000", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(4)
	}
	if err := db.AutoMigrate(
		&model.AgentGrant{},
		&model.AgentCreditReservation{},
		&model.AgentCreditBucket{},
		&model.AgentBudgetRequest{},
	); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func agentCreditTestGrant(t *testing.T, db *gorm.DB, id string, runLimit, dayLimit int64) *model.AgentGrant {
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

func agentCreditBucketUsed(t *testing.T, db *gorm.DB, scope, key, period string) int64 {
	t.Helper()
	var bucket model.AgentCreditBucket
	if err := db.First(&bucket, "scope = ? AND scope_key = ? AND period = ?", scope, key, period).Error; err != nil {
		t.Fatal(err)
	}
	return bucket.ReservedCredits
}

func TestReserveAgentCreditsIsAtomicAndIdempotent(t *testing.T) {
	repo, db := newAgentCreditTestRepository(t)
	grant := agentCreditTestGrant(t, db, "grant-a", 1000, 2000)
	reserve := func(task string, credits int64) error {
		return repo.ReserveAgentCredits(AgentCreditReserve{
			UserID: "user", GrantID: grant.ID, GrantVersion: grant.Version,
			RunID: "run-a", TaskID: task, Credits: credits,
			MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
		})
	}

	if err := reserve("task-1", 900); err != nil {
		t.Fatal(err)
	}
	// 重放同一个任务：TaskID 唯一索引命中，额度不能再被占一次。
	if err := reserve("task-1", 900); err != nil {
		t.Fatal(err)
	}
	if used := agentCreditBucketUsed(t, db, model.AgentCreditScopeRun, "run-a", "all"); used != 900 {
		t.Fatalf("run bucket = %d, want 900", used)
	}
	if err := reserve("task-2", 101); !errors.Is(err, ErrAgentBudgetExceeded) {
		t.Fatalf("over-budget reserve err = %v, want ErrAgentBudgetExceeded", err)
	}
	if used := agentCreditBucketUsed(t, db, model.AgentCreditScopeRun, "run-a", "all"); used != 900 {
		t.Fatalf("rejected reserve changed bucket to %d", used)
	}
	if err := reserve("task-3", 100); err != nil {
		t.Fatalf("exact run limit should still fit: %v", err)
	}
}

func TestAgentDailyLimitIsSharedAcrossConcurrentRunsAndGrants(t *testing.T) {
	repo, db := newAgentCreditTestRepository(t)
	grantA := agentCreditTestGrant(t, db, "grant-a", 1000, 1000)
	grantB := agentCreditTestGrant(t, db, "grant-b", 1000, 1000)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for index, item := range []struct {
		grant   *model.AgentGrant
		run     string
		task    string
		credits int64
	}{
		{grantA, "run-a", "task-a", 600},
		{grantB, "run-b", "task-b", 600},
	} {
		wg.Add(1)
		go func(index int, grant *model.AgentGrant, run, task string, credits int64) {
			defer wg.Done()
			errs[index] = repo.ReserveAgentCredits(AgentCreditReserve{
				UserID: "user", GrantID: grant.ID, GrantVersion: grant.Version,
				RunID: run, TaskID: task, Credits: credits,
				MaxCreditsPerRun: 1000, MaxCreditsPerDay: 1000,
			})
		}(index, item.grant, item.run, item.task, item.credits)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrAgentBudgetExceeded):
		default:
			t.Fatalf("unexpected reserve error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded = %d, want exactly 1 under a shared daily cap", succeeded)
	}
	day := model.AgentCreditDay(time.Now().UTC())
	if used := agentCreditBucketUsed(t, db, model.AgentCreditScopeDaily, "user", day); used != 600 {
		t.Fatalf("daily bucket = %d, want 600 (at most one run)", used)
	}
}

func TestRevokedAndStaleGrantsAreRejected(t *testing.T) {
	repo, db := newAgentCreditTestRepository(t)
	revoked := agentCreditTestGrant(t, db, "grant-revoked", 1000, 2000)
	base := AgentCreditReserve{
		UserID: "user", GrantID: revoked.ID, GrantVersion: revoked.Version,
		RunID: "run-a", TaskID: "task-a", Credits: 10,
		MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
	}
	if err := repo.ReserveAgentCredits(base); err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeAgentGrant("user", revoked.ID); err != nil {
		t.Fatal(err)
	}
	next := base
	next.TaskID = "task-b"
	if err := repo.ReserveAgentCredits(next); !errors.Is(err, ErrAgentGrantRevoked) {
		t.Fatalf("revoked grant err = %v, want ErrAgentGrantRevoked", err)
	}

	stale := agentCreditTestGrant(t, db, "grant-stale", 1000, 2000)
	if err := db.Model(&model.AgentGrant{}).Where("id = ?", stale.ID).Update("version", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.ReserveAgentCredits(AgentCreditReserve{
		UserID: "user", GrantID: stale.ID, GrantVersion: 1,
		RunID: "run-c", TaskID: "task-c", Credits: 10,
		MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
	}); !errors.Is(err, ErrAgentGrantStale) {
		t.Fatalf("stale grant err = %v, want ErrAgentGrantStale", err)
	}
}

func TestExpiredReservationReleasesButChargedOneDoesNot(t *testing.T) {
	repo, db := newAgentCreditTestRepository(t)
	grant := agentCreditTestGrant(t, db, "grant-a", 1000, 2000)
	base := AgentCreditReserve{
		UserID: "user", GrantID: grant.ID, GrantVersion: grant.Version,
		RunID: "run-a", Credits: 500,
		MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
	}
	first := base
	first.TaskID = "task-1"
	if err := repo.ReserveAgentCredits(first); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AgentCreditReservation{}).Where("task_id = ?", "task-1").
		Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	second := base
	second.TaskID = "task-2"
	second.Credits = 600
	if err := repo.ReserveAgentCredits(second); err != nil {
		t.Fatalf("expired reservation should be reclaimed: %v", err)
	}

	charged := base
	charged.RunID = "run-b"
	charged.TaskID = "task-3"
	charged.Credits = 500
	if err := repo.ReserveAgentCredits(charged); err != nil {
		t.Fatal(err)
	}
	if err := repo.SettleAgentReservation("user", "task-3", 500); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AgentCreditReservation{}).Where("task_id = ?", "task-3").
		Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	third := base
	third.RunID = "run-b"
	third.TaskID = "task-4"
	third.Credits = 600
	if err := repo.ReserveAgentCredits(third); !errors.Is(err, ErrAgentBudgetExceeded) {
		t.Fatalf("charged reservation must keep occupying budget, err = %v", err)
	}
}

// 文本任务的 token 成本只有结算时才知道：预扣起步价后，补扣必须把额度桶抬到
// 实际总价，否则一段长对话仍按 1 积分占额度，maxCredits 形同虚设。
func TestTextSupplementRaisesAgentBudgetAndBlocksNextTask(t *testing.T) {
	repo, db := newAgentCreditTestRepository(t)
	grant := agentCreditTestGrant(t, db, "grant-a", 1000, 2000)
	base := AgentCreditReserve{
		UserID: "user", GrantID: grant.ID, GrantVersion: grant.Version,
		RunID: "run-a", TaskID: "task-1", Credits: 1,
		MaxCreditsPerRun: 1000, MaxCreditsPerDay: 2000,
	}
	if err := repo.ReserveAgentCredits(base); err != nil {
		t.Fatal(err)
	}
	if err := repo.SettleAgentReservation("user", "task-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := repo.SettleAgentReservation("user", "task-1", 30); err != nil {
		t.Fatal(err)
	}
	if used := agentCreditBucketUsed(t, db, model.AgentCreditScopeRun, "run-a", "all"); used != 30 {
		t.Fatalf("run bucket after text supplement = %d, want 30", used)
	}
	var reservation model.AgentCreditReservation
	if err := db.First(&reservation, "task_id = ?", "task-1").Error; err != nil {
		t.Fatal(err)
	}
	if reservation.ReservedCredits != 30 || reservation.SettledCredits != 30 || reservation.Status != model.AgentReservationCharged {
		t.Fatalf("reservation after supplement = %+v", reservation)
	}

	next := base
	next.TaskID, next.Credits = "task-2", 971
	if err := repo.ReserveAgentCredits(next); !errors.Is(err, ErrAgentBudgetExceeded) {
		t.Fatalf("next task above remaining run budget err = %v, want ErrAgentBudgetExceeded", err)
	}
	next.TaskID, next.Credits = "task-3", 970
	if err := repo.ReserveAgentCredits(next); err != nil {
		t.Fatalf("exact remaining run budget should fit: %v", err)
	}
}
