package app

import "strings"

const (
	// creationInspirationTagMaxCount 是主推荐卡上标签排的长度上限。这一排是标题下的配角，
	// 超过四个就会换行、把卡片底部的安全区顶开，标题反而被挤走。
	creationInspirationTagMaxCount = 4
	// creationInspirationTagMaxRunes 按字符计，够写「未来 · 科幻」这种双词标签；
	// 再长就不再是标签，而是一句分类描述了。
	creationInspirationTagMaxRunes = 12
)

// normalizeCreationInspirationTags 收敛运营填写的标签：去首尾空白、丢掉空项与超长项、
// 按原文去重、截到数量上限。
//
// 静默丢弃而不是报错：标签纯粹是卡片装饰，为了一个多余的标签让整条灵感存不进去，
// 是拿主体内容的可用性去换一个配角的完整性。
func normalizeCreationInspirationTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > creationInspirationTagMaxRunes {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
		if len(normalized) == creationInspirationTagMaxCount {
			break
		}
	}
	return normalized
}

// joinCreationInspirationTags 把标签数组压成入库的逗号分隔串。
func joinCreationInspirationTags(tags []string) string {
	return strings.Join(normalizeCreationInspirationTags(tags), ",")
}

// splitCreationInspirationTags 把库里的逗号分隔串还原成数组，复归一化是为了让
// 手工写库或迁移前的脏数据（空项、多余空白）也在出口被收干净。
func splitCreationInspirationTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	return normalizeCreationInspirationTags(strings.Split(raw, ","))
}
