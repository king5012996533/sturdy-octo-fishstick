package auth

import (
	"encoding/json"
	"errors"
	"strings"
)

// AdminMethodConfigView 是「登录方式管理」的一行。
//
// Ready 与 UnavailableReason 是给运营看的：通道开着但没有凭据时，用户点下去只会
// 拿到一条报错，后台必须先说自己没配好。
type AdminMethodConfigView struct {
	MethodType        MethodType     `json:"methodType"`
	Category          MethodCategory `json:"category"`
	DisplayName       string         `json:"displayName"`
	Description       string         `json:"description"`
	SortOrder         int            `json:"sortOrder"`
	IsEnabled         bool           `json:"isEnabled"`
	IsVisible         bool           `json:"isVisible"`
	AllowSignUp       bool           `json:"allowSignUp"`
	AllowAutoFill     bool           `json:"allowAutoFill"`
	Ready             bool           `json:"ready"`
	UnavailableReason string         `json:"unavailableReason,omitempty"`
}

// AdminMethodConfigInput 是登录方式的可改字段。
//
// 只有指针字段会被写入：后台一次只改一个开关时，nil 表示"这项没动"，避免把
// 未提交的字段一起写回默认值。
type AdminMethodConfigInput struct {
	IsEnabled   *bool `json:"isEnabled"`
	IsVisible   *bool `json:"isVisible"`
	AllowSignUp *bool `json:"allowSignUp"`
}

// AdminMethodConfigs 返回管理视角的登录方式列表。
func (s *Service) AdminMethodConfigs() ([]AdminMethodConfigView, error) {
	configs, err := s.store.AdminMethodConfigs()
	if err != nil {
		return nil, internalFailure(err)
	}
	views := make([]AdminMethodConfigView, 0, len(configs))
	for _, config := range configs {
		views = append(views, toAdminMethodConfigView(config))
	}
	return views, nil
}

// AdminUpdateMethodConfig 改写一条登录方式的开关。
func (s *Service) AdminUpdateMethodConfig(methodType MethodType, input AdminMethodConfigInput) (*AdminMethodConfigView, error) {
	normalized := MethodType(strings.ToUpper(strings.TrimSpace(string(methodType))))
	if normalized == "" {
		return nil, invalidArgument("请选择要修改的登录方式")
	}
	current, err := s.store.MethodConfig(normalized)
	if errors.Is(err, ErrNotFound) {
		return nil, notFound("登录方式不存在")
	}
	if err != nil {
		return nil, internalFailure(err)
	}
	fields := map[string]any{}
	if input.IsEnabled != nil {
		fields["is_enabled"] = *input.IsEnabled
		if current.Category == CategoryOAuth {
			// OAuth 的 is_enabled 列由凭据推导（见 devschema 的收敛逻辑），管理员的开关
			// 必须落在配置里，否则下次启动会被凭据重算覆盖回"开"。
			merged, mergeErr := mergeOAuthAdminFlag(current.ConfigJSON, *input.IsEnabled)
			if mergeErr != nil {
				return nil, internalFailure(mergeErr)
			}
			fields["config_json"] = merged
			// 凭据不齐时不允许打开：写成 true 只会得到一个"能点但必然失败"的按钮。
			fields["is_enabled"] = *input.IsEnabled && methodUnavailableReason(*current) == ""
		}
	}
	if input.IsVisible != nil {
		fields["is_visible"] = *input.IsVisible
	}
	if input.AllowSignUp != nil {
		fields["allow_sign_up"] = *input.AllowSignUp
	}
	if len(fields) == 0 {
		return nil, invalidArgument("没有需要修改的内容")
	}
	// 关闭某条通道前必须确认还有别的通道能登录：全部关掉等于把所有人（包括管理员
	// 自己）挡在门外，而这条规则只有服务端能一眼看全。
	if enabled, ok := fields["is_enabled"].(bool); ok && !enabled {
		remaining, countErr := s.store.CountEnabledMethodConfigs(normalized)
		if countErr != nil {
			return nil, internalFailure(countErr)
		}
		if remaining == 0 {
			return nil, invalidArgument("至少需要保留一种可用的登录方式")
		}
	}
	if err := s.store.UpdateMethodConfig(normalized, fields); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notFound("登录方式不存在")
		}
		return nil, internalFailure(err)
	}
	updated, err := s.store.MethodConfig(normalized)
	if err != nil {
		return nil, internalFailure(err)
	}
	view := toAdminMethodConfigView(*updated)
	if input.IsEnabled != nil && *input.IsEnabled && !view.IsEnabled && view.UnavailableReason == "" {
		// 请求"打开"却没有打开，必须说明原因：否则管理员只会看到开关弹回去。
		view.UnavailableReason = "该登录方式的凭据尚未配置，已保持关闭"
	}
	return &view, nil
}

func toAdminMethodConfigView(config MethodConfig) AdminMethodConfigView {
	view := AdminMethodConfigView{
		MethodType:    config.MethodType,
		Category:      config.Category,
		DisplayName:   config.DisplayName,
		SortOrder:     config.SortOrder,
		IsEnabled:     config.IsEnabled,
		IsVisible:     config.IsVisible,
		AllowSignUp:   config.AllowSignUp,
		AllowAutoFill: config.AllowAutoFill,
		Ready:         true,
	}
	if config.Description != nil {
		view.Description = *config.Description
	}
	if reason := methodUnavailableReason(config); reason != "" {
		view.Ready = false
		view.UnavailableReason = reason
	}
	return view
}

// methodUnavailableReason 判断一条通道缺什么才能真的用起来。
//
// 目前只有 OAuth 有"凭据不齐"这个状态：验证码通道缺 SMTP/短信网关时服务端会回落到
// 控制台投递，功能仍然可用（只是验证码进日志），所以不算未就绪。
func methodUnavailableReason(config MethodConfig) string {
	if config.Category != CategoryOAuth {
		return ""
	}
	var oauth OAuthConfig
	if len(config.ConfigJSON) > 0 {
		_ = json.Unmarshal(config.ConfigJSON, &oauth)
	}
	if strings.TrimSpace(oauth.ClientID) == "" || strings.TrimSpace(oauth.ClientSecret) == "" {
		return "未配置 Client ID / Client Secret"
	}
	return ""
}

// mergeOAuthAdminFlag 把管理员开关写进 OAuth 配置 JSON。
//
// 未知字段会被保留（先反序列化再回写），因为这张表的生产写入方是 CanvasMind，
// 这里不能顺手把它写的其他配置项抹掉。
func mergeOAuthAdminFlag(raw []byte, adminEnabled bool) ([]byte, error) {
	var extra map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &extra); err != nil {
			return nil, err
		}
	}
	if extra == nil {
		extra = map[string]any{}
	}
	extra["adminEnabled"] = adminEnabled
	return json.Marshal(extra)
}
