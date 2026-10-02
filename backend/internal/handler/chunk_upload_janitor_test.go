package handler

import (
	"context"
	"os"
	"testing"
	"time"
)

// 分片上传的临时目录必须有人兜底回收：会话只在内存里，重启后磁盘上的目录会失去
// 主人；空闲时也没有第二个请求会顺带触发清理。

func registerTestChunkSession(t *testing.T, createdAt time.Time) *chunkedUploadSession {
	t.Helper()
	dir, err := os.MkdirTemp("", chunkUploadDirPrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	session := &chunkedUploadSession{
		ID:        newUploadSessionID(),
		UserID:    "janitor-test-user",
		Dir:       dir,
		CreatedAt: createdAt,
		Size:      1,
	}
	chunkUploadSessions.Lock()
	chunkUploadSessions.m[session.ID] = session
	chunkUploadSessions.Unlock()
	t.Cleanup(func() {
		dropChunkSession(session.ID)
		_ = os.RemoveAll(dir)
	})
	return session
}

func TestRemoveExpiredChunkSessionsRemovesOnlyExpired(t *testing.T) {
	expired := registerTestChunkSession(t, time.Now().Add(-chunkUploadTTL-time.Minute))
	fresh := registerTestChunkSession(t, time.Now())

	if removed := removeExpiredChunkSessions(); removed != 1 {
		t.Fatalf("应当只清掉过期会话，实际清理 %d 条", removed)
	}
	if _, err := os.Stat(expired.Dir); !os.IsNotExist(err) {
		t.Fatalf("过期会话的临时目录没有被删除：%v", err)
	}
	if _, err := os.Stat(fresh.Dir); err != nil {
		t.Fatalf("未过期会话的临时目录被误删：%v", err)
	}
	if got := takeChunkSession(fresh.ID); got == nil {
		t.Fatal("未过期会话被误删")
	}
	if got := takeChunkSession(expired.ID); got != nil {
		t.Fatal("过期会话仍留在内存里")
	}
}

func TestRemoveExpiredChunkSessionsIsIdempotent(t *testing.T) {
	registerTestChunkSession(t, time.Now().Add(-chunkUploadTTL-time.Minute))
	if removed := removeExpiredChunkSessions(); removed != 1 {
		t.Fatalf("首次清理应为 1 条，实际 %d", removed)
	}
	if removed := removeExpiredChunkSessions(); removed != 0 {
		t.Fatalf("重复清理应当没有可清对象，实际 %d", removed)
	}
}

func TestRunChunkUploadJanitorCleansExpiredSessionsAndStops(t *testing.T) {
	registerTestChunkSession(t, time.Now().Add(-chunkUploadTTL-time.Minute))
	fresh := registerTestChunkSession(t, time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunChunkUploadJanitor(ctx, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for !expiredSessionGone(t) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !expiredSessionGone(t) {
		t.Fatal("后台清理没有回收过期会话")
	}
	if takeChunkSession(fresh.ID) == nil {
		t.Fatal("后台清理误删了未过期会话")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("取消 context 后清理协程没有退出")
	}
}

func expiredSessionGone(t *testing.T) bool {
	t.Helper()
	chunkUploadSessions.Lock()
	defer chunkUploadSessions.Unlock()
	for _, session := range chunkUploadSessions.m {
		if session.UserID == "janitor-test-user" && time.Since(session.CreatedAt) > chunkUploadTTL {
			return false
		}
	}
	return true
}

func TestSweepOrphanChunkUploadDirsKeepsFreshDirs(t *testing.T) {
	stale, err := os.MkdirTemp("", chunkUploadDirPrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stale) })
	current, err := os.MkdirTemp("", chunkUploadDirPrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(current) })

	old := time.Now().Add(-chunkUploadTTL - 2*time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if removed := sweepOrphanChunkUploadDirs(); removed == 0 {
		t.Fatal("重启残留目录没有被回收")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("超龄残留目录仍然存在：%v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("仍在校验期内的目录被误删：%v", err)
	}
}
