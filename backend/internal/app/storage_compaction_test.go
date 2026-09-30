package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestCompactInlineTaskMediaMovesInlineVideoIntoResources(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(database.Config{Driver: "sqlite", DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateLocalSchema(db); err != nil {
		t.Fatal(err)
	}
	clip := syntheticVideoMP4(1280, 720, 5042)
	inline := "data:binary/octet-stream;base64," + base64.StdEncoding.EncodeToString(clip)
	resultJSON, err := json.Marshal(map[string]interface{}{
		"mode":  "video",
		"video": map[string]interface{}{"dataUrl": inline, "mimeType": "binary/octet-stream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&model.Task{
		ID:         "inline-video-task",
		UserID:     "user-1",
		Status:     model.TaskStatusSucceeded,
		Type:       "canvas_video",
		Prompt:     "生成视频",
		ResultJSON: string(resultJSON),
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewLocal(repository.New(db), dataDir)
	summary, err := svc.CompactInlineTaskMedia()
	if err != nil {
		t.Fatal(err)
	}
	if summary.Scanned != 1 || summary.Compacted != 1 || summary.ReleasedBytes <= 0 || len(summary.Failed) != 0 {
		t.Fatalf("压缩汇总 = %#v", summary)
	}

	task, err := svc.repo.TaskForUser("user-1", "inline-video-task")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(task.ResultJSON, "base64") {
		t.Fatalf("任务结果仍然内联媒体：%s", task.ResultJSON[:200])
	}
	if !strings.Contains(task.ResultJSON, `"storageKey":"resource:`) {
		t.Fatalf("任务结果没有资源引用：%s", task.ResultJSON)
	}
	if task.Prompt != "生成视频" || task.Status != model.TaskStatusSucceeded {
		t.Fatalf("维护任务改动了其他列：%#v", task)
	}
	var stored map[string]interface{}
	if err := json.Unmarshal([]byte(task.ResultJSON), &stored); err != nil {
		t.Fatal(err)
	}
	video, _ := stored["video"].(map[string]interface{})
	resourceID := strings.TrimPrefix(stringField(video, "storageKey"), "resource:")
	resource, err := svc.repo.ResourceForUser("user-1", resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Kind != "video" || resource.MimeType != "video/mp4" {
		t.Fatalf("生成资源 = %#v", resource)
	}

	// 二次运行必须幂等：已经没有内联媒体的任务不再被扫描。
	second, err := svc.CompactInlineTaskMedia()
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned != 0 || second.Compacted != 0 {
		t.Fatalf("二次压缩汇总 = %#v", second)
	}
}

func TestCompactInlineTaskMediaReportsUnparsableResult(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(database.Config{Driver: "sqlite", DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateLocalSchema(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&model.Task{
		ID:         "broken-result-task",
		UserID:     "user-1",
		Status:     model.TaskStatusFailed,
		Type:       "canvas_video",
		ResultJSON: `{"mode":"video","video":{"dataUrl":"data:video/mp4;base64,`,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewLocal(repository.New(db), dataDir)
	summary, err := svc.CompactInlineTaskMedia()
	if err != nil {
		t.Fatal(err)
	}
	if summary.Compacted != 0 || len(summary.Skipped) != 1 || summary.Skipped[0].TaskID != "broken-result-task" {
		t.Fatalf("压缩汇总 = %#v", summary)
	}
}
