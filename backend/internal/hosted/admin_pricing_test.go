package hosted

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 定价响应的读模型只声明被断言的字段：服务端多出的字段由响应全文兜底检查，
// 这里不跟着改动，类型就不会因为后端加字段而失效。
type pricingPriceRow struct {
	ID                string `json:"id"`
	ModelKey          string `json:"modelKey"`
	Capability        string `json:"capability"`
	PriceTier         string `json:"priceTier"`
	Unit              string `json:"unit"`
	UpstreamUnitPrice *int64 `json:"upstreamUnitPrice"`
	SellUnitPrice     *int64 `json:"sellUnitPrice"`
	MultiplierBp      *int   `json:"multiplierBp"`
	Enabled           bool   `json:"enabled"`
	CreatedAt         string `json:"createdAt"`
}

type pricingMarkupRuleRow struct {
	Scope        string `json:"scope"`
	Target       string `json:"target"`
	MultiplierBp int    `json:"multiplierBp"`
	Multiplier   string `json:"multiplier"`
}

type pricingMarkupData struct {
	Rules               []pricingMarkupRuleRow `json:"rules"`
	DefaultMultiplierBp int                    `json:"defaultMultiplierBp"`
}

type pricingResolutionData struct {
	MultiplierBp  int    `json:"multiplierBp"`
	Source        string `json:"source"`
	SellUnitPrice *int64 `json:"sellUnitPrice"`
	Priced        bool   `json:"priced"`
}

// newPricingAdminRouter 复用标准托管路由，并补建定价表。
//
// 定价路由的挂载点由合并方接线（registerAdminRoutes → registerAdminPricingRoutes），在接线
// 之前 newTestRouter 里没有这组 URL，用例自己挂一次才能覆盖上线的真实路径。一旦合并方接上，
// 同一方法+路径注册两次会让 gin 直接 panic，所以这里先探测路由是否已存在——本用例在"已接线"
// 和"未接线"两种状态下都必须能跑。
func newPricingAdminRouter(t *testing.T) (*Extension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	if err := auth.EnsurePricingSchema(authDB); err != nil {
		t.Fatalf("初始化定价表失败: %v", err)
	}
	router := newTestRouter(extension, service)
	hostedExt := extension.(*Extension)
	if !hasHostedRoute(router, http.MethodGet, "/api/admin/billing/model-prices") {
		group := router.Group("/api/admin")
		group.Use(hostedExt.requireAdmin())
		hostedExt.registerAdminPricingRoutes(group)
	}
	return hostedExt, router, authDB, canvasDB
}

func hasHostedRoute(router *gin.Engine, method string, path string) bool {
	for _, route := range router.Routes() {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

// TestAdminPricingRoutesLifecycle 覆盖守卫、单价 CRUD、倍率全量替换、试算与审计留痕。
func TestAdminPricingRoutesLifecycle(t *testing.T) {
	extension, router, authDB, canvasDB := newPricingAdminRouter(t)
	defer extension.Close()

	// 未登录 401，普通账号 403：后台之外的人连列表都看不到。
	if recorder := perform(router, http.MethodGet, "/api/admin/billing/model-prices", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问定价列表应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	userCookie, _ := registerAccount(t, router, authDB, "pricing-user@example.com")
	if recorder := perform(router, http.MethodGet, "/api/admin/billing/model-prices", "", userCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号访问定价列表应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	adminCookie, adminID := registerAccount(t, router, authDB, "pricing-admin@example.com")
	promoteToAdmin(t, authDB, adminID)

	// 空列表必须是数组：前端直接遍历它渲染表格。
	recorder := perform(router, http.MethodGet, "/api/admin/billing/model-prices", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"prices":[]`) {
		t.Fatalf("空单价列表应为 []，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 新建：只填模型、能力与 token 档位、倍率用倍数写法。单价留空 = 还没定价，读出来必须是 null。
	recorder = perform(router, http.MethodPost, "/api/admin/billing/model-prices",
		`{"modelKey":"gpt-4o-mini","capability":"TEXT","priceTier":"INPUT","multiplier":"1.2"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("新建单价配置失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Data struct {
			Price pricingPriceRow `json:"price"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析新建响应失败: %v %s", err, recorder.Body.String())
	}
	price := created.Data.Price
	if price.ID == "" || price.ModelKey != "gpt-4o-mini" || price.Capability != "TEXT" {
		t.Fatalf("新建响应缺少模型标识：%s", recorder.Body.String())
	}
	if price.UpstreamUnitPrice != nil || price.SellUnitPrice != nil {
		t.Fatalf("未定价的单价必须序列化成 null：%s", recorder.Body.String())
	}
	if price.MultiplierBp == nil || *price.MultiplierBp != 12000 {
		t.Fatalf("倍率 1.2 应存成 12000：%s", recorder.Body.String())
	}
	if price.Unit != string(auth.UnitPerMillionTokens) || !price.Enabled || price.CreatedAt == "" {
		t.Fatalf("新建响应缺少默认单位、启用状态或时间：%s", recorder.Body.String())
	}
	if price.PriceTier != "INPUT" {
		t.Fatalf("新建响应缺少 token 档位：%s", recorder.Body.String())
	}

	// 更新：补上上游价并把售价直接定为 0（免费）。0 与 null 必须能区分。
	recorder = perform(router, http.MethodPut, "/api/admin/billing/model-prices/"+price.ID,
		`{"modelKey":"gpt-4o-mini","capability":"TEXT","priceTier":"INPUT","vendorCode":"openai","upstreamUnitPrice":1000,"sellUnitPrice":0,"multiplier":"1.2"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("更新单价配置失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"sellUnitPrice":0`) || !strings.Contains(recorder.Body.String(), `"upstreamUnitPrice":1000`) {
		t.Fatalf("更新后的金额不正确：%s", recorder.Body.String())
	}

	// 负数是手误的符号，必须在写库之前挡下。
	if recorder := perform(router, http.MethodPut, "/api/admin/billing/model-prices/"+price.ID,
		`{"modelKey":"gpt-4o-mini","capability":"TEXT","upstreamUnitPrice":-1}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("负上游价应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 倍率规则：默认 10000，写入后按归一化结果回显。
	recorder = perform(router, http.MethodGet, "/api/admin/billing/markup", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取倍率规则失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var markup struct {
		Data pricingMarkupData `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &markup); err != nil {
		t.Fatalf("解析倍率响应失败: %v %s", err, recorder.Body.String())
	}
	if markup.Data.DefaultMultiplierBp != 10000 || markup.Data.Rules == nil || len(markup.Data.Rules) != 0 {
		t.Fatalf("初始规则集应为空且默认倍率 10000：%s", recorder.Body.String())
	}

	recorder = perform(router, http.MethodPut, "/api/admin/billing/markup",
		`{"rules":[{"scope":"GLOBAL","multiplierBp":15000},{"scope":"capability","target":"image","multiplierBp":12500}]}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("替换倍率规则失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &markup); err != nil {
		t.Fatalf("解析倍率响应失败: %v %s", err, recorder.Body.String())
	}
	if len(markup.Data.Rules) != 2 {
		t.Fatalf("规则集应有 2 条，实际 %d 条：%s", len(markup.Data.Rules), recorder.Body.String())
	}
	for _, rule := range markup.Data.Rules {
		if rule.Scope == "CAPABILITY" {
			if rule.Target != "IMAGE" || rule.Multiplier != "1.25" {
				t.Fatalf("能力规则未归一化：%+v", rule)
			}
		}
		if rule.Scope == "GLOBAL" && rule.Target != "" {
			t.Fatalf("GLOBAL 规则的目标必须为空：%+v", rule)
		}
	}

	// 非法作用域必须在服务层被拒，不能落库。
	if recorder := perform(router, http.MethodPut, "/api/admin/billing/markup",
		`{"rules":[{"scope":"TENANT","multiplierBp":12000}]}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法作用域应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 试算：命中的是能力规则（该模型没有 IMAGE 配置）：800 × 1.25 = 1000。
	recorder = perform(router, http.MethodPost, "/api/admin/billing/model-prices/preview",
		`{"modelKey":"gpt-4o-mini","capability":"IMAGE","upstreamUnitPrice":800}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("试算失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var preview struct {
		Data struct {
			Resolution pricingResolutionData `json:"resolution"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatalf("解析试算响应失败: %v %s", err, recorder.Body.String())
	}
	resolution := preview.Data.Resolution
	if resolution.Source != "CAPABILITY" || resolution.MultiplierBp != 12500 || !resolution.Priced {
		t.Fatalf("试算未命中能力规则：%s", recorder.Body.String())
	}
	if resolution.SellUnitPrice == nil || *resolution.SellUnitPrice != 1000 {
		t.Fatalf("试算售价应为 1000：%s", recorder.Body.String())
	}

	// 没有上游价又没直接定价：能算倍率，算不出售价（null 而不是 0）。
	recorder = perform(router, http.MethodPost, "/api/admin/billing/model-prices/preview",
		`{"modelKey":"unknown-video","capability":"VIDEO"}`, adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"sellUnitPrice":null`) || !strings.Contains(recorder.Body.String(), `"priced":false`) {
		t.Fatalf("未定价的试算应回 null 售价：%d %s", recorder.Code, recorder.Body.String())
	}

	// 删除：回标记，列表随之清空。
	recorder = perform(router, http.MethodDelete, "/api/admin/billing/model-prices/"+price.ID, "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"deleted":true`) {
		t.Fatalf("删除单价配置失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/admin/billing/model-prices", "", adminCookie); !strings.Contains(recorder.Body.String(), `"prices":[]`) {
		t.Fatalf("删除后列表应为空：%s", recorder.Body.String())
	}

	// 每个写操作都必须留下审计：动作串是复盘时的检索键，不能漂移。
	for _, action := range []string{"model-price.create", "model-price.update", "model-price.delete", "markup.update"} {
		var count int64
		if err := canvasDB.Table("admin_audit_events").Where("action = ?", action).Count(&count).Error; err != nil {
			t.Fatalf("读取审计记录失败: %v", err)
		}
		if count != 1 {
			t.Fatalf("审计动作 %s 应落 1 条，实际 %d 条", action, count)
		}
	}

	// 审计元数据要能回答"改了哪个模型、改成几倍"，并且不含任何凭据字段。
	var event struct {
		ActorUserID  string `gorm:"column:actor_user_id"`
		TargetType   string `gorm:"column:target_type"`
		TargetID     string `gorm:"column:target_id"`
		MetadataJSON string `gorm:"column:metadata_json"`
	}
	if err := canvasDB.Table("admin_audit_events").Where("action = ?", "model-price.update").First(&event).Error; err != nil {
		t.Fatalf("读取单价审计记录失败: %v", err)
	}
	if event.ActorUserID != adminID || event.TargetType != "model-price" || event.TargetID != price.ID {
		t.Fatalf("单价审计记录的主体或目标不正确：%+v", event)
	}
	if !containsAll(event.MetadataJSON, "gpt-4o-mini", "12000", "1000") {
		t.Fatalf("审计元数据缺少模型标识或倍率：%s", event.MetadataJSON)
	}

	var markupEvent struct {
		TargetType   string `gorm:"column:target_type"`
		MetadataJSON string `gorm:"column:metadata_json"`
	}
	if err := canvasDB.Table("admin_audit_events").Where("action = ?", "markup.update").First(&markupEvent).Error; err != nil {
		t.Fatalf("读取倍率审计记录失败: %v", err)
	}
	if markupEvent.TargetType != "markup" || !containsAll(markupEvent.MetadataJSON, "15000", "12500") {
		t.Fatalf("倍率审计记录不正确：%+v", markupEvent)
	}
}
