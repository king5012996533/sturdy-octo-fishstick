package skills

import (
	"testing"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 内置技能必须对所有账号默认可用。
//
// 这条行为的反面是线上真实发生过的状态：33 个内置技能在库里躺着，user_skill_states
// 是 0 行，于是 Agent 的技能选择器是空的、skill_read_file 一个技能也读不到。要求用户
// 先手动「加入我的技能」并不产生任何安全价值——它们是随二进制分发的公开内容。
func TestBuiltinSkillsAreUsableWithoutJoining(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+kernel.NewID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Skill{}, &model.UserSkillState{}); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir(), nil)

	skills := []model.Skill{
		{ID: "builtin-1", OwnerID: "content-author", Name: "内置导演", Status: skillStatusEnabled, SourceType: skillSourceTypeBuiltin},
		{ID: "joined-1", OwnerID: "other-author", Name: "手动加入", Status: skillStatusEnabled, SourceType: "github"},
		{ID: "private-1", OwnerID: "other-author", Name: "他人私有", Status: skillStatusEnabled, SourceType: "markdown", IsPrivate: true},
	}
	for index := range skills {
		if err := db.Create(&skills[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.UserSkillState{ID: kernel.NewID(), UserID: "user-1", SkillID: "joined-1", Added: true}).Error; err != nil {
		t.Fatal(err)
	}

	items, err := svc.AddedSkills("user-1")
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]SkillItem, len(items))
	for _, item := range items {
		byID[item.SkillID] = item
	}

	builtin, ok := byID["builtin-1"]
	if !ok {
		t.Fatal("内置技能没有出现在可用技能列表里")
	}
	if !builtin.IsUsable {
		t.Fatal("内置技能应当是可用状态")
	}
	// IsAdded 保持原义：用户没有手动加入过它，卸载仍然是一个有意义的操作。
	if builtin.IsAdded {
		t.Fatal("内置技能不该被记成用户已加入")
	}

	joined, ok := byID["joined-1"]
	if !ok || !joined.IsAdded || !joined.IsUsable {
		t.Fatalf("手动加入的技能状态不对: %#v", joined)
	}

	if _, ok := byID["private-1"]; ok {
		t.Fatal("他人私有技能不该出现在可用技能列表里")
	}
}
