package model

import "time"

// ModelShowcaseEntry 是模型广场上"平台自己写的那部分"内容：定位、简介、要点与上游溯源。
//
// 与渠道模型分开存，因为两者回答的是不同问题：渠道模型回答"这个模型能不能用"（能力合同、
// 上游 SKU、启用状态，由运营在后台维护），这里回答"这个模型是干什么的"（文案）。两者用
// ModelKey 对齐，所以换上游 SKU 或临时下架都不会动到文案，广场按"在售 ∩ 有文案"渲染。
//
// 这张表只在托管实例上创建（见 database.MigrateHostedSharedSchema）：桌面端没有广场，
// 也不该凭空多出一张永远为空的表。
type ModelShowcaseEntry struct {
	ID string `gorm:"primaryKey;size:64"`
	// ModelKey 是上游模型标识（如 openai/gpt-image-2.5-sunburst），不带渠道路径。
	// 广场是"模型"的介绍，不是"某个渠道上的某个 SKU"，所以同一模型换渠道不该出现两条。
	ModelKey string `gorm:"size:255;uniqueIndex"`
	// Tagline 是一句话定位，列表页卡片上就显示这一行。
	Tagline string `gorm:"size:255"`
	// Summary 是详情页的正文简介。
	Summary string `gorm:"type:text"`
	// Highlights 是中文要点，存 JSON 数组文本；与能力 JSON 同一口径，避免为几个短句再建一张表。
	Highlights string `gorm:"type:text"`
	// SourceURL / SourceNote 保留上游页面与原文描述，用于溯源，也避免改文案时找不到出处。
	SourceURL  string `gorm:"size:512"`
	SourceNote string `gorm:"type:text"`
	// Readme 是"自述文件"正文（Markdown 文本），详情页的主要篇幅。
	//
	// 与 Summary 分开：Summary 是列表与首屏用的一句话，Readme 是展开后的长文，两者
	// 长度差一个数量级，塞进同一个字段会让列表页被迫加载几十 KB 的正文。
	Readme string `gorm:"type:text"`
	// Examples 存平台自己的示例资源地址（JSON 数组），刻意不接受上游外链：
	// 外链会在对方改路径或限流那天变成一片破图，而广场是给人看门面的地方。
	Examples  string `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}
