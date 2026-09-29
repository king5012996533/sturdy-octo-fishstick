package model

import "time"

// CreationInspirationStatus 是精选灵感的上架状态（取值冻结）。
//
// 与画布模板同构：灵感广场是运营维护的平台内容，没有草稿态——运营改完即上架，
// 下线只是撤出前台广场，数据保留可随时恢复或改写。
type CreationInspirationStatus string

const (
	// CreationInspirationOnline 表示已上架：出现在前台精选灵感广场。
	CreationInspirationOnline CreationInspirationStatus = "ONLINE"
	// CreationInspirationOffline 表示已下架：后台仍可编辑，前台看不到。
	CreationInspirationOffline CreationInspirationStatus = "OFFLINE"
)

// CreationInspiration 是运营维护的精选灵感条目（托管专属表，落在画布库）。
//
// 为什么要有这张表：广场内容此前硬编码在前端仓库里，换一批素材要发版，运营也没有
// 上下架、排序、推荐位这些手段。落到平台库里之后，"广场上放什么"变成一次后台写操作，
// 与前端发版解耦。
//
// 字段刻意保持"照抄前端卡片所需"：Prompt 原样保存，服务端不解析提示词模板语法
// （那是生成侧的事）；Likes 只做展示，不参与任何排序或计费，否则会变成一条可被刷的
// 业务口径。SourceURL / Source 与 Author 一起决定前台的署名口径：带 SourceURL 的是
// 外部示例素材，带 Source 的是开源提示词改编，两者都为空才是本平台原创。
type CreationInspiration struct {
	ID          string                    `json:"id" gorm:"primaryKey;size:64"`
	Title       string                    `json:"title" gorm:"size:120"`
	Description string                    `json:"description" gorm:"type:text"`
	CoverURL    string                    `json:"coverUrl" gorm:"size:1000"`
	Prompt      string                    `json:"prompt" gorm:"type:text"`
	Mode        string                    `json:"mode" gorm:"size:16;index"`
	Category    string                    `json:"category" gorm:"size:80;index"`
	Author      string                    `json:"author" gorm:"size:80"`
	Likes       int                       `json:"likes"`
	SourceURL   string                    `json:"sourceUrl" gorm:"size:1000"`
	Source      string                    `json:"source" gorm:"size:120"`
	Status      CreationInspirationStatus `json:"status" gorm:"index;size:16"`
	Featured    bool                      `json:"featured" gorm:"index"`
	SortOrder   int                       `json:"sortOrder" gorm:"index"`
	CreatedAt   time.Time                 `json:"createdAt"`
	UpdatedAt   time.Time                 `json:"updatedAt"`
}

func (CreationInspiration) TableName() string { return "creation_inspirations" }
