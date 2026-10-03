package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// assertAuthError 断言错误是可投影的 *Error，且状态与原因符合预期。
func assertAuthError(t *testing.T, err error, status int, reason string) {
	t.Helper()
	var authErr *Error
	if !errors.As(err, &authErr) {
		t.Fatalf("期望 *auth.Error，实际: %v", err)
	}
	if authErr.Status != status {
		t.Fatalf("状态码 = %d，期望 %d（%v）", authErr.Status, status, err)
	}
	if string(authErr.Reason) != reason {
		t.Fatalf("原因 = %q，期望 %q", authErr.Reason, reason)
	}
}

// recordingTransport 捕获上游请求并返回预置响应。
//
// 用它而不是 httptest：单元测试不需要真实端口，跑在受限环境里也不会因为绑不上
// 套接字而失败。
type recordingTransport struct {
	requests []*http.Request
	status   int
	body     string
	err      error
}

func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, request)
	if t.err != nil {
		return nil, t.err
	}
	status := t.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Header:     make(http.Header),
		Request:    request,
	}, nil
}

// TestNormalizePhone 确认同一号码的不同写法归一到同一个标识。
//
// 归一不彻底就会出现「+86 138-0013-8000」和「13800138000」各注册一个账号，
// 按手机号找回账号也就失去意义。
func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		input string
		want  string
		valid bool
	}{
		{input: "13800138000", want: "13800138000", valid: true},
		{input: " 138 0013 8000 ", want: "13800138000", valid: true},
		{input: "138-0013-8000", want: "13800138000", valid: true},
		{input: "(138)00138000", want: "13800138000", valid: true},
		{input: "+8613800138000", want: "13800138000", valid: true},
		{input: "+86 13800138000", want: "13800138000", valid: true},
		{input: "8613800138000", want: "13800138000", valid: true},
		{input: "12800138000", want: "12800138000", valid: false},
		{input: "1380013800", want: "1380013800", valid: false},
		{input: "138001380000", want: "138001380000", valid: false},
		{input: "1380013800a", want: "1380013800a", valid: false},
		{input: "", want: "", valid: false},
	}
	for _, tc := range cases {
		got := normalizePhone(tc.input)
		if got != tc.want {
			t.Fatalf("normalizePhone(%q) = %q，期望 %q", tc.input, got, tc.want)
		}
		if valid := mainlandPhonePattern.MatchString(got); valid != tc.valid {
			t.Fatalf("normalizePhone(%q) 校验结果 = %v，期望 %v", tc.input, valid, tc.valid)
		}
	}
}

// registerTestPhone 走真实注册入口建手机号账号，并把时钟推过冷却窗口。
func registerTestPhone(t *testing.T, env *testEnv, phone string) *User {
	t.Helper()
	ctx := context.Background()
	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodPhoneCode, Target: phone}); err != nil {
		t.Fatalf("下发注册验证码失败: %v", err)
	}
	_, code, _ := env.sms.last()
	output, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodPhoneCode,
		Target:           phone,
		Code:             code,
		AgreementVersion: CurrentAgreementVersion(),
		RequesterIP:      "203.0.113.9",
		UserAgent:        "beef-tv-test/1.0",
	})
	if err != nil {
		t.Fatalf("手机号注册失败: %v", err)
	}
	account, err := env.service.store.UserByID(output.User.ID)
	if err != nil {
		t.Fatalf("读取注册账号失败: %v", err)
	}
	env.clock.Advance(61 * time.Second)
	return account
}

// TestPhoneRegisterRecordsAgreementAndIssuesSession 覆盖手机号注册主链路。
func TestPhoneRegisterRecordsAgreementAndIssuesSession(t *testing.T) {
	env := newTestEnv(t)
	account := registerTestPhone(t, env, "13800138000")

	if account.PhoneValue() != "13800138000" {
		t.Fatalf("注册手机号不正确: %q", account.PhoneValue())
	}
	if account.EmailValue() != "" {
		t.Fatalf("手机号注册不应写入邮箱: %q", account.EmailValue())
	}
	if account.Status != StatusActive {
		t.Fatalf("注册账号状态应为 ACTIVE，实际: %q", account.Status)
	}

	var records []UserAgreement
	if err := env.db.Where("user_id = ?", account.ID).Order("agreement_type").Find(&records).Error; err != nil {
		t.Fatalf("读取协议留痕失败: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("协议留痕应为 2 条，实际 %d 条", len(records))
	}
	if records[0].Version != CurrentAgreementVersion() {
		t.Fatalf("协议版本不正确: %q", records[0].Version)
	}
}

// TestPhoneLoginAcceptsAnyWrittenForm 确认注册与登录之间号码写法可以不同。
func TestPhoneLoginAcceptsAnyWrittenForm(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	registerTestPhone(t, env, "13800138000")

	if _, err := env.service.SendCode(ctx, SendCodeInput{
		MethodType: MethodPhoneCode,
		Target:     "+86 138-0013-8000",
	}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	to, code, _ := env.sms.last()
	if to != "13800138000" {
		t.Fatalf("投递目标未归一: %q", to)
	}

	output, err := env.service.Login(ctx, LoginInput{MethodType: MethodPhoneCode, Target: "138 0013 8000", Code: code})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if output.Token == "" {
		t.Fatal("登录未签发会话令牌")
	}
	if output.User.Name != "用户8000" {
		t.Fatalf("默认展示名不正确: %q", output.User.Name)
	}
}

// TestPhoneLoginNeverCreatesAccount 与邮箱策略一致：登录不建号。
func TestPhoneLoginNeverCreatesAccount(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodPhoneCode, Target: "13900139000"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, code, _ := env.sms.last()
	_, err := env.service.Login(ctx, LoginInput{MethodType: MethodPhoneCode, Target: "13900139000", Code: code})
	assertAuthError(t, err, http.StatusNotFound, "not_found")

	var count int64
	if err := env.db.Model(&User{}).Where("phone = ?", "13900139000").Count(&count).Error; err != nil {
		t.Fatalf("统计账号失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("登录不应建号，实际建出 %d 个", count)
	}
}

// TestPhoneRegisterRejectsExistingPhone 确认判重先于消费验证码：码留给用户去登录。
func TestPhoneRegisterRejectsExistingPhone(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	registerTestPhone(t, env, "13700137000")

	if _, err := env.service.SendCode(ctx, SendCodeInput{MethodType: MethodPhoneCode, Target: "13700137000"}); err != nil {
		t.Fatalf("下发验证码失败: %v", err)
	}
	_, code, _ := env.sms.last()

	_, err := env.service.Register(ctx, RegisterInput{
		MethodType:       MethodPhoneCode,
		Target:           "13700137000",
		Code:             code,
		AgreementVersion: CurrentAgreementVersion(),
	})
	assertAuthError(t, err, http.StatusConflict, "conflict")

	// 判重若发生在消费之后就等于把码烧掉，用户还得再发一次才能登录。
	if _, err := env.service.Login(ctx, LoginInput{MethodType: MethodPhoneCode, Target: "13700137000", Code: code}); err != nil {
		t.Fatalf("判重后验证码应仍可用于登录: %v", err)
	}
}

// TestPhoneSendCodeRejectsInvalidNumber 覆盖号码格式校验。
func TestPhoneSendCodeRejectsInvalidNumber(t *testing.T) {
	env := newTestEnv(t)
	for _, target := range []string{"", "12345", "23800138000", "1380013800"} {
		_, err := env.service.SendCode(context.Background(), SendCodeInput{MethodType: MethodPhoneCode, Target: target})
		assertAuthError(t, err, http.StatusBadRequest, "invalid_argument")
	}
}

// TestPhoneSendCodeFailsWithoutSender 确认没有投递通道时显式失败，而不是假装已发送。
func TestPhoneSendCodeFailsWithoutSender(t *testing.T) {
	env := newTestEnv(t)
	store := NewStore(env.db)
	store.UseClock(env.clock.Now)
	service, err := NewService(Options{Store: store, Now: env.clock.Now, CodeCooldownSeconds: 60})
	if err != nil {
		t.Fatalf("装配认证服务失败: %v", err)
	}
	_, sendErr := service.SendCode(context.Background(), SendCodeInput{MethodType: MethodPhoneCode, Target: "13800138000"})
	assertAuthError(t, sendErr, http.StatusInternalServerError, "internal")
}

// TestDevEchoForSMSRequiresConsoleSender 把回显开关的约束也钉在短信通道上。
func TestDevEchoForSMSRequiresConsoleSender(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	cases := []struct {
		name    string
		devEcho bool
		sender  SMSSender
		want    bool
	}{
		{name: "开关关闭时不回显", devEcho: false, sender: ConsoleSMSSender{}, want: false},
		{name: "开关打开且为控制台投递时回显", devEcho: true, sender: ConsoleSMSSender{}, want: true},
		{name: "开关打开但通道已换成阿里云时不回显", devEcho: true, sender: &AliyunSMSSender{}, want: false},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(env.db)
			store.UseClock(env.clock.Now)
			service, err := NewService(Options{Store: store, SMSSender: tc.sender, DevEchoCode: tc.devEcho, Now: env.clock.Now, CodeCooldownSeconds: 60})
			if err != nil {
				t.Fatalf("装配认证服务失败: %v", err)
			}
			// 每个用例用不同号码：共用号码会撞上下发冷却，测出来的就不是回显开关。
			target := fmt.Sprintf("138001390%02d", index)
			output, err := service.SendCode(ctx, SendCodeInput{MethodType: MethodPhoneCode, Target: target})
			if err != nil {
				// 阿里云实现没有可用凭据，投递必然失败；此处只关心它不会回显。
				if tc.want {
					t.Fatalf("期望回显时下发失败: %v", err)
				}
				return
			}
			switch {
			case tc.want && output.DevCode == "":
				t.Fatal("期望回显验证码，实际为空")
			case !tc.want && output.DevCode != "":
				t.Fatalf("不应回显验证码，实际: %q", output.DevCode)
			}
		})
	}
}

// TestAliyunPercentEncodeFollowsRPCSpec 钉住与 url.QueryEscape 的三处差异。
func TestAliyunPercentEncodeFollowsRPCSpec(t *testing.T) {
	cases := map[string]string{
		"a b": "a%20b",
		"a+b": "a%2Bb",
		"a*b": "a%2Ab",
		"a~b": "a~b",
		"/":   "%2F",
	}
	for input, want := range cases {
		if got := aliyunPercentEncode(input); got != want {
			t.Fatalf("aliyunPercentEncode(%q) = %q，期望 %q", input, got, want)
		}
	}
}

// TestAliyunCanonicalQuerySortsKeys 确认按参数名排序，否则签名对不上。
func TestAliyunCanonicalQuerySortsKeys(t *testing.T) {
	query := url.Values{"b": {"2"}, "a": {"1"}, "C": {"3"}}
	if got, want := aliyunCanonicalQuery(query), "C=3&a=1&b=2"; got != want {
		t.Fatalf("规范化查询串 = %q，期望 %q", got, want)
	}
}

// TestAliyunSendLoginCodeSignsRequest 校验请求形状与签名的自洽性。
func TestAliyunSendLoginCodeSignsRequest(t *testing.T) {
	transport := &recordingTransport{body: `{"Code":"OK","Message":"OK"}`}
	sender := &AliyunSMSSender{
		AccessKeyID:     "test-key-id",
		AccessKeySecret: "test-key-secret",
		SignName:        "测试签名",
		TemplateCode:    "SMS_123456",
		Endpoint:        "https://dysmsapi.example/",
		Client:          &http.Client{Transport: transport},
	}
	if err := sender.SendLoginCode(context.Background(), "13800138000", "654321", 5); err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("上游请求数 = %d，期望 1", len(transport.requests))
	}
	captured := transport.requests[0].URL.Query()
	// AccessKeyId 曾经漏在这里：字段校验通过、签名也自洽，但请求里没有身份参数，
	// 线上只会拿到「AccessKeyId is mandatory」。必填参数要逐项断言，不能只断言签名。
	expected := map[string]string{
		"Action":        "SendSms",
		"AccessKeyId":   "test-key-id",
		"Version":       aliyunAPIVersion,
		"RegionId":      aliyunDefaultRegion,
		"PhoneNumbers":  "13800138000",
		"SignName":      "测试签名",
		"TemplateCode":  "SMS_123456",
		"TemplateParam": `{"code":"654321"}`,
	}
	for key, want := range expected {
		if got := captured.Get(key); got != want {
			t.Fatalf("参数 %s = %q，期望 %q", key, got, want)
		}
	}
	if captured.Get("SignatureNonce") == "" || captured.Get("Timestamp") == "" {
		t.Fatal("缺少防重放参数")
	}

	// 签名必须与「去掉 Signature 之后的其余参数」自洽：任何在签名之后才追加的
	// 参数都会让这条断言失败，而那正是线上最常见的签名错误。
	unsigned := url.Values{}
	for key, values := range captured {
		if key == "Signature" {
			continue
		}
		unsigned[key] = values
	}
	if got := aliyunSignature(http.MethodGet, unsigned, "test-key-secret"); got != captured.Get("Signature") {
		t.Fatalf("签名与请求参数不自洽: %q != %q", got, captured.Get("Signature"))
	}
}

// TestAliyunSendLoginCodeSurfacesUpstreamFailure 确认业务错误码与网络错误都算投递失败。
func TestAliyunSendLoginCodeSurfacesUpstreamFailure(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		err     error
		wantErr bool
	}{
		{name: "业务错误码", status: http.StatusOK, body: `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"触发流控"}`, wantErr: true},
		{name: "上游 5xx", status: http.StatusBadGateway, body: "upstream down", wantErr: true},
		{name: "响应体不是 JSON", status: http.StatusOK, body: "<html>", wantErr: true},
		{name: "网络错误", err: errors.New("connection refused"), wantErr: true},
		{name: "成功", status: http.StatusOK, body: `{"Code":"OK"}`, wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &recordingTransport{status: tc.status, body: tc.body, err: tc.err}
			sender := &AliyunSMSSender{
				AccessKeyID: "id", AccessKeySecret: "secret",
				SignName: "签名", TemplateCode: "SMS_1",
				Endpoint: "https://dysmsapi.example/",
				Client:   &http.Client{Transport: transport},
			}
			err := sender.SendLoginCode(context.Background(), "13800138000", "123456", 5)
			if tc.wantErr && err == nil {
				t.Fatal("期望投递失败，实际成功")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望投递成功，实际失败: %v", err)
			}
		})
	}
}
