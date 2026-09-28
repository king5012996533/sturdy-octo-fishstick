package model

import (
	"time"

	"gorm.io/gorm"
)

const (
	// ModelVendorKindBuiltin 表示这个厂商来自协议注册表自带的目录：它的能力与协议
	// 清单可以被平台自动带出，运营只需要补充凭据。
	ModelVendorKindBuiltin = "BUILTIN"
	// ModelVendorKindCustom 表示运营手工新增的上游：注册表里没有它的协议定义，
	// 能力清单因此留空，由后续的模型配置去填充。
	ModelVendorKindCustom = "CUSTOM"
)

// ModelVendor 是后台视角的“上游厂商”。
//
// Code 是稳定标识而不是展示名：运营改名字时，已经写好的定价规则与倍率配置不应该
// 跟着失效，所以引用一律走 Code，展示名只服务于界面。
//
// CapabilitiesJSON / ProtocolsJSON 存 JSON 数组而不是逗号拼接：能力清单未来可能带
// 参数（如分辨档位），JSON 才是能向前兼容的载体。
type ModelVendor struct {
	ID               string         `json:"id" gorm:"primaryKey;size:64"`
	Code             string         `json:"code" gorm:"uniqueIndex;size:64"`
	Name             string         `json:"name" gorm:"size:80"`
	Kind             string         `json:"kind" gorm:"size:16"`
	CapabilitiesJSON string         `json:"capabilitiesJson" gorm:"type:text"`
	ProtocolsJSON    string         `json:"protocolsJson" gorm:"type:text"`
	DocsURL          string         `json:"docsUrl" gorm:"size:1000"`
	Enabled          bool           `json:"enabled" gorm:"index"`
	SortOrder        int            `json:"sortOrder" gorm:"index"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
	DeletedAt        gorm.DeletedAt `json:"-" gorm:"index"`
}

func (ModelVendor) TableName() string { return "model_vendors" }

// VendorCredential 是厂商下的一个接入点。
//
// 密钥本体只存在 model_channels（沿用它的加密与脱敏），这里只保留指针与展示用的
// 尾号：同一条密钥在两处各存一份，任何一次轮换都会有一边忘掉，而“忘掉”的那一边
// 通常正是线上真正生效的那一份。
//
// ChannelID 是唯一的关联键，且带唯一索引：一条渠道只能挂在一个厂商下，否则
// “这个厂商下有几条凭据”会数出重复值，凭据删除也会互相踩到对方的渠道。
type VendorCredential struct {
	ID            string         `json:"id" gorm:"primaryKey;size:64"`
	VendorID      string         `json:"vendorId" gorm:"index;size:64"`
	Name          string         `json:"name" gorm:"size:80"`
	ChannelID     string         `json:"channelId" gorm:"uniqueIndex;size:64"`
	KeyHint       string         `json:"keyHint" gorm:"size:16"`
	BaseURL       string         `json:"baseUrl"`
	APIFormat     string         `json:"apiFormat" gorm:"size:24"`
	Enabled       bool           `json:"enabled" gorm:"index"`
	Weight        int            `json:"weight" gorm:"not null;default:100"`
	LastError     string         `json:"lastError" gorm:"type:text"`
	LastCheckedAt *time.Time     `json:"lastCheckedAt"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
}

func (VendorCredential) TableName() string { return "vendor_credentials" }
