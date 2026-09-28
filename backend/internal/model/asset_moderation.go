package model

import "time"

// AssetModerationStatus 是平台对用户素材的处置状态。
//
// 与"用户自己删素材"分开：用户删除是所有权动作，这里记录的是平台依据审核结论
// 对素材的处置，必须能回答"谁在什么时候以什么理由动的"。
type AssetModerationStatus string

const (
	// AssetModerationNormal 表示审核通过或已恢复。
	AssetModerationNormal AssetModerationStatus = "NORMAL"
	// AssetModerationHidden 表示隐藏：前台不再展示，但数据保留，可恢复。
	AssetModerationHidden AssetModerationStatus = "HIDDEN"
	// AssetModerationRemoved 表示判定违规并删除：与隐藏一样不再展示，语义更重。
	// 这里只标记状态，不真正清理对象存储文件。
	AssetModerationRemoved AssetModerationStatus = "REMOVED"
)

// AssetModeration 是素材审核状态。
//
// 这张表落在画布库（与 assets 同库），而不是画布审核所在的托管专属表：素材处置
// 的读路径与素材列表同库，才能在一次分页查询里把状态读出来。
type AssetModeration struct {
	AssetID   string                `json:"assetId" gorm:"primaryKey;size:80"`
	Status    AssetModerationStatus `json:"status" gorm:"index;size:24"`
	Reason    string                `json:"reason" gorm:"size:500"`
	ActorID   string                `json:"actorId" gorm:"size:36"`
	CreatedAt time.Time             `json:"createdAt"`
	UpdatedAt time.Time             `json:"updatedAt"`
}

func (AssetModeration) TableName() string { return "asset_moderation" }
