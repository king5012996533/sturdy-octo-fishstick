package database

import (
	"encoding/json"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// seedEntryCount 直接解析嵌入的种子文件，避免把条数写死在用例里：内容更新时
// 只需要种子文件改对，用例不会莫名其妙地红。
func seedEntryCount(t *testing.T) int {
	t.Helper()
	var entries []creationInspirationSeedEntry
	if err := json.Unmarshal(creationInspirationSeedJSON, &entries); err != nil {
		t.Fatalf("解析种子失败：%v", err)
	}
	return len(entries)
}

// TestSeedCreationInspirationsFillsEmptyTableOnce 覆盖空表播种、内容可用性与幂等。
func TestSeedCreationInspirationsFillsEmptyTableOnce(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:hosted-seed-once?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CreationInspiration{}, &model.IDSequence{}); err != nil {
		t.Fatal(err)
	}

	if err := SeedCreationInspirations(db); err != nil {
		t.Fatal(err)
	}
	var seeded []model.CreationInspiration
	if err := db.Order("sort_order ASC").Find(&seeded).Error; err != nil {
		t.Fatal(err)
	}
	if len(seeded) != seedEntryCount(t) || len(seeded) < 20 {
		t.Fatalf("播种条数与种子文件不符：%d vs %d", len(seeded), seedEntryCount(t))
	}

	// 每条都必须能被前台直接渲染：标题、封面、提示词、模式与上架状态齐备。
	modes := map[string]bool{"video": true, "image": true, "text": true}
	orders := make(map[int]bool, len(seeded))
	for _, item := range seeded {
		if item.ID == "" || item.Title == "" || item.CoverURL == "" || item.Prompt == "" {
			t.Fatalf("种子条目缺少可渲染字段：%#v", item)
		}
		if !modes[item.Mode] || item.Status != model.CreationInspirationOnline {
			t.Fatalf("种子条目的模式或状态非法：%#v", item)
		}
		if orders[item.SortOrder] {
			t.Fatalf("排序号重复会让前台顺序随机跳变：%d", item.SortOrder)
		}
		orders[item.SortOrder] = true
	}

	// 幂等：非空表不再播种，否则运营删除的内容会在重启后自己回来。
	if err := SeedCreationInspirations(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.CreationInspiration{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if int(count) != len(seeded) {
		t.Fatalf("重复播种不应追加行：%d", count)
	}

	// 全新实例清空后仍应能补回内容（对应"装完就空着"的部署场景）。
	if err := db.Where("1 = 1").Delete(&model.CreationInspiration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := SeedCreationInspirations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CreationInspiration{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if int(count) != len(seeded) {
		t.Fatalf("空表应重新播种：%d", count)
	}
}

// TestSeedCreationInspirationsKeepsProvenance 确认种子保留了外部素材的署名与链接。
//
// 示例素材的版权属于对应作者，前台靠 sourceUrl 才显示「示例素材 · 作者」。种子把
// 这个字段丢掉，会静默把它变成"本平台原创"，这是不能靠肉眼发现的一类回归。
func TestSeedCreationInspirationsKeepsProvenance(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:hosted-seed-provenance?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CreationInspiration{}, &model.IDSequence{}); err != nil {
		t.Fatal(err)
	}
	if err := SeedCreationInspirations(db); err != nil {
		t.Fatal(err)
	}

	var withSource int64
	if err := db.Model(&model.CreationInspiration{}).Where("source_url <> ''").Count(&withSource).Error; err != nil {
		t.Fatal(err)
	}
	if withSource == 0 {
		t.Fatal("种子应保留带原始作品链接的示例素材")
	}
	var missingAuthor int64
	if err := db.Model(&model.CreationInspiration{}).Where("source_url <> '' AND author = ''").Count(&missingAuthor).Error; err != nil {
		t.Fatal(err)
	}
	if missingAuthor != 0 {
		t.Fatalf("带原始链接的条目必须有作者署名，实际缺 %d 条", missingAuthor)
	}
}
