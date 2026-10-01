package app

import (
	"os"
	"strings"
	"unicode"
)

// 投稿内容预筛：用户提交的标题/描述/提示词在进入人工审核队列之前先过一遍关键词。
//
// 定位要说清楚：这一层不是审核，只是**给人工队列减负的粗筛**。子串匹配天生笨——
// 它会把"寺庙旅行照"和"宣扬某教"一起拦下，也会被同音字、拆字、拼音绕过。因此它只
// 负责拦掉"没有讨论空间"的那部分（露骨色情、赌博、毒品违禁、暴力恐怖），以及按运营
// 要求一并拦下的政治与宗教表述；边界内容一律放行到人工队列，由人做判断。真实防线是
// 人工审核 + 后续的模型审核，不是这份词表。
//
// 词表刻意留在代码里而不是数据库：它是审核口径的一部分，改动应该经过发版评审，避免
// 后台一次误操作把闸门整个打开。需要临时加词时用环境变量叠加，不必等发版：
//
//	BEEFTV_CONTENT_REVIEW_BLOCK_WORDS  额外硬拦词，英文逗号分隔
//	BEEFTV_CONTENT_REVIEW_FLAG_WORDS   额外标记词，英文逗号分隔
//
// 内置词表是**起步集**，不是完整清单，运营应当按实际收到的投稿持续补充。

// ContentScreenLevel 是预筛结论等级。
type ContentScreenLevel string

const (
	// ContentScreenLevelBlock 表示命中硬拦词：直接驳回，不占用人工队列。
	ContentScreenLevelBlock ContentScreenLevel = "BLOCK"
	// ContentScreenLevelFlag 表示命中关注词：仍然进人工队列，只是带上标记。
	ContentScreenLevelFlag ContentScreenLevel = "FLAG"
)

// ContentScreenResult 是一次预筛的结论。Level 为空字符串表示未命中任何规则。
type ContentScreenResult struct {
	Level    ContentScreenLevel
	Category string
	Term     string
}

// Hit 表示是否命中规则。
func (r ContentScreenResult) Hit() bool { return r.Level != "" }

// RejectReason 是与命中结果匹配的驳回文案，回显给投稿人。
//
// 只说"命中哪一类"而不回显命中的词：回显原词等于把词表送给对方，下一版投稿就会
// 用同音字绕过去。
func (r ContentScreenResult) RejectReason() string {
	switch r.Category {
	case "色情":
		return "内容涉嫌色情低俗，无法通过审核"
	case "赌博":
		return "内容涉嫌赌博或博彩，无法通过审核"
	case "毒品违禁":
		return "内容涉嫌毒品、违禁品或违法交易，无法通过审核"
	case "暴力恐怖":
		return "内容涉嫌暴力、恐怖或血腥，无法通过审核"
	case "政治":
		return "内容涉嫌政治敏感，无法通过审核"
	case "宗教":
		return "内容涉嫌宗教宣扬，无法通过审核"
	default:
		return "内容不符合广场规范，无法通过审核"
	}
}

// FlagNote 是关注词的审核标记文案。
func (r ContentScreenResult) FlagNote() string {
	if r.Category == "" {
		return "命中共审关注词，请人工确认"
	}
	return "命中共审关注词（" + r.Category + "），请人工确认"
}

// screenRule 是一条预筛规则。
type screenRule struct {
	category string
	level    ContentScreenLevel
	term     string
}

// contentScreenBuiltinBlock 是内置硬拦词表（按类别分组，会被拍平成规则）。
//
// 这些词单独出现就足以说明投稿方向，不需要上下文判断。
var contentScreenBuiltinBlock = map[string][]string{
	"色情":   {"色情", "情色", "裸聊", "裸播", "裸奔", "约炮", "一夜情", "成人视频", "成人影片", "黄片", "三级片", "卖淫", "嫖娼", "招嫖", "淫秽", "做爱", "性器官", "露点", "调教", "性奴", "porn", "porno", "nsfw", "hentai", "onlyfans", "escort"},
	"赌博":   {"赌博", "赌球", "博彩", "六合彩", "私彩", "网赌", "棋牌赌博", "老虎机", "洗钱", "跑分平台"},
	"毒品违禁": {"毒品", "冰毒", "海洛因", "摇头丸", "大麻", "吸毒", "制毒", "贩毒", "枪支", "弹药", "爆炸物", "违禁品", "迷药", "听话水", "办假证", "代开发票", "假钞", "仿真枪"},
	"暴力恐怖": {"恐怖袭击", "极端组织", "斩首", "血腥肢解", "虐杀", "自杀教程", "自残教程"},
	"政治":   {"颠覆国家", "煽动颠覆", "分裂国家", "台独", "港独", "疆独", "藏独", "法轮功", "六四事件", "反华", "游行示威", "打倒共产党"},
	"宗教":   {"传教", "传福音", "布道", "邪教", "开光", "法事", "超度", "算命", "看相", "风水转运", "驱邪", "降头"},
}

// contentScreenBuiltinFlag 是内置关注词表：进队列，但要求人工重点看。
var contentScreenBuiltinFlag = map[string][]string{
	"擦边":   {"擦边", "低胸", "内衣模特", "泳装写真", "制服诱惑", "丝袜", "透视装"},
	"医疗":   {"处方药", "治愈癌症", "包治百病", "偏方", "药品代购", "医美贷"},
	"金融":   {"荐股", "带单", "炒币", "虚拟货币", "挖矿", "高额返利", "刷单兼职"},
	"真人肖像": {"明星换脸", "deepfake", "换脸", "ai换脸", "名人肖像"},
}

// contentScreenRules 是启动时展开好的规则表。词表是常量加环境变量，进程生命周期内
// 不变，因此在 init 里展开一次，避免每次投稿都重建一遍正则/切片。
var contentScreenRules = buildContentScreenRules()

func buildContentScreenRules() []screenRule {
	rules := make([]screenRule, 0, 128)
	appendRules := func(table map[string][]string, level ContentScreenLevel) {
		for category, terms := range table {
			for _, term := range terms {
				rules = append(rules, screenRule{category: category, level: level, term: normalizeScreenText(term)})
			}
		}
	}
	appendRules(contentScreenBuiltinBlock, ContentScreenLevelBlock)
	appendRules(contentScreenBuiltinFlag, ContentScreenLevelFlag)

	for _, term := range splitScreenWords(os.Getenv("BEEFTV_CONTENT_REVIEW_BLOCK_WORDS")) {
		rules = append(rules, screenRule{category: "运营追加", level: ContentScreenLevelBlock, term: term})
	}
	for _, term := range splitScreenWords(os.Getenv("BEEFTV_CONTENT_REVIEW_FLAG_WORDS")) {
		rules = append(rules, screenRule{category: "运营追加", level: ContentScreenLevelFlag, term: term})
	}
	return rules
}

// splitScreenWords 把环境变量切成规范化后的词，忽略空项。
func splitScreenWords(raw string) []string {
	parts := strings.Split(raw, ",")
	terms := make([]string, 0, len(parts))
	for _, part := range parts {
		if term := normalizeScreenText(part); term != "" {
			terms = append(terms, term)
		}
	}
	return terms
}

// normalizeScreenText 去掉大小写、空白与标点后再匹配。
//
// 目的是让"色 情"、"色-情"、"S E X"这类最省事的绕写失效。它挡不住同音字与拼音，
// 那是模型审核的事。
func normalizeScreenText(raw string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(raw) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// ScreenCreationPost 对投稿文本做关键词预筛，返回等级最高的命中结果。
//
// 硬拦优先于关注：一条投稿同时踩中两类时按硬拦处理，否则"加一个擦边词把硬拦降级"
// 会变成绕过手法。
func ScreenCreationPost(texts ...string) ContentScreenResult {
	var flagged ContentScreenResult
	for _, text := range texts {
		haystack := normalizeScreenText(text)
		if haystack == "" {
			continue
		}
		for _, rule := range contentScreenRules {
			if rule.term == "" || !strings.Contains(haystack, rule.term) {
				continue
			}
			result := ContentScreenResult{Level: rule.level, Category: rule.category, Term: rule.term}
			if rule.level == ContentScreenLevelBlock {
				return result
			}
			if !flagged.Hit() {
				flagged = result
			}
		}
	}
	return flagged
}
