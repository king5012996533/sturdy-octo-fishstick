package app

import (
	"strings"

	"infinite-canvas/backend/internal/model"
)

// AuthUser is retained as a response-shape compatibility type. Local mode has
// no session or external identity provider.
type AuthUser struct {
	model.User
	AvatarURL        string `json:"avatarUrl,omitempty"`
	IdentityProvider string `json:"identityProvider,omitempty"`
	IdentityID       string `json:"identityId,omitempty"`
	IdentityUsername string `json:"identityUsername,omitempty"`
}

func (s *Service) LocalWorkspaceOwner() (*model.User, error) {
	if s == nil || s.repo == nil {
		return nil, Unauthorized("本地工作区尚未初始化")
	}
	value, err := s.repo.DefaultWorkspace()
	if err != nil {
		return nil, err
	}
	return localWorkspacePrincipal(value), nil
}

func (s *Service) WorkspaceOwner(id string) (*model.User, error) {
	value, err := s.repo.Workspace(id)
	if err != nil {
		return nil, err
	}
	return localWorkspacePrincipal(value), nil
}

// EnsureWorkspace 为新登录账号准备工作区，重复调用无副作用。
//
// 多租户下每个账号各自拥有一份画布数据，工作区必须在首个请求到达前存在，
// 否则后续所有按 workspace 过滤的查询都会落空。
func (s *Service) EnsureWorkspace(id string, displayName string) error {
	if s == nil || s.repo == nil {
		return Unauthorized("本地工作区尚未初始化")
	}
	if strings.TrimSpace(id) == "" {
		return Unauthorized("缺少工作区标识")
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = id
	}
	_, err := s.repo.EnsureWorkspace(id, name)
	return err
}

func localWorkspacePrincipal(value *model.Workspace) *model.User {
	if value == nil {
		return nil
	}
	return &model.User{
		ID: value.ID, Username: "local", DisplayName: value.Name,
		Role: model.UserRoleAdmin, Status: model.UserStatusActive,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func (s *Service) PublicAuthUser(user *model.User) (AuthUser, error) {
	if user == nil {
		return AuthUser{}, Unauthorized("本地工作区尚未初始化")
	}
	return AuthUser{User: *user}, nil
}
