package hosted

import (
	"log"
	"net/http"
	"strings"
	"time"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 用户端的账号注销接口。
//
// 注销是不可逆的账号级动作，所以这一组接口全部以会话账号为主体：请求体里不接受
// 任何用户标识，避免长出一个「拿别人的 ID 申请注销」的入口。确认身份用账号自己
// 绑定的邮箱/手机号验证码，具体校验在 auth 域内完成。

// accountDeletionJanitorInterval 是到期注销的执行周期。
//
// 冷静期以天计，本地执行只是把 arXiv「已到期」的申请落地，跑得比 1 小时更密没有
// 收益；但也不能只在启动时扫一遍——服务重启是不可预期的，用户到期却一直没被注销
// 会变成一条对外承诺不了的隐私条款。
const accountDeletionJanitorInterval = time.Hour

func (e *Extension) registerAccountDeletionRoutes(api *gin.RouterGroup) {
	api.GET("/finance/account/deletion", e.handleAccountDeletionStatus)
	api.POST("/finance/account/deletion", e.handleAccountDeletionRequest)
	api.DELETE("/finance/account/deletion", e.handleAccountDeletionCancel)
}

func (e *Extension) handleAccountDeletionStatus(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	view, err := e.service.AccountDeletionStatus(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAccountDeletionRequest(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	var input struct {
		MethodType string `json:"methodType"`
		Code       string `json:"code"`
		Reason     string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "注销申请参数格式错误")
		return
	}
	view, err := e.service.RequestAccountDeletion(c.Request.Context(), auth.AccountDeletionRequest{
		UserID:      user.ID,
		MethodType:  auth.MethodType(strings.ToUpper(strings.TrimSpace(input.MethodType))),
		Code:        input.Code,
		Reason:      input.Reason,
		RequesterIP: c.ClientIP(),
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAccountDeletionCancel(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	view, err := e.service.CancelAccountDeletion(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

// sessionUser 取当前会话账号并按统一文案回 401。
func (e *Extension) sessionUser(c *gin.Context) (*auth.AuthUser, bool) {
	user, err := e.service.SessionUser(c.Request.Context(), auth.ReadSessionToken(c.Request))
	if err != nil {
		respondFailure(c, http.StatusUnauthorized, "当前未登录或登录已失效")
		return nil, false
	}
	return user, true
}

// startAccountDeletionJanitor 启动到期注销执行协程：先立刻扫一遍（补上停机期间
// 到期的申请），之后按小时执行。
func (e *Extension) startAccountDeletionJanitor() {
	if e == nil || e.service == nil {
		return
	}
	e.accountJanitorOnce.Do(func() {
		stop := make(chan struct{})
		e.accountJanitorStop = stop
		go func() {
			sweep := func() {
				count, err := e.service.ExecuteDueAccountDeletions(0)
				if err != nil {
					log.Printf("hosted: 执行到期注销失败: %v", err)
					return
				}
				if count > 0 {
					// 注销是隐私承诺的一部分，必须留下可回溯的执行记录。
					log.Printf("hosted: 已执行 %d 个到期注销申请（账号已匿名化）", count)
				}
			}
			sweep()
			ticker := time.NewTicker(accountDeletionJanitorInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					sweep()
				}
			}
		}()
	})
}
