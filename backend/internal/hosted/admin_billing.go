package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 计费域的管理端接口（套餐、订单、优惠券、支付渠道、手工赠送订阅）。
//
// 这一组挂在 /api/admin 下并复用宿主的 requireAdmin 守卫，管理员之外的人连列表都看不到。
// 后台是"改动即生效"的：改一次套餐价格、关一个支付渠道都会影响下一笔真实收款，因此每个
// 写操作都必须落审计——事后复盘一次客诉时，只有留痕能回答"谁在什么时候把价格改成了多少"。
//
// 支付渠道的密钥（商户号、密钥）只写不读：读取视图里只有 hasSecret，审计元数据里更是
// 一概不出现，否则留痕本身就成了第二条泄露路径。

// adminBillingOrderPagePayload 组装后台订单分页响应。
//
// 与用户端不同，这里额外带一份全量营收读数：指标卡要的是"不受当前筛选影响"的经营数据，
// 让前端拿筛选结果自己加总会把"只看了待支付"算成当天营收。revenue 恒为对象，缺数据时
// 退化成零值而不是 null，前端读字段时不用再判一次空。
func adminBillingOrderPagePayload(view *auth.BillingOrderPageView, page, pageSize int) gin.H {
	orders := []auth.BillingOrderView{}
	total := int64(0)
	revenue := auth.BillingRevenue{}
	if view != nil {
		orders = billingList(view.Orders)
		total = view.Total
		if view.Page > 0 {
			page = view.Page
		}
		if view.PageSize > 0 {
			pageSize = view.PageSize
		}
		if view.Revenue != nil {
			revenue = *view.Revenue
		}
	}
	return gin.H{"orders": orders, "total": total, "page": page, "pageSize": pageSize, "revenue": revenue}
}

// billingChannelsPayload 把渠道配置包成契约形状，且列表位置恒为数组。
func billingChannelsPayload(view *auth.PaymentChannelsView) gin.H {
	channels := []auth.PaymentChannelView{}
	if view != nil {
		channels = billingList(view.Channels)
	}
	return gin.H{"channels": channels}
}

// adminActorID 取当前管理员的账号 ID，用于需要落库"是谁操作的"的服务调用。
//
// 这些路由挂在带守卫的分组下，取不到只可能是装配错误；缺 actor 宁可按空处理，也不要
// 因为一次 nil 解引用把请求打成 panic——500 加一段栈，比少记一个操作人更难排查。
func adminActorID(c *gin.Context) string {
	if actor := adminActor(c); actor != nil {
		return actor.ID
	}
	return ""
}

// normalizePaymentChannel 只做大小写与空白的归一。
//
// 渠道白名单留在服务层：那里才知道哪些渠道有可用的收银台实现，在这一层再维护一份
// 只会出现"后台加了渠道、HTTP 层先把它拒了"的不一致。
func normalizePaymentChannel(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// registerAdminBillingRoutes 挂载 /api/admin 下的计费路由（group 已带管理员守卫）。
func (e *Extension) registerAdminBillingRoutes(group *gin.RouterGroup) {
	group.GET("/plans", e.handleAdminBillingPlans)
	group.POST("/plans", e.handleAdminBillingPlanCreate)
	group.PUT("/plans/:id", e.handleAdminBillingPlanUpdate)
	group.DELETE("/plans/:id", e.handleAdminBillingPlanDelete)

	group.GET("/orders", e.handleAdminBillingOrders)
	// 手工补单与退款都是"钱已经动了"的写操作，接口本身不发起渠道退款：这里只改订单
	// 与订阅状态，实际出款仍在渠道后台完成。
	group.POST("/orders/:id/mark-paid", e.handleAdminBillingOrderMarkPaid)
	group.POST("/orders/:id/refund", e.handleAdminBillingOrderRefund)

	group.GET("/coupons", e.handleAdminBillingCoupons)
	group.POST("/coupons", e.handleAdminBillingCouponCreate)
	group.PUT("/coupons/:id", e.handleAdminBillingCouponUpdate)
	group.DELETE("/coupons/:id", e.handleAdminBillingCouponDelete)
	group.GET("/coupons/:id/redemptions", e.handleAdminCouponRedemptions)

	group.GET("/billing/payment-channels", e.handleAdminPaymentChannels)
	group.PUT("/billing/payment-channels/:channel", e.handleAdminUpdatePaymentChannel)

	// 手工赠送订阅：线下转账、合同赠送与客服补偿都走这里。直接改库会让"这个账号为什么
	// 是会员"失去解释，也无法追责。
	group.POST("/users/:id/subscription", e.handleAdminGrantSubscription)
}

func (e *Extension) handleAdminBillingPlans(c *gin.Context) {
	plans, err := e.service.AdminBillingPlans()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"plans": billingList(plans)})
}

func (e *Extension) handleAdminBillingPlanCreate(c *gin.Context) {
	var input auth.BillingPlanInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "套餐参数格式错误")
		return
	}
	// 新建时丢弃请求体里的 id：主键由服务层生成，客户端传进来的 id 会让一次"新建"
	// 静默变成覆盖已有套餐（连同它的价格）。
	input.ID = ""
	plan, err := e.service.SaveBillingPlan(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 只记"哪一档套餐、什么价格、开没开"：价格是要对账的主数据，改名/改价必须可回溯。
	e.recordAudit(c, "plan.create", "plan", billingView(plan).ID, "新建计费套餐", gin.H{
		"code":     input.Code,
		"name":     input.Name,
		"priceFen": input.PriceFen,
		"enabled":  input.Enabled,
	})
	respondOK(c, gin.H{"plan": billingView(plan)})
}

func (e *Extension) handleAdminBillingPlanUpdate(c *gin.Context) {
	var input auth.BillingPlanInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "套餐参数格式错误")
		return
	}
	// 主键以路径为准：前端 PUT 的 body 与路径参数若不一致，信 body 会让一次误点改到
	// 另一档套餐上，而套餐价格直接决定下一笔收款金额。
	input.ID = strings.TrimSpace(c.Param("id"))
	if input.ID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少套餐标识")
		return
	}
	plan, err := e.service.SaveBillingPlan(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "plan.update", "plan", input.ID, "更新计费套餐", gin.H{
		"code":     input.Code,
		"name":     input.Name,
		"priceFen": input.PriceFen,
		"enabled":  input.Enabled,
	})
	respondOK(c, gin.H{"plan": billingView(plan)})
}

func (e *Extension) handleAdminBillingPlanDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少套餐标识")
		return
	}
	if err := e.service.DeleteBillingPlan(id); err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "plan.delete", "plan", id, "删除计费套餐", nil)
	// 删除后直接回全量列表：下架套餐是低频运营动作，省掉前端再补一次 GET，也避免
	// "已经删掉了、列表还停在旧数据"的半拍状态。
	plans, listErr := e.service.AdminBillingPlans()
	if listErr != nil {
		respondServiceError(c, listErr)
		return
	}
	respondOK(c, gin.H{"plans": billingList(plans)})
}

func (e *Extension) handleAdminBillingOrders(c *gin.Context) {
	page, pageSize := billingPagination(c)
	view, err := e.service.AdminBillingOrders(auth.BillingOrderFilter{
		Status:   strings.TrimSpace(c.Query("status")),
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		PlanCode: strings.TrimSpace(c.Query("planCode")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, adminBillingOrderPagePayload(view, page, pageSize))
}

func (e *Extension) handleAdminBillingOrderMarkPaid(c *gin.Context) {
	var input struct {
		Remark string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "请求参数格式错误")
		return
	}
	orderID := strings.TrimSpace(c.Param("id"))
	order, err := e.service.MarkBillingOrderPaid(orderID, strings.TrimSpace(input.Remark))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	view := billingView(order)
	// 补单等于认可一笔"渠道没通知、但钱确实到了"的收款：备注写进审计，对账时靠它解释
	// 差额来自哪一笔。
	e.recordAudit(c, "order.mark-paid", "order", orderID, "手工标记订单已支付", gin.H{
		"orderNo":    view.OrderNo,
		"payableFen": view.PayableFen,
		"remark":     input.Remark,
	})
	respondOK(c, gin.H{"order": view})
}

func (e *Extension) handleAdminBillingOrderRefund(c *gin.Context) {
	var input struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "请求参数格式错误")
		return
	}
	orderID := strings.TrimSpace(c.Param("id"))
	order, err := e.service.RefundBillingOrder(orderID, strings.TrimSpace(input.Reason))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	view := billingView(order)
	e.recordAudit(c, "order.refund", "order", orderID, "订单退款", gin.H{
		"orderNo":    view.OrderNo,
		"payableFen": view.PayableFen,
		"reason":     input.Reason,
	})
	respondOK(c, gin.H{"order": view})
}

func (e *Extension) handleAdminBillingCoupons(c *gin.Context) {
	coupons, err := e.service.AdminBillingCoupons()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"coupons": billingList(coupons)})
}

func (e *Extension) handleAdminBillingCouponCreate(c *gin.Context) {
	var input auth.BillingCouponInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "优惠券参数格式错误")
		return
	}
	// 与套餐同理：新建不使用请求体里的 id，避免"新建"变成覆盖一张已发出的券。
	input.ID = ""
	coupon, err := e.service.SaveBillingCoupon(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 面额、门槛与总量都是会直接抵扣现金的参数，必须留痕。
	e.recordAudit(c, "coupon.create", "coupon", billingView(coupon).ID, "新建优惠券", gin.H{
		"code":       input.Code,
		"kind":       input.Kind,
		"value":      input.Value,
		"totalQuota": input.TotalQuota,
		"enabled":    input.Enabled,
	})
	respondOK(c, gin.H{"coupon": billingView(coupon)})
}

func (e *Extension) handleAdminBillingCouponUpdate(c *gin.Context) {
	var input auth.BillingCouponInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "优惠券参数格式错误")
		return
	}
	input.ID = strings.TrimSpace(c.Param("id"))
	if input.ID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少优惠券标识")
		return
	}
	coupon, err := e.service.SaveBillingCoupon(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "coupon.update", "coupon", input.ID, "更新优惠券", gin.H{
		"code":       input.Code,
		"kind":       input.Kind,
		"value":      input.Value,
		"totalQuota": input.TotalQuota,
		"enabled":    input.Enabled,
	})
	respondOK(c, gin.H{"coupon": billingView(coupon)})
}

func (e *Extension) handleAdminBillingCouponDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少优惠券标识")
		return
	}
	if err := e.service.DeleteBillingCoupon(id); err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "coupon.delete", "coupon", id, "删除优惠券", nil)
	// 与套餐删除一致：回全量列表，前端不用再补一次读。
	coupons, listErr := e.service.AdminBillingCoupons()
	if listErr != nil {
		respondServiceError(c, listErr)
		return
	}
	respondOK(c, gin.H{"coupons": billingList(coupons)})
}

func (e *Extension) handleAdminCouponRedemptions(c *gin.Context) {
	page, pageSize := billingPagination(c)
	rows, total, err := e.service.CouponRedemptions(auth.CouponRedemptionFilter{
		CouponID: strings.TrimSpace(c.Param("id")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 核销流水是只读留痕，空结果也必须是数组：客服页面直接遍历它。
	if rows == nil {
		rows = []auth.BillingCouponRedemption{}
	}
	respondOK(c, gin.H{"redemptions": rows, "total": total, "page": page, "pageSize": pageSize})
}

func (e *Extension) handleAdminPaymentChannels(c *gin.Context) {
	view, err := e.service.AdminPaymentChannels()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, billingChannelsPayload(view))
}

func (e *Extension) handleAdminUpdatePaymentChannel(c *gin.Context) {
	channel := normalizePaymentChannel(c.Param("channel"))
	if channel == "" {
		respondFailure(c, http.StatusBadRequest, "未知的支付渠道")
		return
	}
	var input auth.PaymentChannelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "支付渠道配置格式错误")
		return
	}
	view, err := e.service.UpdatePaymentChannel(channel, input, adminActorID(c))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 只记"改过哪个渠道、是开还是关"：config 里是商户号与密钥，写进审计等于把密钥
	// 存进一张后台可查的表，与"密钥只写不读"的设计自相矛盾。
	e.recordAudit(c, "payment-channel.update", "payment-channel", channel, "更新支付渠道配置", gin.H{
		"channel": channel,
		"enabled": input.Enabled,
	})
	respondOK(c, billingChannelsPayload(view))
}

func (e *Extension) handleAdminGrantSubscription(c *gin.Context) {
	var input struct {
		PlanCode string `json:"planCode"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "请求参数格式错误")
		return
	}
	userID := strings.TrimSpace(c.Param("id"))
	planCode := strings.TrimSpace(input.PlanCode)
	if userID == "" || planCode == "" {
		respondFailure(c, http.StatusBadRequest, "缺少用户或套餐标识")
		return
	}
	// actorID 只用于留痕：服务层不落库（本地形态没有审计表），"是谁给的"由这里记。
	entitlements, err := e.service.GrantBillingSubscription(userID, planCode, adminActorID(c))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 赠送订阅等于平台白送一份额度：审计必须能回答"给了谁、哪个套餐"。
	e.recordAudit(c, "subscription.grant", "user", userID, "手工赠送订阅", gin.H{
		"planCode": planCode,
	})
	respondOK(c, gin.H{"entitlements": billingView(entitlements)})
}
