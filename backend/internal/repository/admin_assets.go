package repository

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// AdminAssetFilter 是管理端素材列表的筛选条件。
type AdminAssetFilter struct {
	Keyword string
	// Status 取 NORMAL/HIDDEN/REMOVED；空表示不限。
	Status   string
	Kind     string
	UserID   string
	Page     int
	PageSize int
}

// AdminAssetRow 是管理端素材列表的一行。
//
// PayloadBytes 是占用字节的近似值：assets 表没有文件大小字段，因此在 SQL 里用
// payload_json 与素材各版本 definition_json 的字符长度相加作为口径。它不等于对象
// 存储里真实占用的字节数——同一份物理对象被多次引用也不会去重，详见
// adminAssetBytesExpr 的注释。
//
// PayloadJSON 是素材定义的原文：管理端要看素材本体，而本体只以资源引用的形式记在
// 这里，所以列表也要把它带出来交给 app 层解析。它只服务于预览，不进对外视图。
type AdminAssetRow struct {
	ID               string
	UserID           string
	UserName         string
	Kind             string
	Category         string
	Status           string
	Title            string
	VersionCount     int64
	PayloadBytes     int64
	PayloadJSON      string
	ModerationStatus string
	ModerationReason string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AdminAssetTotals 是素材资源管理的顶部读数。
//
// TotalBytes 与 AdminAssetRow.PayloadBytes 同口径：字符长度近似值，不是真实对象
// 存储用量；Hidden/Removed 只统计仍存在的素材，避免历史处置行把计数撑高。
type AdminAssetTotals struct {
	Total      int64
	Hidden     int64
	Removed    int64
	TotalBytes int64
	Users      int64
}

// adminAssetBytesExpr 是"占用字节"的近似表达式。
//
// assets 没有 size 列，这里取素材自身 payload_json 的长度，加上它全部版本
// definition_json 的长度之和。字符数 / 字节数在 UTF-8 中文下并不相等，也不做对象
// 存储去重，所以它只能用于排序和概览，不能当成账单口径。
const adminAssetBytesExpr = `LENGTH(COALESCE(assets.payload_json, '')) + COALESCE((SELECT SUM(LENGTH(COALESCE(asset_versions.definition_json, ''))) FROM asset_versions WHERE asset_versions.asset_id = assets.id), 0)`

// adminAssetSelectColumns 是列表与详情共用的列集合。
//
// moderation_status / moderation_reason 用 COALESCE 兜底成空串：没有审核行即"正常"，
// 交给上层翻译，SQL 里不出现第二套状态常量。
const adminAssetSelectColumns = `assets.id AS id,
	assets.user_id AS user_id,
	COALESCE(workspaces.name, '') AS user_name,
	assets.kind AS kind,
	assets.category AS category,
	assets.status AS status,
	assets.title AS title,
	(SELECT COUNT(*) FROM asset_versions WHERE asset_versions.asset_id = assets.id) AS version_count,
	` + adminAssetBytesExpr + ` AS payload_bytes,
	COALESCE(assets.payload_json, '') AS payload_json,
	COALESCE(asset_moderation.status, '') AS moderation_status,
	COALESCE(asset_moderation.reason, '') AS moderation_reason,
	assets.created_at AS created_at,
	assets.updated_at AS updated_at`

// adminAssetBaseQuery 构造列表与详情的公共查询。
//
// asset_moderation 与素材同库（不像画布审核那样是托管专属表），因此可以放心写进
// SQL；owner 昵称取画布库的 workspaces.name——它与 user_id 同键，且桌面形态也存在。
//
// 这里刻意不含审核状态条件：详情要能读到已被处置的素材，状态筛选只属于列表。
func (r *Repository) adminAssetBaseQuery(filter AdminAssetFilter) *gorm.DB {
	query := r.db.Model(&model.Asset{}).
		Joins("LEFT JOIN workspaces ON workspaces.id = assets.user_id").
		Joins("LEFT JOIN asset_moderation ON asset_moderation.asset_id = assets.id")
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("assets.title LIKE ? OR assets.id LIKE ? OR assets.user_id = ? OR workspaces.name LIKE ?", like, like, keyword, like)
	}
	if kind := strings.TrimSpace(filter.Kind); kind != "" {
		query = query.Where("assets.kind = ?", kind)
	}
	if userID := strings.TrimSpace(filter.UserID); userID != "" {
		query = query.Where("assets.user_id = ?", userID)
	}
	return query
}

// adminAssetQuery 在公共查询上叠加列表的状态筛选。
func (r *Repository) adminAssetQuery(filter AdminAssetFilter) *gorm.DB {
	query := r.adminAssetBaseQuery(filter)
	switch status := strings.ToUpper(strings.TrimSpace(filter.Status)); status {
	case "", string(model.AssetModerationNormal):
		// "正常"包含两类：没有审核行的素材，以及被恢复过的素材。
		query = query.Where("COALESCE(asset_moderation.status, ?) = ?", string(model.AssetModerationNormal), string(model.AssetModerationNormal))
	case string(model.AssetModerationHidden), string(model.AssetModerationRemoved):
		query = query.Where("asset_moderation.status = ?", status)
	default:
		// 未知状态不返回任何行，而不是静默忽略筛选条件。
		query = query.Where("1 = 0")
	}
	return query
}

// AdminAssetPage 返回分页后的素材列表与总数。
//
// 返回的切片恒为非 nil：空结果要能序列化成 []，前端不必再为 null 兜底。
func (r *Repository) AdminAssetPage(filter AdminAssetFilter) ([]AdminAssetRow, int64, error) {
	var total int64
	if err := r.adminAssetQuery(filter).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AdminAssetRow, 0)
	query := r.adminAssetQuery(filter).
		Select(adminAssetSelectColumns).
		Order("assets.updated_at DESC, assets.id DESC")
	if filter.PageSize > 0 {
		page := filter.Page
		if page < 1 {
			page = 1
		}
		query = query.Limit(filter.PageSize).Offset((page - 1) * filter.PageSize)
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// AdminAsset 按主键读取单条素材，不限定所有者。不存在时返回 gorm.ErrRecordNotFound。
func (r *Repository) AdminAsset(id string) (*AdminAssetRow, error) {
	var row AdminAssetRow
	if err := r.adminAssetBaseQuery(AdminAssetFilter{}).
		Select(adminAssetSelectColumns).
		Where("assets.id = ?", strings.TrimSpace(id)).
		Limit(1).
		Scan(&row).Error; err != nil {
		return nil, err
	}
	// Scan 不会给出 ErrRecordNotFound，需要按主键列是否读到来判定。
	if row.ID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

// AdminAssetTotals 汇总素材规模与处置计数。
func (r *Repository) AdminAssetTotals() (AdminAssetTotals, error) {
	var totals AdminAssetTotals
	if err := r.db.Model(&model.Asset{}).Count(&totals.Total).Error; err != nil {
		return totals, err
	}
	countByStatus := func(status model.AssetModerationStatus) (int64, error) {
		var count int64
		err := r.db.Table("asset_moderation").
			Joins("JOIN assets ON assets.id = asset_moderation.asset_id").
			Where("asset_moderation.status = ?", status).
			Count(&count).Error
		return count, err
	}
	var err error
	if totals.Hidden, err = countByStatus(model.AssetModerationHidden); err != nil {
		return totals, err
	}
	if totals.Removed, err = countByStatus(model.AssetModerationRemoved); err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.Asset{}).
		Select("COALESCE(SUM(" + adminAssetBytesExpr + "), 0)").
		Scan(&totals.TotalBytes).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.Asset{}).Distinct("user_id").Count(&totals.Users).Error; err != nil {
		return totals, err
	}
	return totals, nil
}

// AssetModerationsByIDs 批量读取素材的审核状态，避免列表逐行查询。
func (r *Repository) AssetModerationsByIDs(ids []string) (map[string]model.AssetModeration, error) {
	result := make(map[string]model.AssetModeration, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var records []model.AssetModeration
	if err := r.db.Where("asset_id IN ?", ids).Find(&records).Error; err != nil {
		return nil, err
	}
	for _, record := range records {
		result[record.AssetID] = record
	}
	return result, nil
}

// UpsertAssetModeration 写入或覆盖一条素材处置结论。
//
// 恢复（NORMAL）同样落一行，而不是删掉旧结论：审核历史必须能回答"这条素材被处理
// 过几次、分别是谁做的"，删行等于把前一次处置从证据链里抽走。
func (r *Repository) UpsertAssetModeration(record *model.AssetModeration) error {
	if record == nil {
		return nil
	}
	return r.db.Save(record).Error
}
