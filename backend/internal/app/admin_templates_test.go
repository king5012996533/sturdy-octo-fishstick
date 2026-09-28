package app

import (
	"errors"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// templateStatus 取错误里的 HTTP 状态，非结构化错误统一按 500 处理。
func templateStatus(err error) int {
	if err == nil {
		return 0
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Status
	}
	return 500
}

func newTemplateTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	// 画布模板、审计与 ID 序列都要建：新建模板会走 NextPrefixedID 生成主键。
	if err := db.AutoMigrate(&model.CanvasTemplate{}, &model.AdminAuditEvent{}, &model.IDSequence{}); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir()), db
}

// TestCanvasTemplateSaveValidation 覆盖标识、名称与状态的输入校验。
func TestCanvasTemplateSaveValidation(t *testing.T) {
	svc, _ := newTemplateTestService(t)
	cases := []struct {
		name  string
		input CanvasTemplateInput
	}{
		{"大写标识被拒", CanvasTemplateInput{Code: "Pro-Monthly", Name: "示例", Status: "ONLINE"}},
		{"下划线标识被拒", CanvasTemplateInput{Code: "pro_monthly", Name: "示例", Status: "ONLINE"}},
		{"空标识被拒", CanvasTemplateInput{Code: "   ", Name: "示例", Status: "ONLINE"}},
		{"超长标识被拒", CanvasTemplateInput{Code: strings.Repeat("a", canvasTemplateCodeMaxLen+1), Name: "示例", Status: "ONLINE"}},
		{"空名称被拒", CanvasTemplateInput{Code: "valid-code", Name: "   ", Status: "ONLINE"}},
		{"超长名称被拒", CanvasTemplateInput{Code: "valid-code", Name: strings.Repeat("名", canvasTemplateNameMaxLen+1), Status: "ONLINE"}},
		{"非法状态被拒", CanvasTemplateInput{Code: "valid-code", Name: "示例", Status: "DRAFT"}},
		{"空状态被拒", CanvasTemplateInput{Code: "valid-code", Name: "示例", Status: ""}},
	}
	for _, tc := range cases {
		if _, err := svc.SaveCanvasTemplate(tc.input); templateStatus(err) != 400 {
			t.Fatalf("%s：期望 400，实际 %v", tc.name, err)
		}
	}
}

// TestCanvasTemplateSaveConflictAndSelfEdit 覆盖唯一性冲突与自身编辑不误判。
func TestCanvasTemplateSaveConflictAndSelfEdit(t *testing.T) {
	svc, _ := newTemplateTestService(t)
	first, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "drama", Name: "短剧分镜", Status: "ONLINE"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.Category != canvasTemplateDefaultCategory {
		t.Fatalf("新建应生成主键并落入默认分类：%#v", first)
	}

	// 同 code 新建 → 409。
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "drama", Name: "另一个", Status: "ONLINE"}); templateStatus(err) != 409 {
		t.Fatalf("重复标识应返回 409，实际 %v", err)
	}

	// 编辑自身沿用同一 code，不应被判成冲突。
	updated, err := svc.SaveCanvasTemplate(CanvasTemplateInput{ID: first.ID, Code: "drama", Name: "短剧分镜 v2", Status: "OFFLINE"})
	if err != nil {
		t.Fatalf("编辑自身不应报冲突：%v", err)
	}
	if updated.ID != first.ID || updated.Name != "短剧分镜 v2" || updated.Status != "OFFLINE" {
		t.Fatalf("编辑结果不符：%#v", updated)
	}

	// 编辑成他人已占用的 code → 409。
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "comic", Name: "漫画", Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{ID: first.ID, Code: "comic", Name: "短剧分镜 v2", Status: "ONLINE"}); templateStatus(err) != 409 {
		t.Fatalf("占用他人标识应返回 409，实际 %v", err)
	}

	// 编辑不存在的 id → 404，而不是静默新建。
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{ID: "ctpl-missing", Code: "fresh", Name: "新模板", Status: "ONLINE"}); templateStatus(err) != 404 {
		t.Fatalf("编辑不存在的模板应返回 404，实际 %v", err)
	}
	// 顺带确认不带 ID 时能新建，避免上面的 404 被误读成"必须带 ID"。
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "fresh", Name: "新模板", Status: "ONLINE"}); err != nil {
		t.Fatalf("不带 ID 应能新建：%v", err)
	}
}

// TestCanvasTemplateCatalogAndOrdering 覆盖上下架过滤、推荐位与排序。
func TestCanvasTemplateCatalogAndOrdering(t *testing.T) {
	svc, _ := newTemplateTestService(t)
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "offline", Name: "已下架", Status: "OFFLINE", SortOrder: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "second", Name: "第二", Status: "ONLINE", SortOrder: 2, Featured: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "first", Name: "第一", Status: "ONLINE", SortOrder: 1}); err != nil {
		t.Fatal(err)
	}

	all, err := svc.AdminCanvasTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if codes := templateCodes(all); strings.Join(codes, ",") != "offline,first,second" {
		t.Fatalf("后台列表应按 sort_order 升序：%v", codes)
	}

	catalog, err := svc.CanvasTemplateCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if codes := templateCodes(catalog); strings.Join(codes, ",") != "first,second" {
		t.Fatalf("目录应只含已上架且按 sort_order 升序：%v", codes)
	}
	if !catalog[1].Featured {
		t.Fatalf("推荐位标记应原样返回：%#v", catalog[1])
	}

	// 下架一个后目录立刻少一个。
	if _, err := svc.SaveCanvasTemplate(CanvasTemplateInput{ID: idOf(catalog, "first"), Code: "first", Name: "第一", Status: "OFFLINE", SortOrder: 1}); err != nil {
		t.Fatal(err)
	}
	catalog, err = svc.CanvasTemplateCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if codes := templateCodes(catalog); strings.Join(codes, ",") != "second" {
		t.Fatalf("下架后目录应只剩已上架的：%v", codes)
	}
}

// TestCanvasTemplateDeleteLifecycle 覆盖删除后列表变化与不存在目标的报错。
func TestCanvasTemplateDeleteLifecycle(t *testing.T) {
	svc, _ := newTemplateTestService(t)
	first, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "one", Name: "一号", Status: "ONLINE"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.SaveCanvasTemplate(CanvasTemplateInput{Code: "two", Name: "二号", Status: "ONLINE"})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := svc.AdminCanvasTemplates(); err != nil || len(list) != 2 {
		t.Fatalf("初始列表应为 2 条：%v %v", list, err)
	}

	if err := svc.DeleteCanvasTemplate(first.ID); err != nil {
		t.Fatal(err)
	}
	list, err := svc.AdminCanvasTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("删除后列表应只剩二号：%#v", list)
	}

	if err := svc.DeleteCanvasTemplate("ctpl-not-exist"); templateStatus(err) != 404 {
		t.Fatalf("删除不存在的模板应返回 404，实际 %v", err)
	}
	if err := svc.DeleteCanvasTemplate("  "); templateStatus(err) != 400 {
		t.Fatalf("缺少标识应返回 400，实际 %v", err)
	}
}

func templateCodes(views []CanvasTemplateView) []string {
	codes := make([]string, 0, len(views))
	for _, view := range views {
		codes = append(codes, view.Code)
	}
	return codes
}

func idOf(views []CanvasTemplateView, code string) string {
	for _, view := range views {
		if view.Code == code {
			return view.ID
		}
	}
	return ""
}
