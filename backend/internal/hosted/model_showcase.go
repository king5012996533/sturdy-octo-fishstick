package hosted

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// 模型广场：把"在售的模型"整理成一份不依赖会话的对外介绍。
//
// 三个数据源各管一段，广场只做拼接，不复制任何一份：
//
//   - 渠道模型（app.ModelCatalog）回答"有哪些模型、参数是什么"——与登录用户选模型时
//     看到的是同一份脱敏目录，所以广场不会宣传一个前台选不到的模型；
//   - 价目（auth.ModelPriceQuotes）回答"卖多少钱"——与扣费走同一套解析，页面上的价
//     就是账单上的价；
//   - 广场文案表回答"这个模型是干什么的"——上游抓取 + 人工定位，与上面两者用模型标识对齐。
//
// 没有价目的模型直接不出现：广场的每个条目都应当点得进创作台，宣传一个用不了的模型
// 只会让用户在提交时撞上"未定价"。

// showcaseMediaLimits 是"能塞进去多少张/多少秒"这类参数，字段名与前端渲染的表格逐字对应。
//
// 只有 image / video 两种能力有细分参数，audio 与其余能力留空结构体：前端据 capability
// 决定渲染哪几行，缺的字段发零值而不是省略，省得前端在每次新增能力时补一轮 undefined 判断。
type showcaseMediaLimits struct {
	MaxOutputs         int `json:"maxOutputs"`
	MaxReferenceImages int `json:"maxReferenceImages"`
	MaxReferenceVideos int `json:"maxReferenceVideos"`
}

// showcaseDurationRange 描述"按区间选时长"的视频模型（如 Seedance 的 1-15 秒）。
type showcaseDurationRange struct {
	Min   int `json:"min"`
	Max   int `json:"max"`
	Step  int `json:"step"`
	Value int `json:"value"`
}

// showcaseSpec 是参数表的读模型：字段都取自已发布的能力合同，不是另写一份宣传口径。
type showcaseSpec struct {
	Ratios        []string               `json:"ratios"`
	QualityTiers  []string               `json:"qualityTiers"`
	Resolutions   []string               `json:"resolutions"`
	Durations     []int                  `json:"durations"`
	Range         *showcaseDurationRange `json:"range,omitempty"`
	GenerateAudio bool                   `json:"generateAudio"`
	showcaseMediaLimits
}

// showcasePrice 是广场上的一行价：档位 + 单位 + 售价。
type showcasePrice struct {
	PriceTier     string `json:"priceTier"`
	Unit          string `json:"unit"`
	SellUnitPrice *int64 `json:"sellUnitPrice"`
	Priced        bool   `json:"priced"`
}

// showcaseModel 是一个模型条目的完整对外形状。
type showcaseModel struct {
	// Slug 是上游模型标识（可能带斜杠），也是详情路由的参数与文档锚点。
	Slug        string          `json:"slug"`
	DisplayName string          `json:"displayName"`
	Icon        string          `json:"icon"`
	Capability  string          `json:"capability"`
	Protocol    string          `json:"protocol"`
	Tagline     string          `json:"tagline"`
	Summary     string          `json:"summary"`
	Highlights  []string        `json:"highlights"`
	SourceURL   string          `json:"sourceUrl"`
	Spec        showcaseSpec    `json:"spec"`
	Prices      []showcasePrice `json:"prices"`
	// Readme 只在详情响应里出现：列表页一次要给十几个模型，带上正文会让首屏
	// 多传几十 KB 而一个字都不显示。
	Readme string `json:"readme,omitempty"`
}

func (e *Extension) registerModelShowcaseRoutes(api *gin.RouterGroup) {
	api.GET("/public/models", e.handlePublicModelShowcase)
	// 详情用通配而不是路径参数：模型标识自带斜杠（openai/gpt-image-2.5-sunburst），
	// 编码进单个路径段会在不同代理上表现不一致——有的原样透传，有的先解码，路由匹配
	// 结果随之改变。通配写法让斜杠留在路径里，双方都不需要转义。
	api.GET("/public/models/*slug", e.handlePublicModelShowcaseDetail)
}

func (e *Extension) registerAdminModelShowcaseRoutes(group *gin.RouterGroup) {
	group.GET("/model-showcase", e.handleAdminModelShowcase)
	// 写入口只做幂等覆盖（按模型标识），不提供"新建/更新"两个动作：发布脚本每次都是
	// 按上游现状重放一遍，让它自己判断新增还是更新，等于把同一件事写两遍。
	group.PUT("/model-showcase", e.handleAdminModelShowcaseSave)
}

// showcaseModels 组装对外列表。任何一段数据缺失都只影响对应的模型，不影响整页。
func (e *Extension) showcaseModels() ([]showcaseModel, error) {
	if e.canvas == nil {
		return []showcaseModel{}, nil
	}
	catalog, err := e.canvas.ModelCatalog(nil)
	if err != nil {
		return nil, err
	}

	entries, err := e.canvas.ModelShowcaseEntries()
	if err != nil {
		return nil, err
	}
	copyByModel := make(map[string]model.ModelShowcaseEntry, len(entries))
	for _, entry := range entries {
		copyByModel[entry.ModelKey] = entry
	}

	// 先把要查价的标识收集起来，一次查完：逐个模型查一次会让广场的首屏请求
	// 变成 N 次价目查询，而 N 会随模型数量一起长。
	priceKeys := make([]string, 0, len(catalog.Channels))
	for _, channel := range catalog.Channels {
		for _, item := range channel.Models {
			priceKeys = append(priceKeys, showcasePricingKey(channel.ID, item.ModelKey))
		}
	}
	quotes, err := e.service.ModelPriceQuotes(priceKeys)
	if err != nil {
		return nil, err
	}

	result := make([]showcaseModel, 0, len(priceKeys))
	for _, channel := range catalog.Channels {
		for _, item := range channel.Models {
			modelQuotes := quotes[showcasePricingKey(channel.ID, item.ModelKey)]
			if len(modelQuotes) == 0 {
				// 没有价目 = 现在卖不了，不上广场。
				continue
			}
			entry := copyByModel[item.ModelKey]
			result = append(result, showcaseModel{
				Slug:        item.ModelKey,
				DisplayName: firstNonEmptyText(item.DisplayName, item.ModelKey),
				Icon:        item.Icon,
				Capability:  item.Capability,
				Protocol:    string(item.Protocol),
				Tagline:     entry.Tagline,
				Summary:     entry.Summary,
				Highlights:  decodeShowcaseList(entry.Highlights),
				SourceURL:   entry.SourceURL,
				Spec:        showcaseSpecOf(item.Capability, item.CapabilityConfig),
				Prices:      showcasePricesOf(modelQuotes),
			})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Capability != result[j].Capability {
			return showcaseCapabilityOrder(result[i].Capability) < showcaseCapabilityOrder(result[j].Capability)
		}
		return result[i].Slug < result[j].Slug
	})
	return result, nil
}

// showcasePricingKey 拼出计费用的模型标识：渠道与模型两个维度缺一不可，
// 不同渠道可能上架同名的上游 SKU，只用模型名会让两家价目串在一起。
func showcasePricingKey(channelID string, modelKey string) string {
	return strings.TrimSpace(channelID) + "::" + strings.TrimSpace(modelKey)
}

// showcaseCapabilityOrder 决定广场按什么顺序分组：先图后视频再音频，最后是其余能力。
// 图片当前是主力模型族，放最前面用户第一屏就能看到自己在找的东西。
func showcaseCapabilityOrder(capability string) int {
	switch strings.ToLower(strings.TrimSpace(capability)) {
	case "image":
		return 0
	case "video":
		return 1
	case "audio":
		return 2
	case "text":
		return 3
	default:
		return 4
	}
}

func showcasePricesOf(quotes []auth.ModelPriceQuote) []showcasePrice {
	prices := make([]showcasePrice, 0, len(quotes))
	for _, quote := range quotes {
		prices = append(prices, showcasePrice{
			PriceTier:     quote.PriceTier,
			Unit:          quote.Unit,
			SellUnitPrice: quote.SellUnitPrice,
			Priced:        quote.Priced,
		})
	}
	return prices
}

// decodeShowcaseList 读 JSON 数组文本，损坏或为空时回空切片。
//
// 广场是纯展示路径：一条文案的 JSON 写坏了不该让整页打不开，回空数组即可，
// 而它对应的模型仍然会正常出现。
func decodeShowcaseList(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}
	}
	var values []string
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return []string{}
	}
	if values == nil {
		return []string{}
	}
	return values
}

func encodeShowcaseList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (e *Extension) handlePublicModelShowcase(c *gin.Context) {
	models, err := e.showcaseModels()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 模型介绍页是纯公共只读页，缓存 60 秒省掉每个访客一次全表读；价格与货架的变更
	// 在一分钟内传播到访客，不需要为"秒级一致"牺牲可用性。
	c.Header("Cache-Control", "public, max-age=60")
	respondOK(c, gin.H{"models": models})
}

func (e *Extension) handlePublicModelShowcaseDetail(c *gin.Context) {
	slug := strings.TrimPrefix(c.Param("slug"), "/")
	if strings.TrimSpace(slug) == "" {
		respondFailure(c, http.StatusNotFound, "模型不存在")
		return
	}
	models, err := e.showcaseModels()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	for _, item := range models {
		if item.Slug == slug {
			item.Readme = e.showcaseReadme(slug)
			c.Header("Cache-Control", "public, max-age=60")
			respondOK(c, gin.H{"model": item})
			return
		}
	}
	respondFailure(c, http.StatusNotFound, "模型不存在")
}

// showcaseReadme 取单个模型的自述文件正文。
//
// 读不到就回空串：正文缺失只该让详情页少一段展开内容，不该让整个模型页打不开。
func (e *Extension) showcaseReadme(modelKey string) string {
	if e.canvas == nil {
		return ""
	}
	entry, err := e.canvas.ModelShowcaseEntryByModelKey(modelKey)
	if err != nil || entry == nil {
		return ""
	}
	return entry.Readme
}

// showcaseEntryInput 是后台写入一条广场文案的请求体。
type showcaseEntryInput struct {
	ModelKey   string   `json:"modelKey"`
	Tagline    string   `json:"tagline"`
	Summary    string   `json:"summary"`
	Highlights []string `json:"highlights"`
	SourceURL  string   `json:"sourceUrl"`
	SourceNote string   `json:"sourceNote"`
	Examples   []string `json:"examples"`
	Readme     string   `json:"readme"`
}

func (e *Extension) handleAdminModelShowcase(c *gin.Context) {
	entries, err := e.canvas.ModelShowcaseEntries()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	payload := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		payload = append(payload, showcaseEntryPayload(entry))
	}
	respondOK(c, gin.H{"entries": payload})
}

func (e *Extension) handleAdminModelShowcaseSave(c *gin.Context) {
	var input showcaseEntryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "广场文案格式错误")
		return
	}
	modelKey := strings.TrimSpace(input.ModelKey)
	if modelKey == "" {
		respondFailure(c, http.StatusBadRequest, "请填写模型标识")
		return
	}
	entry := model.ModelShowcaseEntry{
		ModelKey:   modelKey,
		Tagline:    strings.TrimSpace(input.Tagline),
		Summary:    strings.TrimSpace(input.Summary),
		Highlights: encodeShowcaseList(input.Highlights),
		SourceURL:  strings.TrimSpace(input.SourceURL),
		SourceNote: strings.TrimSpace(input.SourceNote),
		Examples:   encodeShowcaseList(input.Examples),
		Readme:     strings.TrimSpace(input.Readme),
	}
	if err := e.canvas.SaveModelShowcaseEntry(&entry); err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, showcaseEntryPayload(entry))
}

func showcaseEntryPayload(entry model.ModelShowcaseEntry) gin.H {
	return gin.H{
		"modelKey":   entry.ModelKey,
		"tagline":    entry.Tagline,
		"summary":    entry.Summary,
		"highlights": decodeShowcaseList(entry.Highlights),
		"sourceUrl":  entry.SourceURL,
		"sourceNote": entry.SourceNote,
		"examples":   decodeShowcaseList(entry.Examples),
		"readme":     entry.Readme,
	}
}

// ---- 参数表：只从已发布的能力合同里取值 ----

// showcaseSpecOf 把能力合同投影成参数表。
//
// 刻意不写第二份"宣传参数"：参数表写宽了，用户按它选了上游不认的组合，故障点会落到
// 生成失败上，而排查的人根本不会想到去看广场页面。所以这里只翻译，不补充。
func showcaseSpecOf(capability string, config map[string]any) showcaseSpec {
	spec := showcaseSpec{Ratios: []string{}, QualityTiers: []string{}, Resolutions: []string{}, Durations: []int{}}
	if config == nil {
		return spec
	}
	switch strings.ToLower(strings.TrimSpace(capability)) {
	case "image":
		image := childMap(config, "image")
		spec.Ratios = stringList(childMap(image, "size")["values"])
		spec.MaxOutputs = intValue(image["maxOutputs"])
		spec.MaxReferenceImages = intValue(childMap(image, "references")["maxImages"])
		quality := childMap(image, "quality")
		if boolValue(quality["supported"]) {
			spec.QualityTiers = stringList(quality["values"])
		}
	case "video":
		video := childMap(config, "video")
		spec.Ratios = stringList(video["ratios"])
		spec.Resolutions = stringList(video["resolutions"])
		references := childMap(video, "references")
		spec.MaxReferenceImages = intValue(references["maxImages"])
		spec.MaxReferenceVideos = intValue(references["maxVideos"])
		spec.GenerateAudio = boolValue(childMap(video, "generateAudio")["supported"])
		duration := childMap(video, "duration")
		switch strings.TrimSpace(toStringValue(duration["selection"])) {
		case "enum":
			spec.Durations = intList(duration["values"])
		case "range":
			spec.Range = &showcaseDurationRange{
				Min:   intValue(duration["min"]),
				Max:   intValue(duration["max"]),
				Step:  intValue(duration["step"]),
				Value: intValue(duration["default"]),
			}
		}
	}
	return spec
}

func childMap(source map[string]any, key string) map[string]any {
	if source == nil {
		return nil
	}
	value, _ := source[key].(map[string]any)
	return value
}

func stringList(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		if values, direct := raw.([]string); direct {
			result := make([]string, 0, len(values))
			for _, value := range values {
				if trimmed := strings.TrimSpace(value); trimmed != "" {
					result = append(result, trimmed)
				}
			}
			return result
		}
		return []string{}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(toStringValue(item)); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func intList(raw any) []int {
	items, ok := raw.([]any)
	if !ok {
		return []int{}
	}
	result := make([]int, 0, len(items))
	for _, item := range items {
		result = append(result, intValue(item))
	}
	return result
}

func intValue(raw any) int {
	switch value := raw.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0
		}
		return int(parsed)
	default:
		return 0
	}
}

func boolValue(raw any) bool {
	value, _ := raw.(bool)
	return value
}

func toStringValue(raw any) string {
	if raw == nil {
		return ""
	}
	if text, ok := raw.(string); ok {
		return text
	}
	return fmt.Sprint(raw)
}
