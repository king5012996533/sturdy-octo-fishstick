package auth

import "time"

// 本包映射的是 CanvasMind（Prisma/MySQL）已有的账号体系表，列名和取值域必须与
// prisma/schema.prisma 完全一致。GORM 只做读写投影，绝不执行 AutoMigrate：
// 这些表由 Prisma 迁移管理，宿主不得改写其结构。
//
// 可空列一律用指针表达。MySQL 的唯一索引把 NULL 视为互不冲突，但多个 "" 会冲突，
// 所以不能把 NULL 写成空字符串。

// UserRole 与 Prisma enum UserRole 对齐。
type UserRole string

const (
	RoleUser  UserRole = "USER"
	RoleAdmin UserRole = "ADMIN"
)

// UserStatus 与 Prisma enum UserStatus 对齐。
type UserStatus string

const (
	StatusAnonymous UserStatus = "ANONYMOUS"
	StatusActive    UserStatus = "ACTIVE"
	StatusDisabled  UserStatus = "DISABLED"
)

// MethodType 与 Prisma enum AuthMethodType 对齐。
type MethodType string

const (
	MethodAdminPassword MethodType = "ADMIN_PASSWORD"
	// MethodPassword 是普通用户的密码通道，标识为邮箱或手机号。
	//
	// 与 MethodAdminPassword 分开而不是复用：管理员那条按 username 列寻址，这条按
	// 邮箱/手机号列寻址，两者的标识形态和可注册性都不同。新增取值需要 CanvasMind
	// 侧一条 Prisma migration —— auth_method_configs 与 app_user_auth_identities 的
	// method_type 都是 MySQL enum 列，枚举外的值会被数据库直接拒掉。
	MethodPassword    MethodType = "PASSWORD"
	MethodPhoneCode   MethodType = "PHONE_CODE"
	MethodEmailCode   MethodType = "EMAIL_CODE"
	MethodWechatOAuth MethodType = "WECHAT_OAUTH"
	MethodGithubOAuth MethodType = "GITHUB_OAUTH"
	MethodGoogleOAuth MethodType = "GOOGLE_OAUTH"
	MethodCustomOAuth MethodType = "CUSTOM_OAUTH"
)

// MethodCategory 与 Prisma enum AuthMethodCategory 对齐。
type MethodCategory string

const (
	CategoryPassword MethodCategory = "PASSWORD"
	CategoryCode     MethodCategory = "CODE"
	CategoryOAuth    MethodCategory = "OAUTH"
)

// VerificationChannel 与 Prisma enum VerificationChannel 对齐。
type VerificationChannel string

const (
	ChannelPhone VerificationChannel = "PHONE"
	ChannelEmail VerificationChannel = "EMAIL"
)

// User 映射 app_users。
type User struct {
	ID           string     `gorm:"column:id;primaryKey;size:36"`
	Username     *string    `gorm:"column:username;size:100"`
	PasswordHash *string    `gorm:"column:password_hash;size:255"`
	Name         *string    `gorm:"column:name;size:100"`
	AvatarURL    *string    `gorm:"column:avatar_url;type:text"`
	Email        *string    `gorm:"column:email;size:191"`
	Phone        *string    `gorm:"column:phone;size:32"`
	Role         UserRole   `gorm:"column:role;size:24"`
	Status       UserStatus `gorm:"column:status;size:24"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
}

func (User) TableName() string { return "app_users" }

// DisplayName 返回可展示名称，缺失时回落到账号或邮箱。
func (u User) DisplayName() string {
	if u.Name != nil && *u.Name != "" {
		return *u.Name
	}
	if u.Username != nil && *u.Username != "" {
		return *u.Username
	}
	if u.Email != nil && *u.Email != "" {
		return *u.Email
	}
	if u.Phone != nil && *u.Phone != "" {
		return *u.Phone
	}
	return u.ID
}

// EmailValue 返回去空白的邮箱。
func (u User) EmailValue() string {
	if u.Email == nil {
		return ""
	}
	return *u.Email
}

// PhoneValue 返回去空白的手机号。
func (u User) PhoneValue() string {
	if u.Phone == nil {
		return ""
	}
	return *u.Phone
}

// Session 映射 app_sessions。
type Session struct {
	ID                 string     `gorm:"column:id;primaryKey;size:36"`
	UserID             string     `gorm:"column:user_id;size:36"`
	TokenHash          string     `gorm:"column:token_hash;size:128"`
	AuthMethodType     MethodType `gorm:"column:auth_method_type;size:24"`
	IdentifierSnapshot *string    `gorm:"column:identifier_snapshot;size:191"`
	IPAddress          *string    `gorm:"column:ip_address;size:100"`
	UserAgent          *string    `gorm:"column:user_agent;size:500"`
	ExpiresAt          time.Time  `gorm:"column:expires_at"`
	RevokedAt          *time.Time `gorm:"column:revoked_at"`
	LastActiveAt       *time.Time `gorm:"column:last_active_at"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at"`
}

func (Session) TableName() string { return "app_sessions" }

// VerificationCode 映射 auth_verification_codes。
//
// Code 以明文存储：该表与 CanvasMind 共用，格式必须保持一致，宿主无权改其语义。
// 明文只允许存在于服务端，任何情况下都不得出现在 HTTP 响应里。
type VerificationCode struct {
	ID          string              `gorm:"column:id;primaryKey;size:36"`
	UserID      *string             `gorm:"column:user_id;size:36"`
	MethodType  MethodType          `gorm:"column:method_type;size:24"`
	Channel     VerificationChannel `gorm:"column:channel;size:24"`
	Scene       string              `gorm:"column:scene;size:50"`
	Target      string              `gorm:"column:target;size:191"`
	Code        string              `gorm:"column:code;size:16"`
	ExpiresAt   time.Time           `gorm:"column:expires_at"`
	UsedAt      *time.Time          `gorm:"column:used_at"`
	RequesterIP *string             `gorm:"column:requester_ip;size:100"`
	UserAgent   *string             `gorm:"column:user_agent;size:500"`
	CreatedAt   time.Time           `gorm:"column:created_at"`
	UpdatedAt   time.Time           `gorm:"column:updated_at"`
}

func (VerificationCode) TableName() string { return "auth_verification_codes" }

// AuthIdentity 映射 app_user_auth_identities，用于绑定第三方身份。
type AuthIdentity struct {
	ID              string     `gorm:"column:id;primaryKey;size:36"`
	UserID          string     `gorm:"column:user_id;size:36"`
	MethodType      MethodType `gorm:"column:method_type;size:24"`
	ProviderUserID  *string    `gorm:"column:provider_user_id;size:191"`
	ProviderUnionID *string    `gorm:"column:provider_union_id;size:191"`
	Identifier      string     `gorm:"column:identifier;size:191"`
	IsVerified      bool       `gorm:"column:is_verified"`
	VerifiedAt      *time.Time `gorm:"column:verified_at"`
	MetaJSON        []byte     `gorm:"column:meta_json"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

func (AuthIdentity) TableName() string { return "app_user_auth_identities" }

// MethodConfig 映射 auth_method_configs。
//
// 该表由 CanvasMind 后台维护；BeefTV 只读取，避免两侧同时改同一行。
type MethodConfig struct {
	ID            string         `gorm:"column:id;primaryKey;size:36"`
	UserID        *string        `gorm:"column:user_id;size:36"`
	MethodType    MethodType     `gorm:"column:method_type;size:24"`
	Category      MethodCategory `gorm:"column:category;size:24"`
	DisplayName   string         `gorm:"column:display_name;size:100"`
	Description   *string        `gorm:"column:description;size:255"`
	IconType      *string        `gorm:"column:icon_type;size:50"`
	IconURL       *string        `gorm:"column:icon_url;type:text"`
	IsEnabled     bool           `gorm:"column:is_enabled"`
	IsVisible     bool           `gorm:"column:is_visible"`
	SortOrder     int            `gorm:"column:sort_order"`
	AllowAutoFill bool           `gorm:"column:allow_auto_fill"`
	AllowSignUp   bool           `gorm:"column:allow_sign_up"`
	ConfigJSON    []byte         `gorm:"column:config_json"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	UpdatedAt     time.Time      `gorm:"column:updated_at"`
}

func (MethodConfig) TableName() string { return "auth_method_configs" }

// UserAgreement 映射 app_user_agreements。
//
// 注册时必须落这条记录：它回答的是「这个用户在哪一刻接受了哪一版协议」。这是争议
// 发生时唯一能举证的原始记录，因此版本与接受时间都不可为空，来源 IP 与 UA 一并留存。
type UserAgreement struct {
	ID            string    `gorm:"column:id;primaryKey;size:36"`
	UserID        string    `gorm:"column:user_id;size:36"`
	AgreementType string    `gorm:"column:agreement_type;size:32"`
	Version       string    `gorm:"column:version;size:32"`
	AcceptedAt    time.Time `gorm:"column:accepted_at"`
	IPAddress     *string   `gorm:"column:ip_address;size:100"`
	UserAgent     *string   `gorm:"column:user_agent;size:500"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (UserAgreement) TableName() string { return "app_user_agreements" }

// AgreementVersion 映射 auth_agreement_versions。
//
// 协议正文由运营后台维护，不编译进二进制：条款更新必须能独立于前端发布，否则停留在
// 旧客户端的用户会一直看到旧条款。版本串是留痕的一部分（用户接受的就是它），因此
// 发布后的行不可修改，只能再发一版。
type AgreementVersion struct {
	Version      string    `gorm:"column:version;primaryKey;size:32"`
	TermsTitle   string    `gorm:"column:terms_title;size:64"`
	TermsBody    string    `gorm:"column:terms_body"`
	PrivacyTitle string    `gorm:"column:privacy_title;size:64"`
	PrivacyBody  string    `gorm:"column:privacy_body"`
	PublishedAt  time.Time `gorm:"column:published_at"`
	PublishedBy  string    `gorm:"column:published_by;size:36"`
}

func (AgreementVersion) TableName() string { return "auth_agreement_versions" }

// AgreementSignatureRow 是签署记录的一行：协议 + 账号标识。
//
// 账号字段来自 app_users，只在托管层合并：审核争议时要能直接说出「谁在什么时候同意了
// 哪一版」，只有一个 user_id 等于还要再查一次。
type AgreementSignatureRow struct {
	ID            string    `gorm:"column:id"`
	UserID        string    `gorm:"column:user_id"`
	Email         string    `gorm:"column:email"`
	Phone         string    `gorm:"column:phone"`
	Name          string    `gorm:"column:name"`
	AgreementType string    `gorm:"column:agreement_type"`
	Version       string    `gorm:"column:version"`
	AcceptedAt    time.Time `gorm:"column:accepted_at"`
	IPAddress     string    `gorm:"column:ip_address"`
	UserAgent     string    `gorm:"column:user_agent"`
}
