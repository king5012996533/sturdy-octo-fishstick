package app

import (
	"reflect"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newInspirationTestService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	// 灵感、审计与 ID 序列都要建：新建灵感会走 NextPrefixedID 生成主键。
	if err := db.AutoMigrate(&model.CreationInspiration{}, &model.AdminAuditEvent{}, &model.IDSequence{}); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir())
}

// TestCreationInspirationSaveValidation 覆盖标题、模式与状态的输入校验。
func TestCreationInspirationSaveValidation(t *testing.T) {
	svc := newInspirationTestService(t)
	cases := []struct {
		name  string
		input CreationInspirationInput
	}{
		{"空标题被拒", CreationInspirationInput{Title: "   ", Mode: "video", Status: "ONLINE"}},
		{"超长标题被拒", CreationInspirationInput{Title: strings.Repeat("灵", creationInspirationTitleMaxLen+1), Mode: "video", Status: "ONLINE"}},
		{"非法模式被拒", CreationInspirationInput{Title: "示例", Mode: "agent", Status: "ONLINE"}},
		{"空模式被拒", CreationInspirationInput{Title: "示例", Mode: "", Status: "ONLINE"}},
		{"非法状态被拒", CreationInspirationInput{Title: "示例", Mode: "video", Status: "DRAFT"}},
		{"空状态被拒", CreationInspirationInput{Title: "示例", Mode: "video", Status: ""}},
	}
	for _, tc := range cases {
		if _, err := svc.SaveCreationInspiration(tc.input); templateStatus(err) != 400 {
			t.Fatalf("%s：期望 400，实际 %v", tc.name, err)
		}
	}
}

// TestCreationInspirationSaveNormalizes 覆盖落库前的归一：默认分类、模式小写、负点赞归零。
func TestCreationInspirationSaveNormalizes(t *testing.T) {
	svc := newInspirationTestService(t)
	created, err := svc.SaveCreationInspiration(CreationInspirationInput{
		Title:       "  雨夜霓虹  ",
		Description: strings.Repeat("描", creationInspirationDescriptionMaxLen+10),
		Prompt:      strings.Repeat("词", creationInspirationPromptMaxLen+10),
		Mode:        "VIDEO",
		Category:    "  ",
		Likes:       -12,
		Status:      "online",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatalf("新建应生成主键：%#v", created)
	}
	if created.Title != "雨夜霓虹" || created.Category != creationInspirationDefaultCategory {
		t.Fatalf("标题去空格或默认分类不符：%#v", created)
	}
	if created.Mode != "video" || created.Status != "ONLINE" {
		t.Fatalf("模式应归一为小写、状态应归一为大写：%#v", created)
	}
	if created.Likes != 0 {
		t.Fatalf("负点赞应归零，实际 %d", created.Likes)
	}
	// 截断保留上限内的前缀，并允许尾部追加省略号（kernel.TruncateRunes 的既有行为）。
	if !strings.HasPrefix(created.Description, strings.Repeat("描", creationInspirationDescriptionMaxLen)) || len([]rune(created.Description)) > creationInspirationDescriptionMaxLen+3 {
		t.Fatalf("说明应按上限截断：%d", len([]rune(created.Description)))
	}
	if !strings.HasPrefix(created.Prompt, strings.Repeat("词", creationInspirationPromptMaxLen)) || len([]rune(created.Prompt)) > creationInspirationPromptMaxLen+3 {
		t.Fatalf("提示词应按上限截断：%d", len([]rune(created.Prompt)))
	}
}

// TestCreationInspirationSaveCardMeta 覆盖卡片元数据的落库与回读：时长只去空白，
// 标签压成逗号分隔串存一个列，但接口吐出来的必须还是数组——存储格式不能漏给前台。
func TestCreationInspirationSaveCardMeta(t *testing.T) {
	svc := newInspirationTestService(t)
	created, err := svc.SaveCreationInspiration(CreationInspirationInput{
		Title:    "星际边境",
		Mode:     "video",
		Status:   "ONLINE",
		Duration: "  01:42  ",
		Tags:     []string{"科幻", "  ", "科幻", "冒险", "史诗", "群像"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Duration != "01:42" {
		t.Fatalf("时长应去掉首尾空白，实际 %q", created.Duration)
	}
	if want := []string{"科幻", "冒险", "史诗", "群像"}; !reflect.DeepEqual(created.Tags, want) {
		t.Fatalf("标签应去重并按上限截断，实际 %v，want %v", created.Tags, want)
	}

	// 后台列表与前台目录走同一条视图映射，这里读后台列表即可确认回读形状。
	listed, err := svc.AdminCreationInspirations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listed {
		if item.ID != created.ID {
			continue
		}
		if item.Duration != "01:42" || !reflect.DeepEqual(item.Tags, created.Tags) {
			t.Fatalf("回读的卡片元数据不一致：%#v", item)
		}
		return
	}
	t.Fatalf("新建的灵感没有出现在后台列表里：%s", created.ID)
}

// TestCreationInspirationCatalogAndEdit 覆盖上下架过滤、排序与"编辑不存在的 id"。
func TestCreationInspirationCatalogAndEdit(t *testing.T) {
	svc := newInspirationTestService(t)
	if _, err := svc.SaveCreationInspiration(CreationInspirationInput{Title: "已下架", Mode: "text", Status: "OFFLINE", SortOrder: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveCreationInspiration(CreationInspirationInput{Title: "第二", Mode: "video", Status: "ONLINE", SortOrder: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveCreationInspiration(CreationInspirationInput{Title: "第一", Mode: "image", Status: "ONLINE", SortOrder: 1}); err != nil {
		t.Fatal(err)
	}

	catalog, err := svc.CreationInspirationCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 2 || catalog[0].Title != "第一" || catalog[1].Title != "第二" {
		t.Fatalf("前台目录应只含已上架且按排序号升序：%#v", catalog)
	}
	if admin, err := svc.AdminCreationInspirations(); err != nil || len(admin) != 3 {
		t.Fatalf("后台列表应含 3 条：%v %#v", err, admin)
	}

	// 编辑不存在的 id → 404，而不是静默新建。
	if _, err := svc.SaveCreationInspiration(CreationInspirationInput{ID: "INSP-missing", Title: "新", Mode: "video", Status: "ONLINE"}); templateStatus(err) != 404 {
		t.Fatalf("编辑不存在的灵感应返回 404，实际 %v", err)
	}
	if err := svc.DeleteCreationInspiration("   "); templateStatus(err) != 400 {
		t.Fatalf("空标识删除应返回 400，实际 %v", err)
	}
	if err := svc.DeleteCreationInspiration("INSP-missing"); templateStatus(err) != 404 {
		t.Fatalf("删除不存在的灵感应返回 404，实际 %v", err)
	}
}
