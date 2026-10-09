package repository

import (
	"fmt"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 这两条查询原先没有直接测试覆盖，容易被"看起来等价"的改写改坏；同时它们承载配额判定，
// 统计口径必须按字节。这里用多字节 UTF-8 把"字节 ≠ 字符"钉死。
func storageUsageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:storage-usage-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.Asset{}, &model.CanvasProject{}, &model.Task{}, &model.TaskLog{}, &model.Result{}, &model.TaskTextDelta{}, &model.ApiCallLog{}, &model.CreationRun{}, &model.CreationSubmission{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return db
}

func TestUserStorageUsageCountsBytesNotCharacters(t *testing.T) {
	db := storageUsageTestDB(t)
	repo := &Repository{db: db}

	// "中" 是 3 字节 UTF-8。若表达式退回 length()（字符数），下面会得到 1 而不是 3。
	chinese := "中"
	ascii := "abcd"

	if err := db.Create(&model.Asset{ID: "a1", UserID: "u1", Title: "t", PayloadJSON: chinese}).Error; err != nil {
		t.Fatalf("插入 asset 失败: %v", err)
	}
	if err := db.Create(&model.Asset{ID: "a2", UserID: "u1", Title: "t", PayloadJSON: ascii}).Error; err != nil {
		t.Fatalf("插入 asset 失败: %v", err)
	}
	// 另一个用户的数据不得计入
	if err := db.Create(&model.Asset{ID: "a3", UserID: "u2", Title: "t", PayloadJSON: "should-not-count"}).Error; err != nil {
		t.Fatalf("插入 asset 失败: %v", err)
	}

	usage, err := repo.UserStorageUsage("u1")
	if err != nil {
		t.Fatalf("UserStorageUsage 失败: %v", err)
	}
	if usage.AssetCount != 2 {
		t.Fatalf("AssetCount = %d, want 2", usage.AssetCount)
	}
	wantBytes := int64(len(chinese) + len(ascii))
	if usage.AssetBytes != wantBytes {
		t.Fatalf("AssetBytes = %d, want %d（3 字节 + 4 字节；返回 2 说明按字符计了）", usage.AssetBytes, wantBytes)
	}
}

func TestUserStorageUsageSumsEveryTableAndToleratesNulls(t *testing.T) {
	db := storageUsageTestDB(t)
	repo := &Repository{db: db}

	if err := db.Create(&model.CanvasProject{ID: "c1", UserID: "u1", Title: "t", PayloadJSON: "12345"}).Error; err != nil {
		t.Fatalf("插入 canvas_project 失败: %v", err)
	}
	if err := db.Create(&model.Task{ID: "t1", UserID: "u1", Prompt: "12", ResultJSON: "123", TextDraft: "", Error: "1"}).Error; err != nil {
		t.Fatalf("插入 task 失败: %v", err)
	}
	if err := db.Create(&model.TaskLog{ID: "l1", UserID: "u1", TaskID: "t1", Message: "1234"}).Error; err != nil {
		t.Fatalf("插入 task_log 失败: %v", err)
	}
	if err := db.Create(&model.Result{ID: "r1", UserID: "u1", TaskID: "t1", URL: "123456"}).Error; err != nil {
		t.Fatalf("插入 result 失败: %v", err)
	}
	if err := db.Create(&model.TaskTextDelta{ID: "d1", UserID: "u1", TaskID: "t1", Sequence: 1, ByteCount: 7}).Error; err != nil {
		t.Fatalf("插入 text_delta 失败: %v", err)
	}
	if err := db.Create(&model.ApiCallLog{ID: "g1", UserID: "u1"}).Error; err != nil {
		t.Fatalf("插入 api_call_log 失败: %v", err)
	}

	usage, err := repo.UserStorageUsage("u1")
	if err != nil {
		t.Fatalf("UserStorageUsage 失败: %v", err)
	}
	if usage.CanvasBytes != 5 {
		t.Fatalf("CanvasBytes = %d, want 5", usage.CanvasBytes)
	}
	if usage.TaskCount != 1 || usage.APICallCount != 1 {
		t.Fatalf("计数不对: task=%d apiCall=%d", usage.TaskCount, usage.APICallCount)
	}
	// 2(prompt) + 3(result) + 0(text_draft) + 1(error) + 4(log) + 6(result.url) + 7(delta) = 23
	if usage.TaskBytes != 23 {
		t.Fatalf("TaskBytes = %d, want 23", usage.TaskBytes)
	}
	if usage.CanvasCount != 1 || usage.AssetCount != 0 {
		t.Fatalf("计数不对: canvas=%d asset=%d", usage.CanvasCount, usage.AssetCount)
	}
}

func TestCreationStorageUsageCountsBytes(t *testing.T) {
	db := storageUsageTestDB(t)
	repo := &Repository{db: db}

	if err := db.Create(&model.CreationRun{ID: "run1", UserID: "u1", StateJSON: "1234"}).Error; err != nil {
		t.Fatalf("插入 creation_run 失败: %v", err)
	}
	if err := db.Create(&model.CreationSubmission{ID: "sub1", UserID: "u1", RequestJSON: "12345"}).Error; err != nil {
		t.Fatalf("插入 creation_submission 失败: %v", err)
	}

	count, bytes, err := repo.CreationStorageUsage("u1")
	if err != nil {
		t.Fatalf("CreationStorageUsage 失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if bytes != 9 {
		t.Fatalf("bytes = %d, want 9（4 + 5）", bytes)
	}
}

func TestStorageUsageEmptyDatabaseReturnsZero(t *testing.T) {
	db := storageUsageTestDB(t)
	repo := &Repository{db: db}

	usage, err := repo.UserStorageUsage("nobody")
	if err != nil {
		t.Fatalf("UserStorageUsage 失败: %v", err)
	}
	if usage.AssetBytes != 0 || usage.CanvasBytes != 0 || usage.TaskBytes != 0 {
		t.Fatalf("空库应全为 0，得到 %+v", usage)
	}
	if _, bytes, err := repo.CreationStorageUsage("nobody"); err != nil || bytes != 0 {
		t.Fatalf("空库 CreationStorageUsage = (%d, %v), want (0, nil)", bytes, err)
	}
}
