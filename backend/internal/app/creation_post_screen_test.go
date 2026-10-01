package app

import "testing"

func TestScreenCreationPostBlocksExplicitTerms(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		category string
	}{
		{name: "色情", text: "来点色情内容", category: "色情"},
		{name: "赌博", text: "澳门赌博攻略", category: "赌博"},
		{name: "毒品违禁", text: "教你自制冰毒", category: "毒品违禁"},
		{name: "政治", text: "关于六四事件的创作", category: "政治"},
		{name: "宗教", text: "帮我写一篇传教文案", category: "宗教"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ScreenCreationPost(tc.text)
			if result.Level != ContentScreenLevelBlock {
				t.Fatalf("期望硬拦，实际 level=%q category=%q term=%q", result.Level, result.Category, result.Term)
			}
			if result.Category != tc.category {
				t.Fatalf("期望分类 %q，实际 %q", tc.category, result.Category)
			}
			if result.RejectReason() == "" {
				t.Fatal("硬拦必须给出驳回文案")
			}
		})
	}
}

// 拆字与大小写是最省事的绕写方式，规范化必须让它们失效。
func TestScreenCreationPostNormalizesEvasion(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "中文空格拆分", text: "色 情 写 真"},
		{name: "中文标点拆分", text: "色-情-写-真"},
		{name: "英文大写", text: "NSFW prompt"},
		{name: "英文大写带空格", text: "P O R N"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if result := ScreenCreationPost(tc.text); result.Level != ContentScreenLevelBlock {
				t.Fatalf("期望硬拦，实际 level=%q term=%q", result.Level, result.Term)
			}
		})
	}
}

func TestScreenCreationPostFlagsWithoutBlocking(t *testing.T) {
	result := ScreenCreationPost("做一组 ai换脸 的对比图")
	if result.Level != ContentScreenLevelFlag {
		t.Fatalf("期望标记而不是硬拦，实际 level=%q", result.Level)
	}
	if result.FlagNote() == "" {
		t.Fatal("标记必须给出审核提示文案")
	}
}

// 硬拦优先于标记：否则加一个擦边词就能把硬拦降级成"待人工"，等于给了一条绕过路径。
func TestScreenCreationPostPrefersBlockOverFlag(t *testing.T) {
	result := ScreenCreationPost("擦边写真 + 色情")
	if result.Level != ContentScreenLevelBlock {
		t.Fatalf("同时命中时应按硬拦处理，实际 level=%q", result.Level)
	}
}

func TestScreenCreationPostPassesCleanContent(t *testing.T) {
	for _, text := range []string{
		"黄昏时分的城市天际线，暖色调，电影感",
		"a cinematic portrait of a woman in a red coat, 85mm lens",
		"",
		"   ",
	} {
		if result := ScreenCreationPost(text); result.Hit() {
			t.Fatalf("干净内容不应命中：%q -> level=%q category=%q", text, result.Level, result.Category)
		}
	}
}

// 任一字段命中即拦截：只查标题会让违规正文从描述或提示词里溜进队列。
func TestScreenCreationPostScansEveryField(t *testing.T) {
	result := ScreenCreationPost("干净的标题", "干净的描述", "提示词里藏了 冰毒")
	if result.Level != ContentScreenLevelBlock {
		t.Fatalf("提示词里的违规词也必须拦下，实际 level=%q", result.Level)
	}
}

func TestSplitScreenWordsIgnoresBlanks(t *testing.T) {
	terms := splitScreenWords(" 违规词 , ,,另一个 ")
	if len(terms) != 2 || terms[0] != "违规词" || terms[1] != "另一个" {
		t.Fatalf("环境变量切词不干净：%#v", terms)
	}
	if len(splitScreenWords("")) != 0 {
		t.Fatal("空环境变量不应产生词条")
	}
}

func TestNormalizeScreenTextKeepsLettersAndDigits(t *testing.T) {
	if got := normalizeScreenText(" AI 绘画 v2.1 "); got != "ai绘画v21" {
		t.Fatalf("规范化结果不符：%q", got)
	}
}
