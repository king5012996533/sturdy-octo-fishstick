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

// CreationInspirationOrigin 标识广场条目的来源（取值冻结）。
//
// PLATFORM 是运营手工录入的平台内容；USER 是用户从自己的生成产物投稿上来的。
// 两者放在同一张表而不是两张，是为了让前台的排序、筛选与"一键复用"只有一套实现：
// 所谓"精选"本质上就是运营把某条投稿置顶，用两个实体表达同一件事迟早要写同步逻辑。
type CreationInspirationOrigin string

const (
	// CreationInspirationOriginPlatform 表示运营维护的平台条目。
	CreationInspirationOriginPlatform CreationInspirationOrigin = "PLATFORM"
	// CreationInspirationOriginUser 表示用户投稿的条目。
	CreationInspirationOriginUser CreationInspirationOrigin = "USER"
)

// CreationInspirationReviewStatus 是投稿的审核结论，与上架状态是两个维度。
//
// Status 回答"运营要不要把它挂在广场上"，ReviewStatus 回答"内容本身能不能过"。
// 合成一个字段会让"审核通过的条目被运营下架"退化成删除，恢复时只能靠记忆。
type CreationInspirationReviewStatus string

const (
	// CreationInspirationReviewApproved 表示审核通过（平台条目恒为此值）。
	CreationInspirationReviewApproved CreationInspirationReviewStatus = "APPROVED"
	// CreationInspirationReviewPending 表示待人工审核。
	CreationInspirationReviewPending CreationInspirationReviewStatus = "PENDING"
	// CreationInspirationReviewRejected 表示审核驳回（含关键词预筛直接拦下）。
	CreationInspirationReviewRejected CreationInspirationReviewStatus = "REJECTED"
)

// CreationInspiration 是灵感广场条目（托管专属表，落在画布库）：运营录入的平台内容与用户投稿共用。
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
	// 以下字段是"用户投稿"引入的：平台条目下 Origin 为 PLATFORM、
	// AuthorUserID 为空、ReviewStatus 恒为 APPROVED。
	Origin       CreationInspirationOrigin       `json:"origin" gorm:"size:16;index"`
	AuthorUserID string                          `json:"authorUserId" gorm:"index;size:64"`
	ResourceID   string                          `json:"resourceId" gorm:"size:64"`
	ReviewStatus CreationInspirationReviewStatus `json:"reviewStatus" gorm:"index;size:16"`
	// ReviewNote 是驳回理由原文，会回显给投稿人；通过时为空。
	ReviewNote string     `json:"reviewNote" gorm:"type:text"`
	ReviewedAt *time.Time `json:"reviewedAt"`
	// ReuseCount 由服务端在真实生成成功时累加，不接受客户端上报，
	// 否则它会变成一个和 Likes 一样的、可以随便刷的展示数字。
	ReuseCount int       `json:"reuseCount"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (CreationInspiration) TableName() string { return "creation_inspirations" }
