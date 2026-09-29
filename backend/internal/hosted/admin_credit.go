package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 后台积分管理（账户列表、流水、手工调整）。
//
// 这一组里唯一会改变余额的是手工调整，因此它与其余写操作一样必须留审计：积分是本平台
// 的现金等价物，"谁在什么时候给谁加了多少、为什么"是事后唯一能复盘的东西。审计里的
// 金额用整数分原样记录，不做格式化——审计内容一旦是"¥12.00"这种文本，就没法再参与对账。

// registerAdminCreditRoutes 挂载后台积分路由（已由 requireAdmin 守卫）。
func (e *Extension) registerAdminCreditRoutes(group *gin.RouterGroup) {
	group.GET("/credits/accounts", e.handleAdminCreditAccounts)
	group.GET("/credits/ledger", e.handleAdminCreditLedger)
	group.POST("/credits/adjust", e.handleAdminCreditAdjust)
}

func (e *Extension) handleAdminCreditAccounts(c *gin.Context) {
	// 分页口径复用用户端那一份（默认 20、上限 200）：服务层还会再钳一次，
	// 两边用同一组默认值，回给前端的页码才是实际查的那一页。
	page, pageSize := billingPagination(c)
	accounts, total, err := e.service.AdminCreditAccounts(auth.CreditAccountFilter{
		Keyword: c.Query("keyword"),
		Page:    page,
		Limit:   pageSize,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"accounts": accounts, "total": total, "page": page, "pageSize": pageSize})
}

func (e *Extension) handleAdminCreditLedger(c *gin.Context) {
	// 后台看流水必须点名账号：不带 userId 的"全部流水"在一个几十万条的表上既慢又
	// 没有意义，运营永远是从某个人进来的。
	userID := strings.TrimSpace(c.Query("userId"))
	if userID == "" {
		respondFailure(c, http.StatusBadRequest, "请指定要查询的账号")
		return
	}
	page, pageSize := billingPagination(c)
	entries, total, err := e.service.CreditLedger(auth.CreditLedgerFilter{
		UserID:   userID,
		Kind:     c.Query("kind"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"entries": entries, "total": total, "page": page, "pageSize": pageSize})
}

func (e *Extension) handleAdminCreditAdjust(c *gin.Context) {
	var input struct {
		UserID string `json:"userId"`
		Amount int64  `json:"amount"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "积分调整参数格式错误")
		return
	}
	note := strings.TrimSpace(input.Note)
	if note == "" {
		// 调整必然要能解释：没有原因的加减分在被追问时没有任何依据，而这条流水
		// 就是唯一的记录。宁可让管理员多打四个字。
		respondFailure(c, http.StatusBadRequest, "请填写调整原因")
		return
	}
	entry, err := e.service.AdjustCredits(strings.TrimSpace(input.UserID), input.Amount, note)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	wallet, err := e.service.CreditWallet(strings.TrimSpace(input.UserID))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "credit.adjust", "user", strings.TrimSpace(input.UserID), "调整积分："+note, gin.H{
		"amount":  input.Amount,
		"balance": wallet.Balance,
	})
	// 调整后的余额一并回给后台：管理员点完按钮就能看到新数值，不必再刷新一次列表。
	respondOK(c, gin.H{"entry": entry, "wallet": wallet})
}
