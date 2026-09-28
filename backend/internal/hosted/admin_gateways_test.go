package hosted

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type gatewayViewResponse struct {
	Data struct {
		SMTP struct {
			Channel   string `json:"channel"`
			Enabled   bool   `json:"enabled"`
			Source    string `json:"source"`
			Ready     bool   `json:"ready"`
			Detail    string `json:"detail"`
			UpdatedBy string `json:"updatedBy"`
			SMTP      *struct {
				Host        string `json:"host"`
				Port        int    `json:"port"`
				Password    string `json:"password"`
				HasPassword bool   `json:"hasPassword"`
				From        string `json:"from"`
			} `json:"smtp"`
		} `json:"smtp"`
		SMS struct {
			Channel string `json:"channel"`
			Enabled bool   `json:"enabled"`
			Source  string `json:"source"`
			Ready   bool   `json:"ready"`
			SMS     *struct {
				AccessKeyID        string `json:"accessKeyId"`
				AccessKeySecret    string `json:"accessKeySecret"`
				HasAccessKeySecret bool   `json:"hasAccessKeySecret"`
				SignName           string `json:"signName"`
				TemplateCode       string `json:"templateCode"`
			} `json:"sms"`
		} `json:"sms"`
	} `json:"data"`
}

func gatewayView(t *testing.T, recorder *httptest.ResponseRecorder) gatewayViewResponse {
	t.Helper()
	var payload gatewayViewResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析网关响应失败: %v %s", err, recorder.Body.String())
	}
	return payload
}

// TestHostedGatewaySettingsKeepSecretsWriteOnly 覆盖「保存 → 密钥只写不读 → 留空的密钥不被清空」。
func TestHostedGatewaySettingsKeepSecretsWriteOnly(t *testing.T) {
	extension, router, authDB, canvasDB := newAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-gateway@example.com")
	promoteToAdmin(t, authDB, adminID)

	// 没有任何配置时要说清楚现状：验证码当前只写日志。
	recorder := perform(router, http.MethodGet, "/api/admin/gateways", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取网关配置失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if initial := gatewayView(t, recorder); initial.Data.SMTP.Source != "console" || initial.Data.SMTP.Ready {
		t.Fatalf("未配置时应报告控制台投递：%s", recorder.Body.String())
	}

	// 启用但缺必填项必须被拒。
	if recorder := perform(router, http.MethodPut, "/api/admin/gateways/SMTP", `{"enabled":true,"smtp":{"host":"","from":""}}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("启用邮件通道缺服务器应被拒，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPut, "/api/admin/gateways/SMS", `{"enabled":true,"sms":{"accessKeyId":"ak","signName":"KinoTV","templateCode":"SMS_1"}}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("启用短信通道缺密钥应被拒，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 保存邮件通道：读接口只能看到"已设置密码"，看不到密码本身。
	recorder = perform(router, http.MethodPut, "/api/admin/gateways/SMTP",
		`{"enabled":true,"smtp":{"host":"127.0.0.1","port":1,"username":"mailer","password":"smtp-secret","from":"noreply@kinotv.example","fromName":"KinoTV"}}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("保存邮件网关失败：%d %s", recorder.Code, recorder.Body.String())
	}
	smtpView := gatewayView(t, recorder).Data.SMTP
	if smtpView.Source != "database" || !smtpView.Ready || smtpView.SMTP == nil {
		t.Fatalf("保存后应报告后台配置生效：%s", recorder.Body.String())
	}
	if smtpView.SMTP.Password != "" || !smtpView.SMTP.HasPassword {
		t.Fatalf("密码不得回传，只能回 hasPassword：%s", recorder.Body.String())
	}

	// 改发件人但密钥留空：旧密码必须保留，否则每次编辑都会把密钥弄丢。
	recorder = perform(router, http.MethodPut, "/api/admin/gateways/SMTP",
		`{"enabled":true,"smtp":{"host":"127.0.0.1","port":1,"username":"mailer","from":"billing@kinotv.example"}}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("更新邮件网关失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if updated := gatewayView(t, recorder).Data.SMTP; updated.SMTP == nil || !updated.SMTP.HasPassword || updated.SMTP.From != "billing@kinotv.example" {
		t.Fatalf("留空密钥应保持原值：%s", recorder.Body.String())
	}

	// 短信通道同理。
	recorder = perform(router, http.MethodPut, "/api/admin/gateways/SMS",
		`{"enabled":true,"sms":{"accessKeyId":"LTAI-demo","accessKeySecret":"sms-secret","signName":"KinoTV","templateCode":"SMS_123","templateParamKey":"code","regionId":"cn-hangzhou"}}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("保存短信网关失败：%d %s", recorder.Code, recorder.Body.String())
	}
	smsView := gatewayView(t, recorder).Data.SMS
	if smsView.SMS == nil || smsView.SMS.AccessKeySecret != "" || !smsView.SMS.HasAccessKeySecret {
		t.Fatalf("短信密钥不得回传，只能回 hasAccessKeySecret：%s", recorder.Body.String())
	}
	if !smsView.SMS.HasAccessKeySecret || smsView.SMS.SignName != "KinoTV" {
		t.Fatalf("短信配置应回显非密钥字段：%s", recorder.Body.String())
	}

	// 测试发送必须真的去投递：这里指向必然连不上的端口，期望显式失败而不是假成功。
	recorder = perform(router, http.MethodPost, "/api/admin/gateways/SMTP/test", `{"target":"someone@example.com"}`, adminCookie)
	if recorder.Code == http.StatusOK {
		t.Fatalf("投递失败时不能返回成功：%s", recorder.Body.String())
	}
	// 失败原因要能直接给运营看：折叠成"系统处理失败"等于让人靠猜。
	body := recorder.Body.String()
	if recorder.Code != http.StatusBadGateway || !strings.Contains(body, "邮件通道发送失败") {
		t.Fatalf("投递失败应回显真实原因：%d %s", recorder.Code, body)
	}
	if strings.Contains(body, "系统处理失败") {
		t.Fatalf("投递失败不应折叠成通用错误：%s", body)
	}
	if strings.Contains(body, "smtp-secret") {
		t.Fatalf("失败原因里不得出现密钥：%s", body)
	}

	// 密钥不进审计：留痕只记"改了哪个通道、开关状态"。
	var auditCount int64
	if err := canvasDB.Table("admin_audit_events").Where("action = ?", "gateway.update").Count(&auditCount).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if auditCount < 3 {
		t.Fatalf("三次保存应留下至少 3 条审计，实际 %d 条", auditCount)
	}
	var secrets int64
	if err := canvasDB.Table("admin_audit_events").Where("metadata_json LIKE ?", "%smtp-secret%").Count(&secrets).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if secrets != 0 {
		t.Fatalf("审计里不得出现网关密钥，实际命中 %d 条", secrets)
	}

	// 配置库里存的必须是密文：明文落库等于把 SMTP 密码直接摊在数据库里。
	var stored string
	if err := authDB.Table("auth_gateway_configs").Where("channel = ?", "SMTP").Pluck("config_json", &stored).Error; err != nil {
		t.Fatalf("读取网关配置失败: %v", err)
	}
	if stored == "" || containsAny(stored, "smtp-secret", "127.0.0.1") {
		t.Fatalf("网关配置必须以密文存储：%s", stored)
	}
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && len(needle) <= len(haystack) {
			for index := 0; index+len(needle) <= len(haystack); index++ {
				if haystack[index:index+len(needle)] == needle {
					return true
				}
			}
		}
	}
	return false
}
