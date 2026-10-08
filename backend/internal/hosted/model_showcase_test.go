package hosted

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	showcaseChannelID  = "CHANNEL_SHOWCASE"
	showcaseModelKey   = "openai/gpt-image-2.5-sunburst"
	showcaseNoPriceKey = "openai/gpt-image-2"
)

type showcaseModelRow struct {
	Slug        string   `json:"slug"`
	DisplayName string   `json:"displayName"`
	Capability  string   `json:"capability"`
	Tagline     string   `json:"tagline"`
	Highlights  []string `json:"highlights"`
	SourceURL   string   `json:"sourceUrl"`
	Spec        struct {
		Ratios       []string `json:"ratios"`
		QualityTiers []string `json:"qualityTiers"`
		MaxOutputs   int      `json:"maxOutputs"`
	} `json:"spec"`
	Prices []struct {
		PriceTier     string `json:"priceTier"`
		Unit          string `json:"unit"`
		SellUnitPrice *int64 `json:"sellUnitPrice"`
		Priced        bool   `json:"priced"`
	} `json:"prices"`
}

// newShowcaseRouter 铺一条能真的出现在广场上的链路：一个启用中的系统渠道、
// 一个带能力合同的模型、以及它的价目。
func newShowcaseRouter(t *testing.T) (*Extension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	if err := auth.EnsurePricingSchema(authDB); err != nil {
		t.Fatalf("初始化定价表失败: %v", err)
	}
	seedShowcaseCatalog(t, canvasDB)
	router := newTestRouter(extension, service)
	return extension.(*Extension), router, authDB, canvasDB
}

func seedShowcaseCatalog(t *testing.T, canvasDB *gorm.DB) {
	t.Helper()
	channel := model.ModelChannel{
		ID: showcaseChannelID, Scope: model.ChannelScopeSystem, Enabled: true,
		Name: "Replicate", PublicAlias: "Replicate", SortOrder: 1,
		BaseURL: "https://api.replicate.com", APIKey: "test-key", APIFormat: "replicate",
	}
	if err := canvasDB.Create(&channel).Error; err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}
	config := `{"version":1,"image":{"references":{"promptMaxChars":32000,"maxImages":4,"maxImageBytes":31457280,"maskSupported":false},` +
		`"size":{"parameter":"aspect_ratio","values":["1:1","16:9"],"default":"1:1","allowCustom":false},` +
		`"quality":{"supported":true,"values":["low","medium","high","xhigh","max"],"default":"low"},` +
		`"transparentBackground":{"supported":false,"default":false},"responseFormat":{"supported":false},` +
		`"outputFormat":{"supported":false},"maxOutputs":10}}`
	rows := []model.ChannelModel{
		{
			ID: "MODEL_SHOWCASE", ChannelID: showcaseChannelID, ModelKey: showcaseModelKey,
			ProviderModelKey: showcaseModelKey, DisplayName: "GPT Image 2.5 Sunburst", Icon: "openai",
			Capability: "image", Protocol: model.ChannelInterfaceReplicatePredictionImage,
			Enabled: true, CapabilityConfigJSON: config,
		},
		{
			// 这个模型没有价目：广场必须把它挡在外面，否则用户点进去只会撞上"未定价"。
			ID: "MODEL_NOPRICE", ChannelID: showcaseChannelID, ModelKey: showcaseNoPriceKey,
			ProviderModelKey: showcaseNoPriceKey, DisplayName: "GPT Image 2", Icon: "openai",
			Capability: "image", Protocol: model.ChannelInterfaceReplicatePredictionImage,
			Enabled: true, CapabilityConfigJSON: config,
		},
	}
	if err := canvasDB.Create(&rows).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}
}

// seedShowcasePrices 用后台接口写价目，保证走的是与线上一致的写入路径与默认倍率。
func seedShowcasePrices(t *testing.T, router *gin.Engine, adminCookie *http.Cookie) {
	t.Helper()
	body := `{"modelKey":"` + showcaseChannelID + `::` + showcaseModelKey + `","capability":"IMAGE","priceTier":"XHIGH","unit":"IMAGE","upstreamUnitPrice":180,"multiplier":"5"}`
	if recorder := perform(router, http.MethodPost, "/api/admin/billing/model-prices", body, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("写入价目失败：%d %s", recorder.Code, recorder.Body.String())
	}
}

// TestPublicModelShowcase 覆盖模型介绍页的公开读取：未登录可读、按价目收敛、
// 价格不带上游成本、列表不携带自述文件正文而详情携带。
func TestPublicModelShowcase(t *testing.T) {
	extension, router, authDB, _ := newShowcaseRouter(t)
	defer extension.Close()

	// 未登录也应该拿到 200：模型介绍页就是给访客看的，401 会让它变成登录墙。
	recorder := perform(router, http.MethodGet, "/api/public/models", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("未登录读取模型目录应返回 200，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if cache := recorder.Header().Get("Cache-Control"); !strings.Contains(cache, "public") {
		t.Fatalf("公开页响应应可被公共缓存：%q", cache)
	}
	var empty struct {
		Data struct {
			Models []showcaseModelRow `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &empty); err != nil {
		t.Fatalf("解析目录响应失败: %v %s", err, recorder.Body.String())
	}
	if len(empty.Data.Models) != 0 {
		t.Fatalf("还没有价目时目录应为空，实际 %s", recorder.Body.String())
	}

	adminCookie, adminID := registerAccount(t, router, authDB, "showcase-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	seedShowcasePrices(t, router, adminCookie)

	recorder = perform(router, http.MethodGet, "/api/public/models", "", nil)
	var listed struct {
		Data struct {
			Models []showcaseModelRow `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("解析目录响应失败: %v %s", err, recorder.Body.String())
	}
	if len(listed.Data.Models) != 1 {
		t.Fatalf("没有价目的模型不该出现在目录，实际 %d 条：%s", len(listed.Data.Models), recorder.Body.String())
	}
	item := listed.Data.Models[0]
	if item.Slug != showcaseModelKey || item.DisplayName != "GPT Image 2.5 Sunburst" || item.Capability != "image" {
		t.Fatalf("条目缺少模型标识或展示名：%+v", item)
	}
	// 参数表只翻译能力合同：五档质量与比例必须出现，否则用户看不到自己买到了什么。
	if len(item.Spec.QualityTiers) != 5 || item.Spec.MaxOutputs != 10 || len(item.Spec.Ratios) != 2 {
		t.Fatalf("参数表与能力合同不一致：%+v", item.Spec)
	}
	if len(item.Prices) != 1 || item.Prices[0].PriceTier != "XHIGH" || item.Prices[0].SellUnitPrice == nil || *item.Prices[0].SellUnitPrice != 900 {
		t.Fatalf("售价应为 180 × 5 = 900：%+v", item.Prices)
	}
	// 上游进价属于经营信息，一个字段都不能漏到响应里。
	if strings.Contains(recorder.Body.String(), "upstreamUnitPrice") || strings.Contains(recorder.Body.String(), "multiplierBp") {
		t.Fatalf("响应里出现了上游成本或倍率：%s", recorder.Body.String())
	}

	// 详情：模型标识自带斜杠，路径必须原样保留。
	detail := perform(router, http.MethodGet, "/api/public/models/"+showcaseModelKey, "", nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"slug":"`+showcaseModelKey+`"`) {
		t.Fatalf("按模型标识读取详情失败：%d %s", detail.Code, detail.Body.String())
	}
	if missing := perform(router, http.MethodGet, "/api/public/models/someone/unknown", "", nil); missing.Code != http.StatusNotFound {
		t.Fatalf("未知模型应返回 404，实际 %d：%s", missing.Code, missing.Body.String())
	}
}

// TestShowcaseReadmeOnlyInDetail 锁定自述文件的传输口径：列表不带正文、详情带正文。
//
// 列表页一次要给十几个模型，正文是长文；带上它会让首屏多传几十 KB 而一个字都不显示。
func TestShowcaseReadmeOnlyInDetail(t *testing.T) {
	extension, router, authDB, _ := newShowcaseRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "showcase-readme-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	seedShowcasePrices(t, router, adminCookie)

	readme := "## 它的作用\n\n它会跟随指令，同时保留你不想改的部分。"
	body := `{"modelKey":"` + showcaseModelKey + `","tagline":"定位","readme":` + strconv.Quote(readme) + `}`
	if recorder := perform(router, http.MethodPut, "/api/admin/model-showcase", body, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("写入自述文件失败：%d %s", recorder.Code, recorder.Body.String())
	}

	list := perform(router, http.MethodGet, "/api/public/models", "", nil)
	if strings.Contains(list.Body.String(), "保留你不想改的部分") {
		t.Fatalf("列表响应不该携带自述文件正文：%s", list.Body.String())
	}

	detail := perform(router, http.MethodGet, "/api/public/models/"+showcaseModelKey, "", nil)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "保留你不想改的部分") {
		t.Fatalf("详情响应应携带自述文件正文：%d %s", detail.Code, detail.Body.String())
	}
}

// TestAdminModelShowcaseRequiresAdmin 覆盖写入口的守卫：文案是对外内容，不能由普通账号改。
func TestAdminModelShowcaseRequiresAdmin(t *testing.T) {
	extension, router, authDB, _ := newShowcaseRouter(t)
	defer extension.Close()

	body := `{"modelKey":"` + showcaseModelKey + `","tagline":"一句话定位"}`
	if recorder := perform(router, http.MethodPut, "/api/admin/model-showcase", body, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录写文案应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	userCookie, _ := registerAccount(t, router, authDB, "showcase-user@example.com")
	if recorder := perform(router, http.MethodPut, "/api/admin/model-showcase", body, userCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号写文案应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	adminCookie, adminID := registerAccount(t, router, authDB, "showcase-admin2@example.com")
	promoteToAdmin(t, authDB, adminID)
	if recorder := perform(router, http.MethodPut, "/api/admin/model-showcase", body, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("管理员写文案失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 幂等覆盖：同一个模型再写一次不新增行，只换文案。
	second := `{"modelKey":"` + showcaseModelKey + `","tagline":"改过的定位","highlights":["指令跟随强","文字渲染清晰"]}`
	if recorder := perform(router, http.MethodPut, "/api/admin/model-showcase", second, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("覆盖文案失败：%d %s", recorder.Code, recorder.Body.String())
	}
	list := perform(router, http.MethodGet, "/api/admin/model-showcase", "", adminCookie)
	var entries struct {
		Data struct {
			Entries []struct {
				ModelKey   string   `json:"modelKey"`
				Tagline    string   `json:"tagline"`
				Highlights []string `json:"highlights"`
			} `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &entries); err != nil {
		t.Fatalf("解析文案列表失败: %v %s", err, list.Body.String())
	}
	if len(entries.Data.Entries) != 1 || entries.Data.Entries[0].Tagline != "改过的定位" || len(entries.Data.Entries[0].Highlights) != 2 {
		t.Fatalf("覆盖写入应只留一行新文案：%s", list.Body.String())
	}

	// 缺模型标识必须被挡下：没有标识的文案永远不会被任何模型读到。
	if recorder := perform(router, http.MethodPut, "/api/admin/model-showcase", `{"tagline":"没有标识"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("缺模型标识应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// TestShowcaseSpecKeepsMediaLimits 锁定参数表翻译：字段名与能力合同对应，不自行补值。
func TestShowcaseSpecKeepsMediaLimits(t *testing.T) {
	config := map[string]any{
		"version": 1,
		"video": map[string]any{
			"ratios":        []any{"16:9", "9:16"},
			"resolutions":   []any{"720p"},
			"references":    map[string]any{"maxImages": 2, "maxVideos": 1},
			"duration":      map[string]any{"selection": "range", "min": 4, "max": 12, "step": 2, "default": 6},
			"generateAudio": map[string]any{"supported": true, "default": true},
		},
	}
	spec := showcaseSpecOf("video", config)
	if len(spec.Ratios) != 2 || len(spec.Resolutions) != 1 || spec.MaxReferenceImages != 2 || spec.MaxReferenceVideos != 1 || !spec.GenerateAudio {
		t.Fatalf("视频参数表翻译不正确：%+v", spec)
	}
	if spec.Range == nil || spec.Range.Min != 4 || spec.Range.Max != 12 || spec.Range.Step != 2 || spec.Range.Value != 6 {
		t.Fatalf("时长区间翻译不正确：%+v", spec.Range)
	}
	// 图片能力不该被填上视频字段，反之亦然：前端按 capability 决定渲染哪几行。
	if spec.Durations == nil || spec.QualityTiers == nil {
		t.Fatalf("空参数必须是空数组而不是 nil，否则前端要补一轮判空：%+v", spec)
	}
	image := showcaseSpecOf("image", map[string]any{"image": map[string]any{"maxOutputs": 4}})
	if image.MaxOutputs != 4 || image.Range != nil || image.GenerateAudio {
		t.Fatalf("图片参数表翻译不正确：%+v", image)
	}
}

// TestShowcaseSpecSplitsDurationsByResolution 锁住"按分辨率分档的时长"的投影边界。
func TestShowcaseSpecSplitsDurationsByResolution(t *testing.T) {
	config := map[string]any{
		"version": 1,
		"video": map[string]any{
			"resolutions": []any{"480p", "720p"},
			"duration":    map[string]any{"selection": "enum", "values": []any{10, 12, 15}, "default": 12},
			"durationByResolution": map[string]any{
				"720p": map[string]any{"selection": "enum", "values": []any{10, 12}, "default": 12},
			},
		},
	}
	spec := showcaseSpecOf("video", config)
	if len(spec.ResolutionDurations) != 2 {
		t.Fatalf("应按分辨率给出两档时长：%+v", spec.ResolutionDurations)
	}
	// 没登记过的分辨率回落到顶层时长，而不是从参数表里消失。
	if spec.ResolutionDurations[0].Resolution != "480p" || len(spec.ResolutionDurations[0].Durations) != 3 {
		t.Fatalf("480p 应回落到顶层 10 / 12 / 15：%+v", spec.ResolutionDurations[0])
	}
	if spec.ResolutionDurations[1].Resolution != "720p" || len(spec.ResolutionDurations[1].Durations) != 2 {
		t.Fatalf("720p 应取登记过的 10 / 12：%+v", spec.ResolutionDurations[1])
	}

	// 每档与顶层一致时不能拆：那只是把同一句话重复 N 遍。
	same := map[string]any{
		"version": 1,
		"video": map[string]any{
			"resolutions": []any{"480p", "720p"},
			"duration":    map[string]any{"selection": "enum", "values": []any{10, 12}},
			"durationByResolution": map[string]any{
				"720p": map[string]any{"selection": "enum", "values": []any{10, 12}},
			},
		},
	}
	if spec := showcaseSpecOf("video", same); spec.ResolutionDurations != nil {
		t.Fatalf("档位一致时不该拆分：%+v", spec.ResolutionDurations)
	}

	// 顶层是区间时不拆：区间在"标签 + 值"的行模型里没有位置。
	ranged := map[string]any{
		"version": 1,
		"video": map[string]any{
			"resolutions": []any{"720p"},
			"duration":    map[string]any{"selection": "range", "min": 4, "max": 12, "step": 1, "default": 6},
			"durationByResolution": map[string]any{
				"720p": map[string]any{"selection": "enum", "values": []any{5, 10}},
			},
		},
	}
	if spec := showcaseSpecOf("video", ranged); spec.ResolutionDurations != nil {
		t.Fatalf("顶层是区间时不该拆分：%+v", spec.ResolutionDurations)
	}
}
