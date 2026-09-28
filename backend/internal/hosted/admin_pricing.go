package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 模型定价的后台接口（可空单价占位、倍率规则与试算）。
//
// 这一组与套餐、优惠券同挂 /api/admin，并复用宿主的 requireAdmin 守卫。定价是"改动即
// 生效"的经营参数，与支付渠道同级，因此不额外挂细粒度权限点：能进后台的管理员就能定价。
//
// 每个写操作都落审计：一次调价事后只能靠留痕回答"谁在什么时候把 1.2 倍改成了 1 倍"。
// 元数据只放模型标识与倍率（这一域本来就没有密钥），不把整条配置塞进去，免得把无关的
// 备注文本也复制进一张后台可查的表。

// markupPayload 把规则集摊成响应形状。
//
// rules 恒为数组：前端直接遍历它渲染表单，null 会在渲染时抛错。默认倍率一并回传，
// 前端展示"默认不加价"时不必自己写死 10000。
func markupPayload(view *auth.MarkupView) gin.H {
	rules := []auth.MarkupRuleView{}
	// 与 auth 侧的万分比基准一致：视图缺失时按"不加价"兜底，而不是 0——0 会让前端
	// 把默认倍率显示成"免费"。
	defaultMultiplierBp := 10000
	if view != nil {
		rules = billingList(view.Rules)
		if view.DefaultMultiplierBp > 0 {
			defaultMultiplierBp = view.DefaultMultiplierBp
		}
	}
	return gin.H{"rules": rules, "defaultMultiplierBp": defaultMultiplierBp}
}

// modelPriceAuditMetadata 只摘"这次改了什么口径"：模型标识、能力与两个价格。
//
// 单价同样是经营参数，一并留下，便于事后对比调价前后的差额；倍率则回答"这次加价定在
// 几倍"。金额是可空的，nil 会序列化成 null，正好表达"改成了未定价"。
func modelPriceAuditMetadata(view *auth.ModelPriceView) gin.H {
	if view == nil {
		return nil
	}
	return gin.H{
		"modelKey":          view.ModelKey,
		"capability":        view.Capability,
		"unit":              view.Unit,
		"multiplierBp":      view.MultiplierBp,
		"upstreamUnitPrice": view.UpstreamUnitPrice,
		"sellUnitPrice":     view.SellUnitPrice,
	}
}

// registerAdminPricingRoutes 挂载 /api/admin 下的定价路由（group 已带管理员守卫）。
func (e *Extension) registerAdminPricingRoutes(group *gin.RouterGroup) {
	group.GET("/billing/markup", e.handleAdminMarkupRules)
	group.PUT("/billing/markup", e.handleAdminReplaceMarkupRules)

	group.GET("/billing/model-prices", e.handleAdminModelPrices)
	group.POST("/billing/model-prices", e.handleAdminModelPriceCreate)
	// 试算只读，但仍用 POST：它带一个结构化请求体，放进查询串会让上游价、能力这些
	// 字段在日志与浏览器历史里各留一份。
	group.POST("/billing/model-prices/preview", e.handleAdminModelPricePreview)
	group.PUT("/billing/model-prices/:id", e.handleAdminModelPriceUpdate)
	group.DELETE("/billing/model-prices/:id", e.handleAdminModelPriceDelete)
}

func (e *Extension) handleAdminMarkupRules(c *gin.Context) {
	view, err := e.service.AdminMarkupRules()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, markupPayload(view))
}

func (e *Extension) handleAdminReplaceMarkupRules(c *gin.Context) {
	var input auth.MarkupInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "倍率参数格式错误")
		return
	}
	view, err := e.service.ReplaceMarkupRules(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 全量替换会一次改掉所有作用域的加价，审计必须留下这批倍率本身：只记"改过规则"
	// 无法回答"当时是几倍"。
	multipliers := make([]int, 0, len(input.Rules))
	for _, rule := range input.Rules {
		multipliers = append(multipliers, rule.MultiplierBp)
	}
	// 规则集没有单条主键可作为 targetId，用空值：targetType 已经表明改的是哪一类配置。
	e.recordAudit(c, "markup.update", "markup", "", "更新模型倍率规则", gin.H{
		"ruleCount":   len(input.Rules),
		"multipliers": multipliers,
		"defaultBp":   view.DefaultMultiplierBp,
	})
	respondOK(c, markupPayload(view))
}

func (e *Extension) handleAdminModelPrices(c *gin.Context) {
	views, err := e.service.AdminModelPrices()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"prices": billingList(views)})
}

func (e *Extension) handleAdminModelPriceCreate(c *gin.Context) {
	var input auth.ModelPriceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "单价参数格式错误")
		return
	}
	// 新建时丢弃请求体里的 id：主键由服务层生成，客户端传进来的 id 会让一次"新建"
	// 静默变成覆盖已有配置（连同它已经填好的价格）。与套餐、优惠券同一口径。
	input.ID = ""
	view, err := e.service.SaveModelPrice(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "model-price.create", "model-price", view.ID, "新建模型单价配置", modelPriceAuditMetadata(view))
	respondOK(c, gin.H{"price": billingView(view)})
}

func (e *Extension) handleAdminModelPriceUpdate(c *gin.Context) {
	var input auth.ModelPriceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "单价参数格式错误")
		return
	}
	input.ID = strings.TrimSpace(c.Param("id"))
	if input.ID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少单价配置标识")
		return
	}
	view, err := e.service.SaveModelPrice(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "model-price.update", "model-price", input.ID, "更新模型单价配置", modelPriceAuditMetadata(view))
	respondOK(c, gin.H{"price": billingView(view)})
}

func (e *Extension) handleAdminModelPriceDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少单价配置标识")
		return
	}
	if err := e.service.DeleteModelPrice(id); err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "model-price.delete", "model-price", id, "删除模型单价配置", nil)
	// 只回一个标记：前端删完会自己重读列表，回全量列表反而让两处数据源各说各话。
	respondOK(c, gin.H{"deleted": true})
}

func (e *Extension) handleAdminModelPricePreview(c *gin.Context) {
	var input auth.PricingInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "试算参数格式错误")
		return
	}
	resolution, err := e.service.PreviewModelPrice(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 试算不落审计：后台表单会高频调用它，把只读的试算写进留痕只会把真正改过的配置淹掉。
	respondOK(c, gin.H{"resolution": billingView(resolution)})
}
