package hosted

import (
	"log"
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 用户端计费接口（套餐、权益、优惠券、订单与支付回调）。
//
// 全组的账号来源只有一个：当前会话。请求体与查询串里的 userId 一律不作为凭据——
// 计费接口按用户隔离数据，一旦能指定 userId，查订单就变成了"遍历别人的账单"。
//
// 金额沿用账号库的口径（分），这一层不做任何换算与格式化：JSON 里出现浮点就意味着
// 前端会拿到 0.30000000000000004 这种实付金额。

const (
	billingDefaultPageSize = 20
	// billingMaxPageSize 与账号库 BillingOrders 的上限保持一致：没有上限时一次筛选
	// 就能把整张订单表读进内存，那是资源边界而不是安全边界。
	billingMaxPageSize = 200
)

// billingCallbackPathPrefix 是支付渠道异步通知的路径前缀。
//
// 回调由渠道服务器发起：它不会带我们的会话 Cookie，请求要先能被 WorkspaceMiddleware
// 放行，否则验签还没开始就被判成 401。这个前缀在 hosted.go 的 anonymousPathPrefixes
// 里显式登记，不在这里额外注册：放行名单必须集中可见。
//
// 放行不等于放权：真正的准入是回调内的渠道验签与金额核对（HandleBillingCallback），
// 中间件这一层只负责"不要因为没登录就提前拒绝"。前缀只覆盖回调子路径，不带任何读接口。
const billingCallbackPathPrefix = "/api/payments/callback"

// registerBillingRoutes 挂载需要登录的用户端计费路由。
//
// 这里只挂"以当前账号为主体"的接口，路径与 web/src/services/api/billing.ts 一一对应；
// 后台的读写走 registerAdminBillingRoutes，两边的视图结构体是同一个，字段名不再各解释一次。
func (e *Extension) registerBillingRoutes(api *gin.RouterGroup) {
	api.GET("/finance/plans", e.handleBillingPlans)
	api.GET("/finance/entitlements", e.handleBillingEntitlements)
	api.GET("/finance/coupons", e.handleBillingCoupons)
	api.POST("/finance/coupons/quote", e.handleBillingOrderQuote)

	api.POST("/payments/orders", e.handleBillingCreateOrder)
	api.GET("/payments/orders", e.handleBillingOrderList)
	api.GET("/payments/orders/:id", e.handleBillingOrderDetail)
	api.POST("/payments/orders/:id/cancel", e.handleBillingCancelOrder)
	api.POST("/payments/orders/:id/pay", e.handleBillingStartPayment)

	// 唯一不需要登录的一条：见 billingCallbackPathPrefix 的说明。
	api.POST("/payments/callback/:channel", e.handleBillingCallback)
}

// billingSessionUser 取当前登录账号，取不到就直接回 401。
//
// 宿主中间件已经挡过一层，这里仍然再判一次：这一组路由可能被挂到没有中间件的分组上
// （例如只注册业务路由的测试入口），缺了会话必须显式拒绝，而不是拿空 userId 去查库
// 再把"查不到"当成"没有订单"。
func (e *Extension) billingSessionUser(c *gin.Context) (*auth.AuthUser, bool) {
	user, err := e.service.SessionUser(c.Request.Context(), auth.ReadSessionToken(c.Request))
	if err != nil {
		respondFailure(c, http.StatusUnauthorized, "当前未登录或登录已失效")
		return nil, false
	}
	return user, true
}

// billingView 把可选的单项视图解引用成对象。
//
// 服务层的写接口返回指针，nil 直接塞进 gin.H 会变成 `null`，而契约里这些位置永远是
// 对象（前端会直接读 order.status / plan.priceFen）。nil 与 error 不会同时出现，退回
// 零值对象只是为了让响应形状稳定，不代表操作成功。
func billingView[T any](view *T) T {
	if view == nil {
		var zero T
		return zero
	}
	return *view
}

// billingList 保证列表位置永远是数组。
//
// 空列表在 Go 里可能是 nil，序列化后是 null；前端对货架、券包与订单列表直接做
// map/filter，null 会在渲染时抛错。服务层今天已经承诺返回非 nil 切片，但那是契约
// 而不是编译期约束，这一层把形状钉死，代价只是一个长度判断。
func billingList[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// billingPagination 归一化分页参数。
//
// 复用包内既有的 adminIntQuery：它只做"查询串转整数"，与是否管理端无关，另外复制一份
// 只会让两处默认值先后漂移。默认值与上限对齐账号库的 normalizeBillingPage（20 / 200），
// 保证回给前端的页码就是实际查的那一页——核销流水接口不回传页码，那里只能靠这一层算。
func billingPagination(c *gin.Context) (int, int) {
	page := adminIntQuery(c, "page")
	if page < 1 {
		page = 1
	}
	pageSize := adminIntQuery(c, "pageSize")
	if pageSize <= 0 || pageSize > billingMaxPageSize {
		pageSize = billingDefaultPageSize
	}
	return page, pageSize
}

func (e *Extension) handleBillingPlans(c *gin.Context) {
	plans, err := e.service.BillingCatalog()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"plans": billingList(plans)})
}

func (e *Extension) handleBillingEntitlements(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	entitlements, err := e.service.BillingEntitlements(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"entitlements": billingView(entitlements)})
}

func (e *Extension) handleBillingCoupons(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	coupons, err := e.service.AvailableBillingCoupons(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"coupons": billingList(coupons)})
}

func (e *Extension) handleBillingOrderQuote(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	var input auth.BillingOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "请求参数格式错误")
		return
	}
	// 试算与下单共用服务层的同一段定价逻辑：两边各算一次，就会出现"结算面板显示 8 折，
	// 下单却按原价扣"这种没人能解释的差额。
	quote, err := e.service.QuoteBillingOrder(user.ID, input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"quote": billingView(quote)})
}

func (e *Extension) handleBillingCreateOrder(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	var input auth.BillingOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "请求参数格式错误")
		return
	}
	order, err := e.service.CreateBillingOrder(user.ID, input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"order": billingView(order)})
}

func (e *Extension) handleBillingOrderList(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	page, pageSize := billingPagination(c)
	view, err := e.service.UserBillingOrders(user.ID, page, pageSize)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	orders := []auth.BillingOrderView{}
	total := int64(0)
	if view != nil {
		orders = billingList(view.Orders)
		total = view.Total
		if view.Page > 0 {
			page = view.Page
		}
		if view.PageSize > 0 {
			pageSize = view.PageSize
		}
	}
	// 不回 revenue：全站营收是经营数据，用户查自己的订单时不该顺带拿到。
	respondOK(c, gin.H{"orders": orders, "total": total, "page": page, "pageSize": pageSize})
}

func (e *Extension) handleBillingOrderDetail(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	order, err := e.service.BillingOrderForUser(user.ID, strings.TrimSpace(c.Param("id")))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"order": billingView(order)})
}

func (e *Extension) handleBillingCancelOrder(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	// 取消原因可选：契约里这个接口不带请求体，绑不上就当用户没填，不因为一个可选字段
	// 把整次取消拒掉——用户想撤销一笔待支付订单，不该先被迫填表。
	var input struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&input)
	order, err := e.service.CancelBillingOrder(user.ID, strings.TrimSpace(c.Param("id")), strings.TrimSpace(input.Reason))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"order": billingView(order)})
}

func (e *Extension) handleBillingStartPayment(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	orderID := strings.TrimSpace(c.Param("id"))
	launch, err := e.service.StartBillingPayment(user.ID, orderID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	if launch == nil {
		// 服务层返回成功却没有收银台信息：这里不能把 null 交给前端，否则收银台会停在
		// 一个打不开的页面上，用户以为已支付。契约违约必须留日志，否则只会看到一次
		// 无法复现的 500。
		log.Printf("hosted: 发起支付返回空结果 order=%s", orderID)
		respondFailure(c, http.StatusInternalServerError, "发起支付失败，请稍后重试")
		return
	}
	// 直接回 BillingPaymentLaunch：order/provider/payUrl/payParams 就是收银台需要的
	// 全部字段，再包一层只会让前端多拆一次信封。
	respondOK(c, launch)
}

// handleBillingCallback 接收支付渠道的异步通知。
//
// 这里是整组接口里唯一不做登录校验的一条：回调由渠道服务器发起，它没有也不可能有我们
// 的会话，登录态在这一层帮不上任何忙。真正的准入是渠道验签（HandleBillingCallback 按
// 渠道用后台配置的密钥校验原始报文），验签不过就必须拒绝——回调接口是公开的，能伪造
// 一次"已支付"就等于可以白拿套餐。
//
// 原始 body 原样交给服务层，先反序列化再验签会因为字段顺序、空白与转义差异而验签失败
// （与 auth 包对 OAuth 回调的处理方式一致）。
func (e *Extension) handleBillingCallback(c *gin.Context) {
	channel := strings.ToUpper(strings.TrimSpace(c.Param("channel")))
	raw, err := c.GetRawData()
	if err != nil {
		respondFailure(c, http.StatusBadRequest, "回调报文读取失败")
		return
	}
	if err := e.service.HandleBillingCallback(channel, raw, c.Request.Header); err != nil {
		// 验签失败、报文非法由服务层落成 4xx；其余按 500 处理。把内部故障也回成 400
		// 会让渠道判定"请求无效、无需重试"，订单就永远停在待支付。
		respondServiceError(c, err)
		return
	}
	// 成功只回空对象：回显订单详情等于把一份用户数据写进渠道的日志。
	respondOK(c, gin.H{})
}
