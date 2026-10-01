package auth

import (
	"context"
	"encoding/json"
	"time"
)

const (
	// SessionCookieName 与 CanvasMind 保持一致，便于两个前端共用同一份会话。
	SessionCookieName = "canana_session"

	// DefaultSessionDays 是滑动有效期，与 CanvasMind 默认值一致。
	DefaultSessionDays = 30

	// AbsoluteSessionDays 是本宿主额外施加的绝对上限。CanvasMind 只有滑动过期，
	// 被盗令牌只要持续使用就永不失效；这里从 createdAt 起算封顶。
	AbsoluteSessionDays = 90

	// lastActiveWriteInterval 用于抑制 last_active_at 的写放大：
	// 命中会话时每个请求都写一次 MySQL 是不可接受的。
	lastActiveWriteInterval = 5 * time.Minute

	// DefaultCodeExpireMinutes 是验证码有效期，与 CanvasMind 默认值一致。
	DefaultCodeExpireMinutes = 5
)

// AuthUser 是暴露给上层的登录用户视图，不含任何凭据。
type AuthUser struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Email    string     `json:"email"`
	Avatar   string     `json:"avatarUrl"`
	Role     UserRole   `json:"role"`
	Method   MethodType `json:"loginMethodType"`
	Status   UserStatus `json:"status"`
	ExpireAt time.Time  `json:"expiresAt"`
}

// SendCodeInput 是下发验证码的入口参数。
type SendCodeInput struct {
	MethodType  MethodType
	Target      string
	RequesterIP string
	UserAgent   string
	// Scene 由服务端指定（见 sendCodeScene），留空即登录场景。它同时决定冷却的
	// 计数维度，因此不接受客户端传值。
	Scene  string
	Config MethodConfig
}

// SendCodeOutput 只包含可安全回传的字段。
//
// 默认不含验证码：CanvasMind 的 debugCode 出参是一次认证绕过，本模块不复制那个
// 模式。唯一的例外是 DevCode，它由两个条件同时把守（见 Service.devEcho）：显式
// 打开配置，且确实在用控制台投递器（即没有 SMTP）。生产配了 SMTP 后该分支不可达。
type SendCodeOutput struct {
	Target    string    `json:"target"`
	Channel   string    `json:"channel"`
	ExpiresAt time.Time `json:"expiresAt"`
	Cooldown  int       `json:"cooldownSeconds"`
	DevCode   string    `json:"devCode,omitempty"`
}

// LoginInput 是登录入口参数。
type LoginInput struct {
	MethodType  MethodType
	Target      string
	Code        string
	Password    string
	RequesterIP string
	UserAgent   string
	Config      MethodConfig
}

// AuthorizeInput 是第三方授权跳转参数。
type AuthorizeInput struct {
	MethodType  MethodType
	State       string
	RedirectURI string
	Config      MethodConfig
}

// CallbackInput 是第三方授权回调参数。
type CallbackInput struct {
	MethodType  MethodType
	Code        string
	State       string
	RedirectURI string
	RequesterIP string
	UserAgent   string
	Config      MethodConfig
}

// LoginOutput 是登录成功结果，Token 只交给 Cookie，不进响应体。
type LoginOutput struct {
	Token     string
	ExpiresAt time.Time
	User      AuthUser
}

// OAuthConfig 是 auth_method_configs.config_json 的 OAuth 子集。
type OAuthConfig struct {
	AuthorizeURL string `json:"authorizeUrl"`
	TokenURL     string `json:"tokenUrl"`
	UserInfoURL  string `json:"userInfoUrl"`
	EmailURL     string `json:"emailUrl"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	ResponseType string `json:"responseType"`
	Scope        string `json:"scope"`
	RedirectURI  string `json:"redirectUri"`
	// AdminEnabled 是"管理后台是否允许该通道"的落点。
	//
	// OAuth 的 is_enabled 列由凭据推导（见 devschema 的收敛逻辑），管理员开关若也写
	// 那一列，会在下次启动时被凭据重算覆盖。放在配置里，两者就不会互相擦掉。
	// nil 表示管理员没表过态，按"允许"处理。
	AdminEnabled *bool `json:"adminEnabled,omitempty"`
}

// ParseOAuthConfig 解析登录方式携带的 OAuth 配置。
func ParseOAuthConfig(raw []byte) (OAuthConfig, error) {
	var config OAuthConfig
	if len(raw) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return OAuthConfig{}, err
	}
	return config, nil
}

// Strategy 描述一种登录方式的能力。
//
// 用函数字段而不是接口，是为了让「可选能力」显式为 nil：短信要能下发验证码，
// OAuth 要能换 token 再回调，两者能力集合不同，用接口会强迫实现空方法。
type Strategy struct {
	MethodType     MethodType
	Category       MethodCategory
	SendCode       func(ctx context.Context, in SendCodeInput) (*SendCodeOutput, error)
	Login          func(ctx context.Context, in LoginInput) (*LoginOutput, error)
	Register       func(ctx context.Context, in RegisterInput) (*LoginOutput, error)
	AuthorizeURL   func(ctx context.Context, in AuthorizeInput) (*AuthorizeOutput, error)
	HandleCallback func(ctx context.Context, in CallbackInput) (*LoginOutput, error)
}

// AuthorizeOutput 是第三方授权跳转结果。
type AuthorizeOutput struct {
	AuthURL string `json:"authUrl"`
	State   string `json:"state"`
}

// RegisterInput 是注册入口参数。
//
// 注册与登录分成两个入口是刻意的：注册必须先确认协议已被接受，而登录只需要证明
// 账号存在。把两件事塞进同一个接口，就没法保证「建号」一定伴随一次协议同意。
type RegisterInput struct {
	MethodType MethodType
	Target     string
	Code       string
	// Password 只被密码通道使用；验证码通道留空。
	Password string
	// AgreementVersion 是前端展示给用户的协议版本。与当前版本不一致时必须拒绝，
	// 否则用户同意的是旧条款，落库的却是新版本号。
	AgreementVersion string
	RequesterIP      string
	UserAgent        string
	Config           MethodConfig
}
