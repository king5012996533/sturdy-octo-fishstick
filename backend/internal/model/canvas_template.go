package model

import "time"

// CanvasTemplateStatus 是画布模板的上架状态（取值冻结）。
//
// 只有「已上架 / 已下架」两个状态：模板是运营维护的平台内容，不存在草稿态，
// 运营准备好即直接上架，下线也只是撤出前台目录，数据保留可随时恢复。
type CanvasTemplateStatus string

const (
	// CanvasTemplateOnline 表示已上架：出现在用户端模板目录里。
	CanvasTemplateOnline CanvasTemplateStatus = "ONLINE"
	// CanvasTemplateOffline 表示已下架：后台仍可编辑，但用户端看不到。
	CanvasTemplateOffline CanvasTemplateStatus = "OFFLINE"
)

// CanvasTemplate 是平台运营维护的画布模板（托管专属表，落在画布库）。
//
// 刻意与提示词模板（prompt_templates）分开：那是用户自己的提示词配置，这是平台
// 内容运营的素材库，两者的生命周期、备份与权限边界完全不同，共用一张表只会让
// 「谁有权改」变得说不清。
//
// PayloadJSON 原样保存画布快照：后台只负责搬运内容，不解析节点结构——解析规则
// 属于画布客户端，写进这里会让模板随画布协议升级而失效。
type CanvasTemplate struct {
	ID          string               `json:"id" gorm:"primaryKey;size:64"`
	Code        string               `json:"code" gorm:"uniqueIndex;size:64"`
	Name        string               `json:"name" gorm:"size:80"`
	Description string               `json:"description" gorm:"type:text"`
	Category    string               `json:"category" gorm:"size:80;index"`
	CoverURL    string               `json:"coverUrl" gorm:"size:1000"`
	PayloadJSON string               `json:"payloadJson" gorm:"type:text"`
	Status      CanvasTemplateStatus `json:"status" gorm:"index;size:16"`
	Featured    bool                 `json:"featured" gorm:"index"`
	SortOrder   int                  `json:"sortOrder" gorm:"index"`
	CreatedAt   time.Time            `json:"createdAt"`
	UpdatedAt   time.Time            `json:"updatedAt"`
}

func (CanvasTemplate) TableName() string { return "canvas_templates" }
