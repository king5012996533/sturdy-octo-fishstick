package app

import (
	"path/filepath"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newResourceProvenanceTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "canvas.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.UserDailyUploadUsage{}, &model.Resource{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir()), db
}

func TestBackfillResourceProvenanceLinksHistoryAndStaysIdempotent(t *testing.T) {
	service, db := newResourceProvenanceTestService(t)
	now := time.Now().UTC()
	if err := db.Create(&[]model.Resource{
		{ID: "res-1", UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady, Provider: "local", CreatedAt: now},
		{ID: "res-2", UserID: "user-1", Kind: "video", Status: model.ResourceStatusReady, Provider: "local", CreatedAt: now},
		// 用户上传的素材：任何任务结果里都不会出现它，回填后仍然是未关联。
		{ID: "res-3", UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady, Provider: "local", Source: resourceSourceUpload, CreatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.Task{
		{ID: "task-1", UserID: "user-1", Type: "canvas_image", Status: model.TaskStatusSucceeded, CreatedAt: now, ResultJSON: `{"images":[{"resourceId":"res-1","url":"/api/public/resources/res-1/file/x.png"}]}`},
		// 另一条任务的结果里写的是 storageKey：两种写法都要能被认出来。
		{ID: "task-2", UserID: "user-1", Type: "canvas_video", Status: model.TaskStatusSucceeded, CreatedAt: now, ResultJSON: `{"video":{"storageKey":"resource:res-2"}}`},
		// 自由文本里出现一个像资源 ID 的片段：不能按子串匹配，否则会误认领。
		{ID: "task-3", UserID: "user-1", Type: "canvas_text", Status: model.TaskStatusSucceeded, CreatedAt: now, ResultJSON: `{"text":"试试 res-1 这个编号"}`},
	}).Error; err != nil {
		t.Fatal(err)
	}

	dryRun, err := service.BackfillResourceProvenance(true)
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.Applied == false || dryRun.Linked != 2 || dryRun.Unmatched != 1 || dryRun.ResourcesMissing != 3 {
		t.Fatalf("演练结果 = %#v; want linked 2 / unmatched 1 / missing 3 / 未写库", dryRun)
	}
	var untouched model.Resource
	if err := db.Where("id = ?", "res-1").First(&untouched).Error; err != nil {
		t.Fatal(err)
	}
	if untouched.TaskID != "" {
		t.Fatalf("演练不该写库，res-1.task_id = %q", untouched.TaskID)
	}

	applied, err := service.BackfillResourceProvenance(false)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.Linked != 2 || applied.Unmatched != 1 {
		t.Fatalf("回填结果 = %#v; want applied / linked 2 / unmatched 1", applied)
	}
	var linked model.Resource
	if err := db.Where("id = ?", "res-1").First(&linked).Error; err != nil {
		t.Fatal(err)
	}
	if linked.TaskID != "task-1" || linked.Source != resourceSourceGeneration {
		t.Fatalf("res-1 溯源 = %q/%q; want task-1/generation", linked.TaskID, linked.Source)
	}
	var storageKeyLinked model.Resource
	if err := db.Where("id = ?", "res-2").First(&storageKeyLinked).Error; err != nil {
		t.Fatal(err)
	}
	if storageKeyLinked.TaskID != "task-2" {
		t.Fatalf("storageKey 写法没被认出来：%#v", storageKeyLinked)
	}
	var uploaded model.Resource
	if err := db.Where("id = ?", "res-3").First(&uploaded).Error; err != nil {
		t.Fatal(err)
	}
	if uploaded.TaskID != "" || uploaded.Source != resourceSourceUpload {
		t.Fatalf("上传素材被回填改写了：%#v", uploaded)
	}

	// 再跑一次：没有待补的记录，也不该改写已经确认的关联。
	again, err := service.BackfillResourceProvenance(false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Linked != 0 || again.ResourcesMissing != 1 {
		t.Fatalf("重复回填 = %#v; want 只剩上传的那一条待定", again)
	}
}

func TestPersistGeneratedMediaResultForTaskRecordsProvenance(t *testing.T) {
	service, db := newResourceProvenanceTestService(t)
	service.localResourceStorage = true
	task := &model.Task{ID: "task-provenance", UserID: "user-1", Type: "canvas_image"}
	if _, err := service.persistGeneratedMediaResultForTask(task, map[string]interface{}{
		"image": map[string]interface{}{"dataUrl": "data:image/png;base64,YQ=="},
	}); err != nil {
		t.Fatal(err)
	}
	var stored model.Resource
	if err := db.Where("user_id = ?", "user-1").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TaskID != task.ID || stored.Source != resourceSourceGeneration {
		t.Fatalf("生成产物的溯源 = %q/%q; want %s/generation", stored.TaskID, stored.Source, task.ID)
	}
}

func TestPersistGeneratedMediaResultWithoutTaskKeepsUnlinked(t *testing.T) {
	service, db := newResourceProvenanceTestService(t)
	service.localResourceStorage = true
	if _, err := service.persistGeneratedMediaResult("user-1", map[string]interface{}{
		"image": map[string]interface{}{"dataUrl": "data:image/png;base64,YQ=="},
	}); err != nil {
		t.Fatal(err)
	}
	var stored model.Resource
	if err := db.Where("user_id = ?", "user-1").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	// 没有任务上下文时必须自认来源不明，而不是猜一个任务挂上去。
	if stored.TaskID != "" || stored.Source != resourceSourceLegacy {
		t.Fatalf("无任务上下文的产物溯源 = %q/%q; want 空/legacy", stored.TaskID, stored.Source)
	}
}
