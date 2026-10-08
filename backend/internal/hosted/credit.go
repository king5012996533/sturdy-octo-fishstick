package hosted

import (
	"errors"
	"net/http"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 用户端积分接口（余额与流水）。
//
// 与计费接口同一条铁律：账号来源只有当前会话，查询串里的 userId 一律不作为凭据。
// 积分比订单更敏感——余额可以换算出"这个人充过多少钱"，让它可指定就等于公开全站营收。
//
// 这一组只读：入账由充值到账（支付回调）与后台调整产生，出账由任务提交产生。
// 用户端没有任何一个能直接改余额的接口，哪怕是他自己的。

// registerCreditRoutes 挂载需要登录的积分路由。
//
// 路径与 web/src/services/api/credit.ts 一一对应；后台的读写走 registerAdminCreditRoutes。
func (e *Extension) registerCreditRoutes(api *gin.RouterGroup) {
	api.GET("/finance/wallet", e.handleCreditWallet)
	api.GET("/finance/ledger", e.handleCreditLedger)
	// 生成前试算：与提交任务同形、只读、不落账。前端在选好模型与参数后实时调用它，
	// 把"这次要花多少积分"显示在生成按钮旁边。
	api.POST("/finance/tasks/quote", e.handleTaskChargeQuote)
}

// handleTaskChargeQuote 试算一次提交要扣多少积分。
//
// 请求体与 POST /api/tasks 完全同形：报价必须由服务端按同一份目录重新解析模型，
// 让前端自己算价（或让浏览器传来一个价）都会在渠道、档位或倍率上出现"提示的价"与
// "实扣的价"不一致，而那类偏差只会以客诉的形式暴露。
//
// 余额与报价一起回传：两个数若分两次取，会出现"报价按新价、余额按旧值"的中间态，
// 前端据此判断余额是否够扣就会给出错误结论。
func (e *Extension) handleTaskChargeQuote(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	var input app.CreateTaskRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "请求参数格式错误")
		return
	}
	quote, err := e.canvas.QuoteTaskCharge(user.ID, input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	wallet, err := e.service.CreditWallet(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{
		"quote": quote,
		// 够不够要被两条线同时管住：这次扣多少（Credits），以及文本会话的最低余额水位
		// （MinimumBalance）。只比 Credits 会出现"面板说够、一按生成被 402 挡回"。
		"wallet": gin.H{"balance": wallet.Balance, "sufficient": wallet.Balance >= billingRequiredBalance(quote)},
	})
}

// billingRequiredBalance 是一次提交真正需要的最低余额。
//
// 取两者较大值而不是相加：水位描述的是"跑完这一轮大概要花多少"，本次预扣的钱就来自
// 这笔余额，把它再叠加一遍会凭空翻倍。
func billingRequiredBalance(quote *app.TaskChargeOutcome) int64 {
	if quote == nil {
		return 0
	}
	if quote.MinimumBalance > quote.Credits {
		return quote.MinimumBalance
	}
	return quote.Credits
}

func (e *Extension) handleCreditWallet(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	wallet, err := e.service.CreditWallet(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"wallet": wallet})
}

func (e *Extension) handleCreditLedger(c *gin.Context) {
	user, ok := e.billingSessionUser(c)
	if !ok {
		return
	}
	page, pageSize := billingPagination(c)
	entries, total, err := e.service.CreditLedger(auth.CreditLedgerFilter{
		UserID:   user.ID,
		Kind:     c.Query("kind"),
		RefType:  c.Query("refType"),
		RefID:    c.Query("refId"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 与账单列表同形：空列表回 []，total 与 page 一并回传，前端不必自己数页数。
	respondOK(c, gin.H{"entries": entries, "total": total, "page": page, "pageSize": pageSize})
}

// creditLedgerAdapter 把积分域接到任务计费端口上。
//
// app 侧只认 TaskCreditLedger 这个窄接口，是为了让任务域不依赖账号域：桌面与本地装载
// 没有账号库，也就没有计费端口，那里不注入即可（见 app.UseTaskCreditLedger）。
// 这一层只做类型搬运，不做任何金额计算——算价与扣款都在 auth 域内完成，否则
// "后台显示 1.2 倍、实扣按 1 倍"这类偏差只会出现在对账时。
type creditLedgerAdapter struct {
	service *auth.Service
}

// ChargeTask 预扣一次任务消耗。
//
// 未定价的模型会被 auth 侧拒绝（409 failed_precondition）而不是放行：静默按 0 元出货
// 是这类系统里最难追溯的一种损失。想让某个模型免费，就把它的售价显式配成 0。
func (a creditLedgerAdapter) ChargeTask(request app.TaskChargeRequest) (app.TaskChargeOutcome, error) {
	quote, _, _, err := a.service.ChargeTask(auth.TaskChargeInput{
		UserID:           request.UserID,
		TaskID:           request.TaskID,
		ModelKey:         request.ModelKey,
		Capability:       request.Capability,
		Tier:             request.Tier,
		Quantity:         request.Quantity,
		SurchargeCredits: request.SurchargeCredits,
		SurchargeNote:    request.SurchargeNote,
		Note:             request.Note,
	})
	if err != nil {
		return app.TaskChargeOutcome{}, creditLedgerError(err)
	}
	return taskChargeOutcome(quote), nil
}

// QuoteTask 试算一次任务消耗，不写账。
//
// 与 ChargeTask 共用账号域的取价函数：两个方法唯一的差别是"要不要真的动余额"。
func (a creditLedgerAdapter) QuoteTask(request app.TaskChargeRequest) (app.TaskChargeOutcome, error) {
	quote, err := a.service.QuoteTask(auth.TaskChargeInput{
		UserID:           request.UserID,
		TaskID:           request.TaskID,
		ModelKey:         request.ModelKey,
		Capability:       request.Capability,
		Tier:             request.Tier,
		Quantity:         request.Quantity,
		SurchargeCredits: request.SurchargeCredits,
		SurchargeNote:    request.SurchargeNote,
		Note:             request.Note,
	})
	if err != nil {
		return app.TaskChargeOutcome{}, creditLedgerError(err)
	}
	return taskChargeOutcome(quote), nil
}

// taskChargeOutcome 把账号域的报价搬成任务域的形状，只搬不换算。
func taskChargeOutcome(quote *auth.TaskChargeQuote) app.TaskChargeOutcome {
	if quote == nil {
		return app.TaskChargeOutcome{}
	}
	return app.TaskChargeOutcome{
		Credits:          quote.Credits,
		Unit:             quote.Unit,
		Quantity:         quote.Quantity,
		SurchargeCredits: quote.SurchargeCredits,
		SellUnitPrice:    quote.SellUnitPrice,
		MultiplierBp:     quote.MultiplierBp,
		MultiplierSource: quote.MultiplierSource,
		Priced:           quote.Priced,
		MinimumBalance:   quote.MinimumBalance,
	}
}

// EnsureTextTaskBalance 校验文本任务的最低余额水位（余额不足返回 402）。
//
// 金额与口径都在账号域：这个模型要留多少余额是定价决策，任务域只决定"这一次要不要查"。
func (a creditLedgerAdapter) EnsureTextTaskBalance(userID string, modelKey string) error {
	return creditLedgerError(a.service.EnsureTextTaskBalance(userID, modelKey))
}

// creditLedgerError 把账号域错误翻译成任务域能识别的错误。
//
// 必须在这里翻译，因为 app 与 handler 都不认识 *auth.Error：让它原样穿过端口，
// 提交任务时"尚未定价"（409）会在 HTTP 层掉进兜底分支变成 500「系统处理失败」，
// 而"余额不足"（402）会变成 400。用户看到的文案与真正的原因无关，
// 前端也就永远命中不了它按状态码写的分支。翻译只搬运状态、码与原因，不改文案。
func creditLedgerError(err error) error {
	var authErr *auth.Error
	if !errors.As(err, &authErr) {
		return err
	}
	bridged := app.NewAppError(authErr.Status, authErr.Message)
	if authErr.Code != 0 {
		bridged.Code = authErr.Code
	}
	if authErr.Reason != "" {
		bridged.Reason = authErr.Reason
	}
	bridged.Cause = authErr
	return bridged
}

// RefundTask 退回一次预扣，金额由流水决定，调用方只说"这个任务没跑成"。
func (a creditLedgerAdapter) RefundTask(userID string, taskID string, note string) (int64, bool, error) {
	return a.service.RefundTaskCharge(userID, taskID, note)
}

// SettleTextTask 按上游回执的真实 token 用量给一次文本任务补扣差额。
//
// 只补扣、不退款：预扣的起步价是产品定价而不是押金，差额为负时按起步价成交（口径见
// auth.SettleTextTaskCharge）。任务域因此不必为"要不要退差"准备第二条资金路径。
//
// 未取到任何价目时返回 Priced=false 而不是报错：结算发生在任务成功之后，此时把错误
// 抛给 worker 只会把一条已经成功的任务翻成失败。少收的钱由调用方落日志、人工核对。
func (a creditLedgerAdapter) SettleTextTask(request app.TaskTextSettleRequest) (app.TaskTextSettleOutcome, error) {
	quote, _, _, err := a.service.SettleTextTaskCharge(auth.TextSettleInput{
		UserID:   request.UserID,
		TaskID:   request.TaskID,
		ModelKey: request.ModelKey,
		Usage: auth.TextTokenUsage{
			Input:  request.InputTokens,
			Cached: request.CachedTokens,
			Output: request.OutputTokens,
		},
	})
	if err != nil {
		return app.TaskTextSettleOutcome{}, creditLedgerError(err)
	}
	return taskTextSettleOutcome(quote), nil
}

// taskTextSettleOutcome 把账号域的结算读数搬成任务域的形状，只搬不换算。
func taskTextSettleOutcome(quote *auth.TextSettleQuote) app.TaskTextSettleOutcome {
	if quote == nil {
		return app.TaskTextSettleOutcome{}
	}
	return app.TaskTextSettleOutcome{
		Credits:      quote.Credits,
		Charged:      quote.Charged,
		Delta:        quote.Delta,
		Priced:       quote.Priced,
		MissingTiers: quote.MissingTiers,
		Uncollected:  quote.Uncollected,
		Note:         quote.Note,
	}
}
