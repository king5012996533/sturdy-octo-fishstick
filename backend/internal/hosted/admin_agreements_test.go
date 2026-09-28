package hosted

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	agreementTestTermsBody   = "一、账号：你需要使用真实有效的邮箱完成注册，并对账号下发生的一切行为负责；账号仅限本人使用，不得出租、出借、转让或与第三方共享，发现被盗用应立即联系我们。二、服务内容：本服务提供 AI 创作、画布编排、模型调用与素材管理能力，具体功能、可用模型与额度以页面实际展示为准，我们可能根据运营需要调整。三、付费与额度：需要付费的功能以下单页展示的价格与额度为准，法律另有规定的除外。四、你的内容与责任：你对自己上传、生成、发布的内容负责，并保证拥有相应权利，不得利用本服务制作或传播违反法律法规、侵害他人权益、危害未成年人的内容。五、服务变更与终止：你若严重违反本协议，我们有权限制或终止你对本服务的使用。六、其他：本协议的订立、效力与争议解决适用中华人民共和国法律。"
	agreementTestPrivacyBody = "本政策说明我们在你使用服务期间如何收集、使用、存储和保护你的个人信息。一、我们收集的信息：账号信息（注册邮箱，以及登录时间、来源 IP 与浏览器 User-Agent）、使用信息（你创建的项目、上传的素材、调用的模型与产生的任务记录）；银行卡等支付要素由支付机构直接处理，我们不存储。二、我们如何使用这些信息：提供并维护服务、完成账号鉴权与订单结算；识别异常登录、防止刷量与滥用；履行法律法规规定的义务。三、我们如何共享：除取得你的单独同意、为完成你请求的服务而必须共享、法律法规要求外，我们不会向第三方提供，也不会出售你的个人信息。四、存储与保护：我们采取访问控制与传输加密等措施保护你的信息，会话凭据在服务端仅以摘要形式保存。五、你的权利：你可以要求查询、更正、删除你的个人信息或注销账号，注销后将删除或匿名化相关信息。六、联系我们：如对本政策有疑问，可通过站内客服渠道与我们联系。"
)

type agreementAdminResponse struct {
	Data struct {
		Version      string `json:"version"`
		Configured   bool   `json:"configured"`
		PendingUsers int64  `json:"pendingUsers"`
		Documents    []struct {
			Type string `json:"type"`
			Body string `json:"body"`
		} `json:"documents"`
		History []struct {
			Version string `json:"version"`
			Current bool   `json:"current"`
		} `json:"history"`
	} `json:"data"`
}

func agreementAdminPayload(t *testing.T, recorder *httptest.ResponseRecorder) agreementAdminResponse {
	t.Helper()
	var payload agreementAdminResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析协议响应失败: %v %s", err, recorder.Body.String())
	}
	return payload
}

// TestHostedAgreementPublishForcesReconsent 覆盖「发布新版 → 老用户被强制重签 → 留痕」。
func TestHostedAgreementPublishForcesReconsent(t *testing.T) {
	extension, router, authDB, canvasDB := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-agreement@example.com")
	promoteToAdmin(t, authDB, adminID)
	userCookie, _ := registerAccount(t, router, authDB, "reader@example.com")

	// 还没发布过任何一版时用内置骨架：注册页可用，但要明确告诉后台这不是真实条款。
	recorder := perform(router, http.MethodGet, "/api/admin/agreements", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取协议管理失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if initial := agreementAdminPayload(t, recorder); initial.Data.Configured || initial.Data.Version == "" {
		t.Fatalf("未发布时应回落内置骨架并标记未配置：%s", recorder.Body.String())
	}

	// 占位骨架不能上线：正文过短必须被拒。
	if recorder := perform(router, http.MethodPut, "/api/admin/agreements",
		`{"termsTitle":"用户协议","termsBody":"太短","privacyTitle":"隐私政策","privacyBody":"`+agreementTestPrivacyBody+`"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("过短的正文应被拒绝，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	body, err := json.Marshal(map[string]string{
		"termsTitle":   "用户协议",
		"termsBody":    agreementTestTermsBody,
		"privacyTitle": "隐私政策",
		"privacyBody":  agreementTestPrivacyBody,
	})
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	recorder = perform(router, http.MethodPut, "/api/admin/agreements", string(body), adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("发布协议失败：%d %s", recorder.Code, recorder.Body.String())
	}
	published := agreementAdminPayload(t, recorder)
	version := published.Data.Version
	// 版本串以发布日期为基准：同日再次发布追加序号，避免覆盖上一版。
	if !published.Data.Configured || !strings.HasPrefix(version, time.Now().Format("2006-01-02")) {
		t.Fatalf("发布后应以发布日期作为版本：%s", recorder.Body.String())
	}
	if len(published.Data.History) != 1 || !published.Data.History[0].Current {
		t.Fatalf("发布后历史里应有且只有当前这一版：%s", recorder.Body.String())
	}

	// 老用户现在签的是旧版本：会话必须显式告诉前端要重签。
	var session struct {
		Data struct {
			Agreements struct {
				CurrentVersion  string `json:"currentVersion"`
				AcceptedVersion string `json:"acceptedVersion"`
				Accepted        bool   `json:"accepted"`
			} `json:"agreements"`
		} `json:"data"`
	}
	recorder = perform(router, http.MethodGet, "/api/auth/session", "", userCookie)
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatalf("解析会话失败: %v %s", err, recorder.Body.String())
	}
	if session.Data.Agreements.Accepted || session.Data.Agreements.CurrentVersion != version {
		t.Fatalf("发布新版后老用户应处于待重签状态：%s", recorder.Body.String())
	}

	// 拿旧版本号来补签要被拒：留痕必须落在真实生效的那一版上。
	if recorder := perform(router, http.MethodPost, "/api/auth/agreements/accept", `{"version":"2020-01-01"}`, userCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("过期版本号应被拒绝，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/auth/agreements/accept", `{"version":"`+version+`"}`, userCookie); recorder.Code != http.StatusOK {
		t.Fatalf("重新同意失败：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodGet, "/api/auth/session", "", userCookie)
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatalf("解析会话失败: %v %s", err, recorder.Body.String())
	}
	if !session.Data.Agreements.Accepted {
		t.Fatalf("补签后应不再要求重签：%s", recorder.Body.String())
	}

	// 签署记录要能按版本查到，并且带上账号标识。
	recorder = perform(router, http.MethodGet, "/api/admin/agreements/signatures?version="+version, "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取签署记录失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var signatures struct {
		Data struct {
			Signatures []struct {
				Email         string `json:"email"`
				AgreementType string `json:"agreementType"`
				Version       string `json:"version"`
			} `json:"signatures"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &signatures); err != nil {
		t.Fatalf("解析签署记录失败: %v %s", err, recorder.Body.String())
	}
	if signatures.Data.Total < 2 {
		t.Fatalf("该版本应留下两份签署记录（协议 + 隐私政策），实际 %d：%s", signatures.Data.Total, recorder.Body.String())
	}
	for _, row := range signatures.Data.Signatures {
		if row.Version != version || !strings.Contains(row.Email, "@example.com") {
			t.Fatalf("签署记录缺少账号标识或版本：%s", recorder.Body.String())
		}
	}

	// 发布新版本属于强制影响全站写操作，必须留痕。
	var auditCount int64
	if err := canvasDB.Table("admin_audit_events").Where("action = ?", "agreement.publish").Count(&auditCount).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("发布协议应留下 1 条审计，实际 %d 条", auditCount)
	}
}
