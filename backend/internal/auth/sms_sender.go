package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// SMSSender 负责投递短信验证码。
//
// 与 EmailSender 同构：策略层只依赖这个接口，因此换服务商（阿里云、腾讯云、
// 自建网关）只是换一个实现。验证码只经由此通道下发，任何实现都不得把它写进
// HTTP 响应。
type SMSSender interface {
	SendLoginCode(ctx context.Context, to string, code string, expireMinutes int) error
}

// ConsoleSMSSender 只把验证码写进服务端日志，用于本地开发和联调。
//
// 它不适用于生产：日志可见者等于可以登录任意账号。
type ConsoleSMSSender struct{}

func (ConsoleSMSSender) SendLoginCode(_ context.Context, to string, code string, expireMinutes int) error {
	log.Printf("auth: 短信验证码（本地投递，未真实发送）to=%s code=%s 有效期=%d分钟", to, code, expireMinutes)
	return nil
}

// devOnlySender 标记「只写日志、不做真实投递」的通道实现。
//
// devEcho 靠这个标记而不是一张类型清单判断能否回显验证码：新增投递通道时，
// 它是否属于本地联调通道由类型自己声明，不需要回头改 devEcho 的白名单。
type devOnlySender interface{ devOnlySender() }

func (ConsoleSender) devOnlySender()    {}
func (ConsoleSMSSender) devOnlySender() {}

const (
	aliyunDefaultEndpoint = "https://dysmsapi.aliyuncs.com/"
	aliyunDefaultRegion   = "cn-hangzhou"
	aliyunAPIVersion      = "2017-05-25"
	// aliyunDefaultTemplateKey 是阿里云短信模板里验证码变量的名字，模板通常写成
	// 你的验证码是 ${code}。模板各不相同，因此允许用环境变量覆盖。
	aliyunDefaultTemplateKey = "code"
)

// AliyunSMSSender 通过阿里云短信服务（Dysmsapi 2017-05-25）投递验证码。
//
// 这里手写 RPC 签名而不是引入 SDK：需要的只是 SendSms 一个动作，而阿里云 SDK
// 会带进一串传递依赖。
type AliyunSMSSender struct {
	AccessKeyID     string
	AccessKeySecret string
	SignName        string
	TemplateCode    string
	// TemplateParamKey 是模板中验证码变量的名字，留空时用 code。
	TemplateParamKey string
	RegionID         string
	Endpoint         string
	Client           *http.Client
}

func (s *AliyunSMSSender) SendLoginCode(ctx context.Context, to string, code string, _ int) error {
	if s == nil {
		return errors.New("短信投递器未初始化")
	}
	if strings.TrimSpace(s.AccessKeyID) == "" || strings.TrimSpace(s.AccessKeySecret) == "" {
		return errors.New("阿里云短信缺少 AccessKey")
	}
	if strings.TrimSpace(s.SignName) == "" || strings.TrimSpace(s.TemplateCode) == "" {
		return errors.New("阿里云短信缺少短信签名或模板编号")
	}

	templateParam, err := json.Marshal(map[string]string{firstNonEmpty(s.TemplateParamKey, aliyunDefaultTemplateKey): code})
	if err != nil {
		return err
	}

	query := url.Values{}
	query.Set("Action", "SendSms")
	// 身份参数必须进 query：少了它阿里云只会回一句「AccessKeyId is mandatory」，
	// 而本地签名照样能算出来，看起来像"签名不对"，实际是参数根本没带上。
	query.Set("AccessKeyId", strings.TrimSpace(s.AccessKeyID))
	query.Set("Version", aliyunAPIVersion)
	query.Set("RegionId", firstNonEmpty(s.RegionID, aliyunDefaultRegion))
	query.Set("PhoneNumbers", strings.TrimSpace(to))
	query.Set("SignName", strings.TrimSpace(s.SignName))
	query.Set("TemplateCode", strings.TrimSpace(s.TemplateCode))
	query.Set("TemplateParam", string(templateParam))
	query.Set("Format", "JSON")
	query.Set("SignatureMethod", "HMAC-SHA1")
	query.Set("SignatureVersion", "1.0")
	query.Set("SignatureNonce", aliyunNonce())
	query.Set("Timestamp", time.Now().UTC().Format("2006-01-02T15:04:05Z"))

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, aliyunRequestURL(firstNonEmpty(s.Endpoint, aliyunDefaultEndpoint), query, s.AccessKeySecret), nil)
	if err != nil {
		return err
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		// 具体原因只进服务端日志：响应体可能含账号信息，不该被带到调用方。
		log.Printf("auth: 阿里云短信投递失败 status=%d body=%s", response.StatusCode, truncate(string(body), 500))
		return fmt.Errorf("阿里云短信返回状态码 %d", response.StatusCode)
	}
	var payload struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	if payload.Code != "OK" {
		log.Printf("auth: 阿里云短信投递失败 code=%s message=%s", payload.Code, payload.Message)
		return fmt.Errorf("阿里云短信投递失败: %s", payload.Code)
	}
	return nil
}

// SMSSenderFromEnv 从环境变量构造短信投递器。
//
// 配了 AccessKey 却缺签名或模板时直接报错，不静默回落到控制台：那会让人以为
// 短信已经开通，实际只是写日志，直到线上用户收不到码才暴露。
func SMSSenderFromEnv() (SMSSender, error) {
	accessKeyID := strings.TrimSpace(os.Getenv("BEEFTV_SMS_ACCESS_KEY_ID"))
	if accessKeyID == "" {
		return nil, errors.New("缺少 BEEFTV_SMS_ACCESS_KEY_ID")
	}
	accessKeySecret := strings.TrimSpace(os.Getenv("BEEFTV_SMS_ACCESS_KEY_SECRET"))
	if accessKeySecret == "" {
		return nil, errors.New("缺少 BEEFTV_SMS_ACCESS_KEY_SECRET")
	}
	signName := strings.TrimSpace(os.Getenv("BEEFTV_SMS_SIGN_NAME"))
	if signName == "" {
		return nil, errors.New("缺少 BEEFTV_SMS_SIGN_NAME")
	}
	templateCode := strings.TrimSpace(os.Getenv("BEEFTV_SMS_TEMPLATE_CODE"))
	if templateCode == "" {
		return nil, errors.New("缺少 BEEFTV_SMS_TEMPLATE_CODE")
	}
	return &AliyunSMSSender{
		AccessKeyID:      accessKeyID,
		AccessKeySecret:  accessKeySecret,
		SignName:         signName,
		TemplateCode:     templateCode,
		TemplateParamKey: strings.TrimSpace(os.Getenv("BEEFTV_SMS_TEMPLATE_PARAM_KEY")),
		RegionID:         strings.TrimSpace(os.Getenv("BEEFTV_SMS_REGION_ID")),
		Endpoint:         strings.TrimSpace(os.Getenv("BEEFTV_SMS_ENDPOINT")),
	}, nil
}

// aliyunRequestURL 拼出带签名的请求地址。
func aliyunRequestURL(endpoint string, query url.Values, secret string) string {
	signed := url.Values{}
	for key, values := range query {
		signed[key] = values
	}
	signed.Set("Signature", aliyunSignature(http.MethodGet, query, secret))
	target, err := url.Parse(endpoint)
	if err != nil {
		// endpoint 来自配置，非法时退化成必然失败的地址，让调用方拿到明确的网络
		// 错误，而不是在这里 panic。
		return endpoint + "?" + aliyunCanonicalQuery(signed)
	}
	target.RawQuery = aliyunCanonicalQuery(signed)
	return target.String()
}

// aliyunSignature 计算阿里云 RPC 风格的 HMAC-SHA1 签名。
func aliyunSignature(method string, query url.Values, secret string) string {
	stringToSign := method + "&" + aliyunPercentEncode("/") + "&" + aliyunPercentEncode(aliyunCanonicalQuery(query))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// aliyunCanonicalQuery 按参数名排序并逐项编码；签名的计算与最终请求必须共用它。
func aliyunCanonicalQuery(query url.Values) string {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, aliyunPercentEncode(key)+"="+aliyunPercentEncode(query.Get(key)))
	}
	return strings.Join(parts, "&")
}

// aliyunPercentEncode 按阿里云签名规范做 RFC3986 编码。
//
// 不能直接用 url.QueryEscape：它把空格编成加号，也不转义星号，两者都会让本地
// 算出的签名和阿里云算出来的对不上。
func aliyunPercentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func aliyunNonce() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
