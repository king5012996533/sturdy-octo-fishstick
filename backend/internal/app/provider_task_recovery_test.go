package app

import (
	"context"
	"errors"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestProviderTaskRecoveryContextSurvivesClientCancellation(t *testing.T) {
	type contextKey string
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), contextKey("trace"), "trace-1"))
	cancelParent()

	recovery, cancelRecovery := providerTaskRecoveryContext(parent)
	defer cancelRecovery()
	if recovery.Err() != nil {
		t.Fatalf("recovery context inherited cancellation: %v", recovery.Err())
	}
	if recovery.Value(contextKey("trace")) != "trace-1" {
		t.Fatal("recovery context did not preserve request values")
	}
	if _, ok := recovery.Deadline(); !ok {
		t.Fatal("recovery context has no bounded deadline")
	}
}

func TestRetryableProtocolMediaDownload(t *testing.T) {
	for _, err := range []error{
		errors.New("net/http: TLS handshake timeout"),
		errors.New("read: connection reset by peer"),
		errors.New("unexpected EOF"),
	} {
		if !retryableProtocolMediaDownload(err) {
			t.Fatalf("retryableProtocolMediaDownload(%v) = false", err)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("HTTP 404")} {
		if retryableProtocolMediaDownload(err) {
			t.Fatalf("retryableProtocolMediaDownload(%v) = true", err)
		}
	}
}

func TestAdminRecoverFailedVideoTaskRequiresAdmin(t *testing.T) {
	svc := &Service{}
	_, err := svc.AdminRecoverFailedVideoTask(context.Background(), &model.User{ID: "u1", Role: model.UserRoleUser}, "TASK_1", "vid_1")
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.Status != 403 {
		t.Fatalf("普通账号取回上游结果应被拒绝，得到 %v", err)
	}
}

func TestAdminRecoverFailedVideoTaskRequiresProviderRequestID(t *testing.T) {
	svc := &Service{}
	actor := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	// 没有上游任务号就没有可查询的目标：这里必须在落库前失败，不能等到查询阶段。
	if _, err := svc.AdminRecoverFailedVideoTask(context.Background(), actor, "TASK_1", "   "); err == nil {
		t.Fatal("缺少上游任务号时应当报错")
	}
	// 任务号缺失同理，避免把空任务号当成有效请求送到仓储层。
	if _, err := svc.AdminRecoverFailedVideoTask(context.Background(), actor, "", "vid_1"); err == nil {
		t.Fatal("缺少任务号时应当报错")
	}
}
