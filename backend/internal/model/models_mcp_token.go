package model

import "time"

// MCPToken is a personal access token for external MCP clients. Only the hash
// is stored; the plaintext token is shown once at creation time.
type MCPToken struct {
	ID        string `gorm:"column:id;primaryKey;size:36"`
	UserID    string `gorm:"column:user_id;size:36;index;not null"`
	Name      string `gorm:"column:name;size:100;not null"`
	TokenHash string `gorm:"column:token_hash;size:64;uniqueIndex;not null"`
	Prefix    string `gorm:"column:prefix;size:16;not null"`
	// GrantID 指向授权中心。令牌只表达"这是谁的连接"，额度与权限在 Grant 上。
	GrantID    string     `gorm:"column:grant_id;size:36;index"`
	ScopesJSON string     `gorm:"column:scopes_json;type:text;not null"`
	ExpiresAt  *time.Time `gorm:"column:expires_at"`
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
}

func (MCPToken) TableName() string { return "mcp_tokens" }
