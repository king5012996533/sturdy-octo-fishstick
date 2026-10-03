package repository

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// AdminResourceFilter 是管理端「生成产物」列表的筛选条件。
type AdminResourceFilter struct {
	Keyword string
	Kind    string
	UserID  string
	Since   time.Time
	Until   time.Time
	// UnreferencedOnly 只看「用户没拿到」的产物：既没进素材库、也不在任何画布上。
	// 这正是对账要捞的那批——上游出了结果，客户端却没把它存下来。
	UnreferencedOnly bool
	Page             int
	PageSize         int
}

// AdminResourceRow 是管理端产物列表的一行。
//
// 带完整 Resource 而不是拼几个字段：上层要用它现场签发预览地址，字段少一个就会
// 让签名结果和播放地址对不上。
type AdminResourceRow struct {
	Resource   model.Resource
	UserName   string
	Referenced bool
}

// AdminResourceTotals 是产物管理的顶部读数。口径恒为全量，不随筛选变化——
// 「有多少产物用户根本没拿到」是要看总数的，跟着筛选走反而看不出规模。
type AdminResourceTotals struct {
	Total        int64
	Unreferenced int64
	TotalBytes   int64
	Users        int64
}

// adminResourceReferencedExpr 判定一条产物有没有被用户侧引用。
//
// 这一步没有外键可用：resources 表没有 task_id，素材库和画布的引用只存在于各自的
// payload_json 里，只能按资源 ID 做子串匹配。资源 ID 是 32 位十六进制，误命中的概率
// 可以忽略；代价是每条记录要对 assets 与 canvas_projects 各扫一遍，数据量再涨一个量级
// 时应当改成写入时记引用关系，而不是继续加索引。
const adminResourceReferencedExpr = `(EXISTS (SELECT 1 FROM assets WHERE assets.payload_json LIKE '%' || resources.id || '%')` +
	` OR EXISTS (SELECT 1 FROM canvas_projects WHERE canvas_projects.payload_json LIKE '%' || resources.id || '%'))`

func (r *Repository) adminResourceBaseQuery(filter AdminResourceFilter) *gorm.DB {
	query := r.db.Model(&model.Resource{}).
		Joins("LEFT JOIN workspaces ON workspaces.id = resources.user_id")
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where(
			"resources.id LIKE ? OR resources.user_id = ? OR workspaces.name LIKE ?",
			like, keyword, like,
		)
	}
	if kind := strings.TrimSpace(filter.Kind); kind != "" {
		query = query.Where("resources.kind = ?", kind)
	}
	if userID := strings.TrimSpace(filter.UserID); userID != "" {
		query = query.Where("resources.user_id = ?", userID)
	}
	if !filter.Since.IsZero() {
		query = query.Where("resources.created_at >= ?", filter.Since)
	}
	if !filter.Until.IsZero() {
		query = query.Where("resources.created_at < ?", filter.Until)
	}
	if filter.UnreferencedOnly {
		query = query.Where("NOT " + adminResourceReferencedExpr)
	}
	return query
}

// adminResourceListRow 只是列表查询的中间结果：先取分页所需的键与标记，
// 再按 id 取回完整资源，避免把整段 payload 字段塞进同一次 Scan。
type adminResourceListRow struct {
	ID         string
	UserName   string
	Referenced bool
}

// AdminResourcePage 返回分页后的产物列表与总数。
func (r *Repository) AdminResourcePage(filter AdminResourceFilter) ([]AdminResourceRow, int64, error) {
	var total int64
	if err := r.adminResourceBaseQuery(filter).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	list := make([]adminResourceListRow, 0)
	query := r.adminResourceBaseQuery(filter).
		Select("resources.id AS id, COALESCE(workspaces.name, '') AS user_name, " +
			adminResourceReferencedExpr + " AS referenced").
		Order("resources.created_at DESC, resources.id DESC")
	if filter.PageSize > 0 {
		page := filter.Page
		if page < 1 {
			page = 1
		}
		query = query.Limit(filter.PageSize).Offset((page - 1) * filter.PageSize)
	}
	if err := query.Scan(&list).Error; err != nil {
		return nil, 0, err
	}
	if len(list) == 0 {
		return []AdminResourceRow{}, total, nil
	}

	ids := make([]string, 0, len(list))
	for _, item := range list {
		ids = append(ids, item.ID)
	}
	resources := make([]model.Resource, 0, len(ids))
	if err := r.db.Where("id IN ?", ids).Find(&resources).Error; err != nil {
		return nil, 0, err
	}
	byID := make(map[string]model.Resource, len(resources))
	for _, resource := range resources {
		byID[resource.ID] = resource
	}
	// 按列表顺序回填：IN 查询不保证顺序，直接用它的结果会让分页顺序抖。
	rows := make([]AdminResourceRow, 0, len(list))
	for _, item := range list {
		resource, ok := byID[item.ID]
		if !ok {
			continue
		}
		rows = append(rows, AdminResourceRow{
			Resource: resource, UserName: item.UserName, Referenced: item.Referenced,
		})
	}
	return rows, total, nil
}

// AdminResourceTotals 汇总产物规模与「用户没拿到」的条数。
func (r *Repository) AdminResourceTotals() (AdminResourceTotals, error) {
	var totals AdminResourceTotals
	base := r.db.Model(&model.Resource{})
	if err := base.Count(&totals.Total).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.Resource{}).
		Where("NOT " + adminResourceReferencedExpr).
		Count(&totals.Unreferenced).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.Resource{}).
		Select("COALESCE(SUM(resources.size), 0)").
		Scan(&totals.TotalBytes).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.Resource{}).
		Distinct("user_id").
		Count(&totals.Users).Error; err != nil {
		return totals, err
	}
	return totals, nil
}
