package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

// 未装配管理端口时必须是拒绝，而不是放行。放行会让"忘接线"变成静默的越权入口。
func TestPlatformHostRequireAdminFailsClosedWhenUnwired(t *testing.T) {
	host := platformHost{}
	if err := host.RequireAdmin(&model.User{ID: "u1", Role: model.UserRoleUser}); err == nil {
		t.Fatal("platformHost 未装配 requireAdmin 时应拒绝")
	}
}

func TestPlatformHostRequireAdminUsesWiredGate(t *testing.T) {
	denied := platformHost{requireAdmin: func(*model.User) error { return Forbidden("需要管理员权限") }}
	if err := denied.RequireAdmin(&model.User{ID: "u1"}); err == nil {
		t.Fatal("已装配的拒绝判定不应被绕过")
	}

	allowed := platformHost{requireAdmin: func(*model.User) error { return nil }}
	if err := allowed.RequireAdmin(&model.User{ID: "u1"}); err != nil {
		t.Fatalf("已装配的放行判定不应被改写：%v", err)
	}
}

func TestPromptAdminGateRequireAdminFailsClosedWhenUnwired(t *testing.T) {
	gate := promptAdminGate{}
	if err := gate.RequireAdmin(&model.User{ID: "u1", Role: model.UserRoleUser}); err == nil {
		t.Fatal("promptAdminGate 未装配 requireAdmin 时应拒绝")
	}
}

func TestPromptAdminGateRequireAdminUsesWiredGate(t *testing.T) {
	denied := promptAdminGate{requireAdmin: func(*model.User) error { return Forbidden("需要管理员权限") }}
	if err := denied.RequireAdmin(&model.User{ID: "u1"}); err == nil {
		t.Fatal("已装配的拒绝判定不应被绕过")
	}

	allowed := promptAdminGate{requireAdmin: func(*model.User) error { return nil }}
	if err := allowed.RequireAdmin(&model.User{ID: "u1"}); err != nil {
		t.Fatalf("已装配的放行判定不应被改写：%v", err)
	}
}
