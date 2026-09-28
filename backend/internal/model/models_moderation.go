package model

import "time"

// CanvasModerationStatus 是平台对画布内容的处置状态。
//
// 与"用户自己删画布"分开：用户删除是所有权动作，这里记录的是平台依据审核结论
// 对内容的处置，必须能回答"谁在什么时候以什么理由动的"。
type CanvasModerationStatus string

const (
	// CanvasModerationNormal 表示审核通过或已恢复。
	CanvasModerationNormal CanvasModerationStatus = "NORMAL"
	// CanvasModerationHidden 表示下架：用户不可读、不可写，但数据保留，可恢复。
	CanvasModerationHidden CanvasModerationStatus = "HIDDEN"
	// CanvasModerationRemoved 表示判定违规并移除：与下架一样不可见，
	// 但在界面上语义更重。数据同样保留，避免误判之后无法举证。
	CanvasModerationRemoved CanvasModerationStatus = "REMOVED"
)

// CanvasModeration 是画布审核状态（托管专属表）。
//
// 刻意不挂在 canvas_projects 上：画布表在桌面端也要用，而审核是平台能力；
// 混在一起会让桌面产物必须携带一个它永远写不到的状态列。
type CanvasModeration struct {
	CanvasID    string                 `json:"canvasId" gorm:"primaryKey;size:80"`
	UserID      string                 `json:"userId" gorm:"index;size:36"`
	Status      CanvasModerationStatus `json:"status" gorm:"index;size:24"`
	Reason      string                 `json:"reason" gorm:"size:500"`
	ActorUserID string                 `json:"actorUserId" gorm:"size:36"`
	CreatedAt   time.Time              `json:"createdAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
}

func (CanvasModeration) TableName() string { return "canvas_moderation" }
