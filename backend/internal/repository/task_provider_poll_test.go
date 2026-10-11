package repository

import (
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDeferredProviderPollKeepsOriginalTaskIdentityWithoutImmediateReclaim(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:provider-poll-defer?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := model.Task{
		ID: "task-1", UserID: "user-1", Type: "canvas_video", Status: model.TaskStatusRunning,
		LeaseOwner: "worker-1", LeaseExpiresAt: ptrTime(time.Now().Add(time.Minute)), ProviderRequestID: "provider-task-1",
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	if err := repo.DeferRunningTaskForProviderPoll(task.ID, task.LeaseOwner, "后台仍在生成", time.Minute); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimNextTask("worker-2", 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed != nil {
		t.Fatalf("task reclaimed before next poll: %#v", claimed)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Update("next_poll_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.ClaimNextTask("worker-2", 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != task.ID || claimed.ProviderRequestID != task.ProviderRequestID {
		t.Fatalf("reclaimed task = %#v", claimed)
	}
}

func TestUpdateTaskProviderProgressIsMonotonic(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:provider-progress?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "task-progress", UserID: "user-1", Type: "canvas_video", Status: model.TaskStatusRunning, Stage: "正在连接上游"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	for _, progress := range []int{12, 48, 31, 76} {
		if err := repo.UpdateTaskProviderProgress(task.ID, progress, ""); err != nil {
			t.Fatal(err)
		}
	}
	var stored model.Task
	if err := db.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Progress != 76 || stored.Stage != "上游生成中" {
		t.Fatalf("stored progress = %d, stage = %q", stored.Progress, stored.Stage)
	}
}

// 上游报满进度时结果往往还没取回。运行中的任务不能显示 100%，否则界面停在
// "100% + 还在转"，用户只能理解为卡死（线上真实反馈）。
func TestUpdateTaskProviderProgressKeepsRunningTaskBelowComplete(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:provider-progress-ceiling?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "task-ceiling", UserID: "user-1", Type: "canvas_video", Status: model.TaskStatusRunning, Stage: "正在连接上游"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	if err := repo.UpdateTaskProviderProgress(task.ID, 100, "上游已生成，正在取回结果"); err != nil {
		t.Fatal(err)
	}
	var stored model.Task
	if err := db.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Progress != 95 || stored.Stage != "上游已生成，正在取回结果" {
		t.Fatalf("stored progress = %d, stage = %q", stored.Progress, stored.Stage)
	}
	if stored.Status != model.TaskStatusRunning {
		t.Fatalf("status = %q", stored.Status)
	}
}

// 长时间没有结果的运行中任务只换文案：进度不能被这段话术带跑，状态也不能被改成终态。
func TestUpdateTaskRunningStageKeepsProgressAndStatus(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:task-running-stage?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "task-stale", UserID: "user-1", Type: "canvas_video", Status: model.TaskStatusRunning, Stage: "上游生成中", Progress: 40}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	if err := repo.UpdateTaskRunningStage(task.ID, "上游长时间未返回结果，可继续等待或取消后重新提交"); err != nil {
		t.Fatal(err)
	}
	var stored model.Task
	if err := db.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Stage != "上游长时间未返回结果，可继续等待或取消后重新提交" || stored.Progress != 40 {
		t.Fatalf("stored stage = %q, progress = %d", stored.Stage, stored.Progress)
	}
	if stored.Status != model.TaskStatusRunning {
		t.Fatalf("status = %q, want running", stored.Status)
	}
	// 已结束的任务不该被这条文案改写：清理和退款路径都靠终态判定。
	if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTaskRunningStage(task.ID, "不应写入"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&stored, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Stage == "不应写入" {
		t.Fatalf("stage = %q, want 已结束任务的文案保持不变", stored.Stage)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
