package repository

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// AdminCreationInspirations 返回全部精选灵感（含已下架），供后台管理使用。
//
// 排序固定为 sort_order 升序、updated_at 降序，与画布模板一致：运营用 sort_order
// 决定前台顺序，同一位置以最近改动者优先，避免两次保存后顺序随机跳变。
func (r *Repository) AdminCreationInspirations() ([]model.CreationInspiration, error) {
	var records []model.CreationInspiration
	if err := r.db.Order("sort_order ASC, updated_at DESC").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// CreationInspirationByID 按主键读取精选灵感。
func (r *Repository) CreationInspirationByID(id string) (*model.CreationInspiration, error) {
	var record model.CreationInspiration
	if err := r.db.First(&record, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// SaveCreationInspiration 落库一条精选灵感。
//
// ID 为空表示新建：主键由序列生成（前缀 INSP），避免把「新建」写成覆盖已有行；
// 非空则按主键整行保存，编辑时连带上架状态、推荐位与排序一起生效。
func (r *Repository) SaveCreationInspiration(record *model.CreationInspiration) error {
	if record == nil {
		return nil
	}
	if strings.TrimSpace(record.ID) == "" {
		id, err := r.NextPrefixedID("INSP")
		if err != nil {
			return err
		}
		record.ID = id
		return r.db.Create(record).Error
	}
	return r.db.Save(record).Error
}

// DeleteCreationInspiration 按主键删除精选灵感；目标不存在时返回 gorm.ErrRecordNotFound。
func (r *Repository) DeleteCreationInspiration(id string) error {
	result := r.db.Delete(&model.CreationInspiration{}, "id = ?", strings.TrimSpace(id))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// CreationInspirationCatalog 返回前台可见的广场条目：只含已上架，且用户投稿必须审核通过。
//
// 可见性判断刻意写成显式条件而不是"来源非空"这类缩写：用户投稿经历了"审核结论"
// 与"运营上下架"两次独立判断，任何一次没通过都不该出现在前台。平台条目（含迁移前的
// 历史行，其 origin 可能仍是空串）走另一支，不受审核状态约束。
func (r *Repository) CreationInspirationCatalog() ([]model.CreationInspiration, error) {
	var records []model.CreationInspiration
	if err := r.db.
		Where("status = ?", model.CreationInspirationOnline).
		// 空串与 NULL 都要算平台条目：迁移前写入的行 origin 可能是空串，
		// 只判 NULL 会让这些历史灵感在前台凭空消失。
		Where("(origin IS NULL OR origin = '' OR origin = ?) OR (origin = ? AND review_status = ?)",
			model.CreationInspirationOriginPlatform,
			model.CreationInspirationOriginUser,
			model.CreationInspirationReviewApproved).
		Order("sort_order ASC, updated_at DESC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// CreationInspirationsByAuthor 返回某个用户的全部投稿（含待审、被驳回），最新在前。
//
// 自己的投稿列表必须带上审核状态与驳回理由，否则用户只知道"没通过"，不知道该改什么。
func (r *Repository) CreationInspirationsByAuthor(userID string) ([]model.CreationInspiration, error) {
	var records []model.CreationInspiration
	if err := r.db.
		Where("origin = ? AND author_user_id = ?", model.CreationInspirationOriginUser, strings.TrimSpace(userID)).
		Order("created_at DESC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// CreationInspirationByAuthorAndID 按作者与主键取一条投稿。
//
// 撤回时必须带上作者条件：只按主键删等于把"删自己那条"变成"删任何人的那条"。
func (r *Repository) CreationInspirationByAuthorAndID(userID string, id string) (*model.CreationInspiration, error) {
	var record model.CreationInspiration
	if err := r.db.
		Where("id = ? AND origin = ? AND author_user_id = ?",
			strings.TrimSpace(id), model.CreationInspirationOriginUser, strings.TrimSpace(userID)).
		First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// CreationInspirationReviewQueue 返回待人工审核的投稿，最早的在前。
//
// 先到先审：投稿队列是运营的待办，按时间正序才不会让人一直看到最新那条而漏掉压在
// 底下的旧投稿。
func (r *Repository) CreationInspirationReviewQueue() ([]model.CreationInspiration, error) {
	var records []model.CreationInspiration
	if err := r.db.
		Where("origin = ? AND review_status = ?", model.CreationInspirationOriginUser, model.CreationInspirationReviewPending).
		Order("created_at ASC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// CountCreationInspirationsByAuthorSince 统计某用户在给定时刻之后提交的投稿数，用于限额。
func (r *Repository) CountCreationInspirationsByAuthorSince(userID string, since time.Time) (int64, error) {
	var count int64
	if err := r.db.Model(&model.CreationInspiration{}).
		Where("origin = ? AND author_user_id = ? AND created_at >= ?",
			model.CreationInspirationOriginUser, strings.TrimSpace(userID), since).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// CountCreationInspirations 统计精选灵感条数，用于首次启动的空表播种判断。
//
// 只用来回答「这张表是不是全新的」：播种必须只在空库发生，否则运营删掉几条种子后
// 下一次重启会把它们原样塞回来，后台的删除操作就变成了假的。
func (r *Repository) CountCreationInspirations() (int64, error) {
	var count int64
	if err := r.db.Model(&model.CreationInspiration{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
