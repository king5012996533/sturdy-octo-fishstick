package hosted

import (
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
		UserID:     request.UserID,
		TaskID:     request.TaskID,
		ModelKey:   request.ModelKey,
		Capability: request.Capability,
		Quantity:   request.Quantity,
		Note:       request.Note,
	})
	if err != nil {
		return app.TaskChargeOutcome{}, err
	}
	return app.TaskChargeOutcome{Credits: quote.Credits}, nil
}

// RefundTask 退回一次预扣，金额由流水决定，调用方只说"这个任务没跑成"。
func (a creditLedgerAdapter) RefundTask(userID string, taskID string, note string) (int64, bool, error) {
	return a.service.RefundTaskCharge(userID, taskID, note)
}
