package repository

import (
	"regexp"
	"strings"

	"infinite-canvas/backend/internal/model"
)

// 广场灵感对本地资源的引用。
//
// 这些引用不在用户维度里：灵感是平台内容，参考图挂在 platform-inspiration-* 这类
// 平台账号名下，所以按 user_id 过滤的引用快照一条都扫不到。少了这一步，"未引用素材
// 24 小时回收"会把还在被复刻配方使用的参考图当成残留删掉——广场上那条作品看着正常，
// 用户点复刻却生成不出原作画面。
//
// 三种引用都要认：
//   - resource_id：投稿/封面用的主资源；
//   - recipe_image_ids：复刻配方的参考图，逗号分隔的本地资源 ID；
//   - cover_url / video_url：历史数据里存的是 /api/resources/<id>/file 形式的地址。
func (r *Repository) InspirationResourceReferences(resourceIDs []string) ([]ResourceDirectReference, error) {
	refs := []ResourceDirectReference{}
	if len(resourceIDs) == 0 {
		return refs, nil
	}
	candidates := make(map[string]struct{}, len(resourceIDs))
	for _, id := range resourceIDs {
		if id != "" {
			candidates[id] = struct{}{}
		}
	}
	// 灵感广场只在托管 schema 里建表。本地/桌面构建没有这张表，
	// 直接查会报 "no such table" 并让整轮清理失败——本地加载器必须整轮跳过，
	// 而不是把无人引用的素材永远留着。
	if !r.db.Migrator().HasTable(&model.CreationInspiration{}) {
		return refs, nil
	}
	var rows []model.CreationInspiration
	if err := r.db.Select("id", "title", "cover_url", "video_url", "resource_id", "recipe_image_ids").Find(&rows).Error; err != nil {
		return refs, err
	}
	for _, row := range rows {
		for _, resourceID := range inspirationResourceIDs(row, candidates) {
			refs = append(refs, ResourceDirectReference{Kind: "灵感广场", ID: row.ID, Title: row.Title, ResourceID: resourceID})
		}
	}
	return refs, nil
}

func inspirationResourceIDs(row model.CreationInspiration, candidates map[string]struct{}) []string {
	matched := make([]string, 0, 2)
	seen := map[string]struct{}{}
	record := func(id string) {
		if _, ok := candidates[id]; !ok {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		matched = append(matched, id)
	}
	record(strings.TrimSpace(row.ResourceID))
	for _, id := range strings.Split(row.RecipeImageIDs, ",") {
		record(strings.TrimSpace(id))
	}
	for _, id := range resourceIDsInURL(row.CoverURL, candidates) {
		record(id)
	}
	for _, id := range resourceIDsInURL(row.VideoURL, candidates) {
		record(id)
	}
	return matched
}

var resourceFilePathPattern = regexp.MustCompile(`/resources/([^/?#]+)/file`)

func resourceIDsInURL(value string, candidates map[string]struct{}) []string {
	if value == "" {
		return nil
	}
	matched := []string{}
	for _, group := range resourceFilePathPattern.FindAllStringSubmatch(value, -1) {
		if _, ok := candidates[group[1]]; ok {
			matched = append(matched, group[1])
		}
	}
	return matched
}
