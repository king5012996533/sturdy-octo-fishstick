package hosted

import (
	"net/http"
	"strings"

	"time"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 协议管理。
//
// 一处管理两件事：条款本身的版本（发布即强制重签），以及"谁在什么时候同意了哪一版"
// 的留痕。协议正文与版本存在账号库，因此这一组接口挂在托管层——只有这里同时持有
// 账号服务与审计出口。

type agreementSignatureRow struct {
	ID            string    `json:"id"`
	UserID        string    `json:"userId"`
	Email         string    `json:"email"`
	Phone         string    `json:"phone"`
	Name          string    `json:"name"`
	AgreementType string    `json:"agreementType"`
	Version       string    `json:"version"`
	AcceptedAt    time.Time `json:"acceptedAt"`
	IPAddress     string    `json:"ipAddress"`
	UserAgent     string    `json:"userAgent"`
}

func (e *Extension) registerAgreementRoutes(group *gin.RouterGroup) {
	group.GET("/agreements", e.handleAdminAgreements)
	group.PUT("/agreements", e.handleAdminPublishAgreements)
	group.GET("/agreements/signatures", e.handleAdminAgreementSignatures)
}

func (e *Extension) handleAdminAgreements(c *gin.Context) {
	view, err := e.service.AgreementAdminView()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAdminPublishAgreements(c *gin.Context) {
	var input struct {
		TermsTitle   string `json:"termsTitle"`
		TermsBody    string `json:"termsBody"`
		PrivacyTitle string `json:"privacyTitle"`
		PrivacyBody  string `json:"privacyBody"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "协议内容格式错误")
		return
	}
	actor := adminActor(c)
	view, err := e.service.PublishAgreements(auth.PublishAgreementInput{
		TermsTitle:   input.TermsTitle,
		TermsBody:    input.TermsBody,
		PrivacyTitle: input.PrivacyTitle,
		PrivacyBody:  input.PrivacyBody,
		ActorUserID:  actor.ID,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 发布即强制重签：审计里要能看到"这一版影响了多少人"，事后复盘用户投诉时靠它。
	e.recordAudit(c, "agreement.publish", "agreement", view.Version, "发布新版用户协议与隐私政策", gin.H{
		"version":      view.Version,
		"pendingUsers": view.PendingUsers,
	})
	respondOK(c, view)
}

func (e *Extension) handleAdminAgreementSignatures(c *gin.Context) {
	rows, total, pageSize, err := e.service.AgreementSignatures(auth.AgreementSignatureFilter{
		Version:       strings.TrimSpace(c.Query("version")),
		AgreementType: strings.TrimSpace(c.Query("type")),
		Keyword:       c.Query("keyword"),
		Page:          adminIntQuery(c, "page"),
		Limit:         adminIntQuery(c, "pageSize"),
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	response := make([]agreementSignatureRow, 0, len(rows))
	for _, row := range rows {
		response = append(response, agreementSignatureRow{
			ID:            row.ID,
			UserID:        row.UserID,
			Email:         row.Email,
			Phone:         row.Phone,
			Name:          row.Name,
			AgreementType: row.AgreementType,
			Version:       row.Version,
			AcceptedAt:    row.AcceptedAt,
			IPAddress:     row.IPAddress,
			UserAgent:     row.UserAgent,
		})
	}
	page := adminIntQuery(c, "page")
	if page < 1 {
		page = 1
	}
	respondOK(c, gin.H{"signatures": response, "total": total, "page": page, "pageSize": pageSize})
}
