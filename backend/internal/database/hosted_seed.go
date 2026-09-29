package database

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

// creationInspirationSeedJSON 是精选灵感广场的首批内容。
//
// 它来自两条已有来源：LibTV 公开作品页的示例素材（封面 + 作者 + 真实提示词），
// 以及本项目自有的原创/开源改编提示词。放在仓库里当种子而不是留在前端常量里，
// 是因为广场内容此后由后台维护——种子只负责让新库开箱就有内容，之后运营改的是库里的行。
//
// 版权口径不在这里改变：示例素材仍带 sourceUrl 与作者署名，前台照旧标注「示例素材」，
// 上线前仍需替换为自有内容或取得授权。
//
//go:embed seeddata/creation_inspirations.json
var creationInspirationSeedJSON []byte

// creationInspirationSeedEntry 与种子里出现的字段一一对应。
//
// 刻意不复用 app 层的入参结构：种子是"已经归一过"的数据（长度都在列宽与上限之内），
// 让它经过一次写接口校验，只会把"数据有问题"变成启动期报错，而这里真正想要的失败
// 是"解析不了就明确报错"，其余交给列约束。
type creationInspirationSeedEntry struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	CoverURL    string `json:"coverUrl"`
	Prompt      string `json:"prompt"`
	Mode        string `json:"mode"`
	Category    string `json:"category"`
	Author      string `json:"author"`
	Likes       int    `json:"likes"`
	SourceURL   string `json:"sourceUrl"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	Featured    bool   `json:"featured"`
	SortOrder   int    `json:"sortOrder"`
}

// SeedCreationInspirations 在精选灵感表为空时灌入种子内容。
//
// 只在空表播种：这张表一旦有行，就说明运营已经接手（或已经播种过），此时再写一次
// 会把后台的删除与下架操作悄悄撤销——"删掉的灵感自己回来了"是最难解释的一类 bug。
func SeedCreationInspirations(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("播种精选灵感：数据库连接为空")
	}
	repo := repository.New(db)
	count, err := repo.CountCreationInspirations()
	if err != nil {
		return fmt.Errorf("统计精选灵感: %w", err)
	}
	if count > 0 {
		return nil
	}

	var entries []creationInspirationSeedEntry
	if err := json.Unmarshal(creationInspirationSeedJSON, &entries); err != nil {
		return fmt.Errorf("解析精选灵感种子: %w", err)
	}
	if len(entries) == 0 {
		// 空种子说明 embed 的路径或内容没进构建产物，静默跳过会让新库永远空着。
		return fmt.Errorf("精选灵感种子为空")
	}

	records := make([]model.CreationInspiration, 0, len(entries))
	// 整批共用一个时间戳：种子的顺序由 sort_order 决定（每条都不同），updated_at 只
	// 作为并列时的兜底，这里没有并发写者，逐个取当前时间只会引入无意义的抖动。
	seededAt := time.Now()
	for index, entry := range entries {
		id, err := repo.NextPrefixedID("INSP")
		if err != nil {
			return fmt.Errorf("生成精选灵感主键: %w", err)
		}
		if strings.TrimSpace(entry.Title) == "" {
			return fmt.Errorf("精选灵感种子第 %d 条缺少标题", index+1)
		}
		records = append(records, model.CreationInspiration{
			ID:          id,
			Title:       entry.Title,
			Description: entry.Description,
			CoverURL:    entry.CoverURL,
			Prompt:      entry.Prompt,
			Mode:        entry.Mode,
			Category:    entry.Category,
			Author:      entry.Author,
			Likes:       entry.Likes,
			SourceURL:   entry.SourceURL,
			Source:      entry.Source,
			Status:      model.CreationInspirationStatus(entry.Status),
			Featured:    entry.Featured,
			SortOrder:   entry.SortOrder,
			CreatedAt:   seededAt,
			UpdatedAt:   seededAt,
		})
	}
	// 整批一次写入：播种只发生在空表上，没有并发写入者，逐条插入只会放大启动耗时。
	if err := db.CreateInBatches(&records, 100).Error; err != nil {
		return fmt.Errorf("写入精选灵感种子: %w", err)
	}
	return nil
}
