package repository

import (
	"testing"

	"infinite-canvas/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 广场灵感是按平台账号存参考图的，用户维度的引用快照扫不到它们。
// 少了这条引用，"未引用素材 24 小时回收"会把复刻配方还在用的图删掉。
func TestInspirationResourceReferencesKeepRecipeImagesAlive(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:inspiration-resource-refs?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CreationInspiration{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.CreationInspiration{
		{ID: "insp-recipe", Title: "复刻配方", RecipeImageIDs: "ref-a, ref-b"},
		{ID: "insp-cover", Title: "封面引用", CoverURL: "/api/resources/ref-c/file"},
		{ID: "insp-main", Title: "主资源引用", ResourceID: "ref-d"},
		{ID: "insp-unrelated", Title: "无关作品", RecipeImageIDs: "other", CoverURL: "https://cdn.example/a.png"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	refs, err := New(db).InspirationResourceReferences([]string{"ref-a", "ref-b", "ref-c", "ref-d", "unused"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, ref := range refs {
		if ref.Kind != "灵感广场" {
			t.Fatalf("kind = %q", ref.Kind)
		}
		got[ref.ResourceID] = ref.ID
	}
	for resourceID, inspirationID := range map[string]string{"ref-a": "insp-recipe", "ref-b": "insp-recipe", "ref-c": "insp-cover", "ref-d": "insp-main"} {
		if got[resourceID] != inspirationID {
			t.Fatalf("resource %s -> %q, want %q (all: %#v)", resourceID, got[resourceID], inspirationID, got)
		}
	}
	if _, exists := got["unused"]; exists {
		t.Fatalf("unrelated resource reported as referenced: %#v", got)
	}
}

func TestInspirationResourceReferencesIgnoreUnmatchedCandidates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:inspiration-resource-refs-empty?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CreationInspiration{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CreationInspiration{ID: "insp", Title: "x", RecipeImageIDs: "ref-a", CoverURL: "/api/resources/ref-b/file"}).Error; err != nil {
		t.Fatal(err)
	}
	refs, err := New(db).InspirationResourceReferences([]string{"ref-z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("unexpected references: %#v", refs)
	}
	empty, err := New(db).InspirationResourceReferences(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty candidates = %#v %v", empty, err)
	}
}

// 本地/桌面 schema 没有 creation_inspirations。查询必须整轮跳过，
// 而不是报错让引用校验失败、把清理任务一起拖死。
func TestInspirationResourceReferencesSkipWhenTableMissing(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:inspiration-resource-refs-noschema?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := New(db).InspirationResourceReferences([]string{"ref-a"})
	if err != nil {
		t.Fatalf("missing table must not fail the reference snapshot: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("unexpected references: %#v", refs)
	}
}
