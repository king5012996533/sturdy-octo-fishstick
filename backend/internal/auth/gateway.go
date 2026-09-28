package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

// 网关配置的业务层：读取（脱敏）、保存（密钥留空即保持）、以及在投递时按配置解析
// 出真正的发送器。

const gatewayTestTimeout = 15 * time.Second

// gatewayResolver 让投递通道可以在运行期变化。
//
// 注册时序决定的现实：Service 在启动时装配，而网关配置可能在任何一个时刻被后台
// 改掉。因此这里不缓存"最终结果"，而是每次投递前解析一次——验证码下发本来就是
// 低频操作，多一次数据库查询换掉一次重启。
type gatewayResolver struct {
	store  *Store
	cipher *gatewayCipher
	email  EmailSender
	sms    SMSSender
	client *http.Client

	mu         sync.Mutex
	warnedSMTP bool
	warnedSMS  bool
}

func (r *gatewayResolver) ResolveEmail() EmailSender {
	config, enabled, err := r.smtpConfig()
	if err != nil {
		r.warnOnce(&r.warnedSMTP, "读取邮件网关配置失败", err)
		return r.email
	}
	if config == nil || !enabled {
		return r.email
	}
	port := config.Port
	if port == 0 {
		port = 587
	}
	return &SMTPSender{
		Host:     config.Host,
		Port:     port,
		Username: config.Username,
		Password: config.Password,
		From:     config.From,
		FromName: config.FromName,
	}
}

func (r *gatewayResolver) ResolveSMS() SMSSender {
	config, enabled, err := r.smsConfig()
	if err != nil {
		r.warnOnce(&r.warnedSMS, "读取短信网关配置失败", err)
		return r.sms
	}
	if config == nil || !enabled {
		return r.sms
	}
	return &AliyunSMSSender{
		AccessKeyID:      config.AccessKeyID,
		AccessKeySecret:  config.AccessKeySecret,
		SignName:         config.SignName,
		TemplateCode:     config.TemplateCode,
		TemplateParamKey: config.TemplateParamKey,
		RegionID:         config.RegionID,
		Endpoint:         config.Endpoint,
		Client:           r.client,
	}
}

// warnOnce 只提示一次：配置读不出来时每个验证码都打一行日志，会把真正的错误淹掉。
func (r *gatewayResolver) warnOnce(flag *bool, message string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if *flag {
		return
	}
	*flag = true
	log.Printf("auth: %s: %v（已回落到环境变量/日志投递）", message, err)
}

func (r *gatewayResolver) smtpConfig() (*SMTPGatewayConfig, bool, error) {
	plaintext, enabled, err := r.decrypted(GatewayChannelSMTP)
	if err != nil || plaintext == nil {
		return nil, false, err
	}
	var config SMTPGatewayConfig
	if err := json.Unmarshal(plaintext, &config); err != nil {
		return nil, false, fmt.Errorf("邮件网关配置解析失败: %w", err)
	}
	return &config, enabled, nil
}

func (r *gatewayResolver) smsConfig() (*SMSGatewayConfig, bool, error) {
	plaintext, enabled, err := r.decrypted(GatewayChannelSMS)
	if err != nil || plaintext == nil {
		return nil, false, err
	}
	var config SMSGatewayConfig
	if err := json.Unmarshal(plaintext, &config); err != nil {
		return nil, false, fmt.Errorf("短信网关配置解析失败: %w", err)
	}
	return &config, enabled, nil
}

func (r *gatewayResolver) decrypted(channel string) ([]byte, bool, error) {
	record, err := r.store.GatewayConfig(channel)
	if errors.Is(err, ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	plaintext, err := r.cipher.Decrypt(record.ConfigJSON)
	if err != nil {
		return nil, false, err
	}
	return plaintext, record.Enabled, nil
}

// AdminGateways 返回两个通道的脱敏配置。
func (s *Service) AdminGateways() (*GatewaySettingsView, error) {
	view := &GatewaySettingsView{
		SMTP: s.gatewayChannelView(GatewayChannelSMTP),
		SMS:  s.gatewayChannelView(GatewayChannelSMS),
	}
	return view, nil
}

func (s *Service) gatewayChannelView(channel string) GatewayChannelView {
	view := GatewayChannelView{Channel: channel}
	record, err := s.store.GatewayConfig(channel)
	if err == nil && record != nil {
		view.Enabled = record.Enabled
		view.Source = "database"
		view.UpdatedBy = record.UpdatedBy
		updatedAt := record.UpdatedAt
		view.UpdatedAt = &updatedAt
		plaintext, decryptErr := s.gatewayCipher().Decrypt(record.ConfigJSON)
		if decryptErr != nil {
			view.Detail = decryptErr.Error()
			return view
		}
		if channel == GatewayChannelSMTP {
			var config SMTPGatewayConfig
			if json.Unmarshal(plaintext, &config) == nil {
				config.HasPassword = strings.TrimSpace(config.Password) != ""
				config.Password = ""
				view.SMTP = &config
			}
		} else {
			var config SMSGatewayConfig
			if json.Unmarshal(plaintext, &config) == nil {
				config.HasAccessKeySecret = strings.TrimSpace(config.AccessKeySecret) != ""
				config.AccessKeySecret = ""
				view.SMS = &config
			}
		}
		if !record.Enabled {
			view.Detail = "后台配置为停用，验证码会走环境变量通道"
			return view
		}
		view.Ready = true
		view.Detail = "验证码按后台配置投递"
		return view
	}
	// 没有后台配置时报告环境变量的实际状态：运营最需要知道的就是"现在到底发不发得出去"。
	if channel == GatewayChannelSMS {
		smsSender := s.resolveSMSSender()
		if _, devOnly := smsSender.(devOnlySender); devOnly || smsSender == nil {
			view.Enabled = false
			view.Source = "console"
			view.Detail = "未配置短信通道，验证码只写入服务端日志"
			return view
		}
		view.Enabled = true
		view.Source = "environment"
		view.Ready = true
		view.Detail = "使用环境变量 BEEFTV_SMS_* 投递"
		return view
	}
	sender := s.resolveEmailSender()
	if _, devOnly := sender.(devOnlySender); devOnly || sender == nil {
		view.Enabled = false
		view.Source = "console"
		view.Detail = "未配置 SMTP，验证码只写入服务端日志"
		return view
	}
	view.Enabled = true
	view.Source = "environment"
	view.Ready = true
	view.Detail = "使用环境变量 BEEFTV_SMTP_* 投递"
	return view
}

// UpdateGateway 保存一个通道的配置。
//
// 密钥留空表示保持原值：后台读不到密钥，空值若按"清空"处理，改一次发件人就会把
// 密钥弄丢，而界面上看不出任何异常。
func (s *Service) UpdateGateway(channel string, input GatewayUpdateInput, actorUserID string) (*GatewaySettingsView, error) {
	cipher, err := s.gatewayCipherChecked()
	if err != nil {
		return nil, internalFailure(err)
	}
	var payload any
	switch channel {
	case GatewayChannelSMTP:
		if input.SMTP == nil {
			return nil, invalidArgument("缺少邮件网关配置")
		}
		config := *input.SMTP
		config.Host = strings.TrimSpace(config.Host)
		config.From = strings.TrimSpace(config.From)
		config.Username = strings.TrimSpace(config.Username)
		config.FromName = strings.TrimSpace(config.FromName)
		if config.Port == 0 {
			config.Port = 587
		}
		if config.Port < 1 || config.Port > 65535 {
			return nil, invalidArgument("SMTP 端口必须是 1-65535 的整数")
		}
		if input.Enabled {
			if config.Host == "" || config.From == "" {
				return nil, invalidArgument("启用邮件通道前必须填写 SMTP 服务器与发件人地址")
			}
		}
		if strings.TrimSpace(config.Password) == "" {
			existing, err := s.existingSMTP()
			if err != nil {
				return nil, err
			}
			if existing != nil {
				config.Password = existing.Password
			}
		}
		payload = config
	case GatewayChannelSMS:
		if input.SMS == nil {
			return nil, invalidArgument("缺少短信网关配置")
		}
		config := *input.SMS
		config.AccessKeyID = strings.TrimSpace(config.AccessKeyID)
		config.SignName = strings.TrimSpace(config.SignName)
		config.TemplateCode = strings.TrimSpace(config.TemplateCode)
		config.TemplateParamKey = strings.TrimSpace(config.TemplateParamKey)
		config.RegionID = strings.TrimSpace(config.RegionID)
		config.Endpoint = strings.TrimSpace(config.Endpoint)
		if input.Enabled {
			if config.AccessKeyID == "" || config.SignName == "" || config.TemplateCode == "" {
				return nil, invalidArgument("启用短信通道前必须填写 AccessKey、短信签名与模板编号")
			}
		}
		if strings.TrimSpace(config.AccessKeySecret) == "" {
			existing, err := s.existingSMS()
			if err != nil {
				return nil, err
			}
			if existing != nil {
				config.AccessKeySecret = existing.AccessKeySecret
			}
		}
		if input.Enabled && strings.TrimSpace(config.AccessKeySecret) == "" {
			return nil, invalidArgument("启用短信通道前必须填写 AccessKeySecret")
		}
		payload = config
	default:
		return nil, invalidArgument("未知的网关通道")
	}

	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, internalFailure(err)
	}
	encrypted, err := cipher.Encrypt(plaintext)
	if err != nil {
		return nil, internalFailure(err)
	}
	if err := s.store.SaveGatewayConfig(&GatewayConfig{
		Channel:    channel,
		ConfigJSON: encrypted,
		Enabled:    input.Enabled,
		UpdatedAt:  s.now(),
		UpdatedBy:  strings.TrimSpace(actorUserID),
	}); err != nil {
		return nil, internalFailure(err)
	}
	return s.AdminGateways()
}

// TestGateway 用当前生效的配置发一条真实验证码。
//
// 存在的意义是"能不能发出去"这个问题只能靠真发一次回答：填错了签名或模板编号，
// 保存时看不出来，上线后才发现用户收不到码。
func (s *Service) TestGateway(ctx context.Context, channel string, target string) (string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", invalidArgument("请填写接收验证码的地址")
	}
	code, err := generateVerificationCode()
	if err != nil {
		return "", internalFailure(err)
	}
	ctx, cancel := context.WithTimeout(ctx, gatewayTestTimeout)
	defer cancel()
	switch channel {
	case GatewayChannelSMTP:
		sender := s.resolveEmailSender()
		if _, devOnly := sender.(devOnlySender); devOnly || sender == nil {
			return "", invalidArgument("当前没有可用的邮件通道：请先保存配置并启用")
		}
		if err := sender.SendLoginCode(ctx, trimmed, code, s.codeExpireMinutes); err != nil {
			return "", gatewayTestFailure("邮件通道发送失败", err)
		}
		return "测试邮件已发送至 " + trimmed + "，请查收", nil
	case GatewayChannelSMS:
		sender := s.resolveSMSSender()
		if _, devOnly := sender.(devOnlySender); devOnly || sender == nil {
			return "", invalidArgument("当前没有可用的短信通道：请先保存配置并启用")
		}
		if err := sender.SendLoginCode(ctx, trimmed, code, s.codeExpireMinutes); err != nil {
			return "", gatewayTestFailure("短信通道发送失败", err)
		}
		return "测试短信已发送至 " + trimmed + "，请查收", nil
	default:
		return "", invalidArgument("未知的网关通道")
	}
}

// gatewayTestFailure 把投递失败转成可以直接展示的原因。
//
// 测试发送的全部价值就是回答"为什么发不出去"：SMTP 拒连、授权码过期、短信签名未过审
// 都属于运营自己能修的问题，折叠成统一的"系统处理失败"会让人只能靠猜。
// 这里回显的是底层错误文本（不含平台密钥），并把它按上游投递失败（502）投影出去，
// 与"参数填错"（400）在监控里区分开。
func gatewayTestFailure(label string, cause error) *Error {
	message := strings.TrimSpace(cause.Error())
	if message == "" {
		return internalFailure(cause)
	}
	const maxRunes = 300
	if runes := []rune(message); len(runes) > maxRunes {
		message = string(runes[:maxRunes]) + "…"
	}
	return &Error{
		Status:  http.StatusBadGateway,
		Code:    kernel.CodeInternal,
		Reason:  kernel.ReasonInternal,
		Message: label + "：" + message,
		Cause:   cause,
	}
}

func (s *Service) existingSMTP() (*SMTPGatewayConfig, error) {
	record, err := s.store.GatewayConfig(GatewayChannelSMTP)
	if errors.Is(err, ErrNotFound) || record == nil {
		return nil, nil
	}
	if err != nil {
		return nil, internalFailure(err)
	}
	plaintext, err := s.gatewayCipher().Decrypt(record.ConfigJSON)
	if err != nil {
		return nil, internalFailure(err)
	}
	var config SMTPGatewayConfig
	if err := json.Unmarshal(plaintext, &config); err != nil {
		return nil, internalFailure(err)
	}
	return &config, nil
}

func (s *Service) existingSMS() (*SMSGatewayConfig, error) {
	record, err := s.store.GatewayConfig(GatewayChannelSMS)
	if errors.Is(err, ErrNotFound) || record == nil {
		return nil, nil
	}
	if err != nil {
		return nil, internalFailure(err)
	}
	plaintext, err := s.gatewayCipher().Decrypt(record.ConfigJSON)
	if err != nil {
		return nil, internalFailure(err)
	}
	var config SMSGatewayConfig
	if err := json.Unmarshal(plaintext, &config); err != nil {
		return nil, internalFailure(err)
	}
	return &config, nil
}

// gatewayCipher 返回服务持有的加密器；没有密钥时返回 nil，调用方按"读不出来"处理。
func (s *Service) gatewayCipher() *gatewayCipher {
	if s == nil || s.gateways == nil {
		return nil
	}
	return s.gateways.cipher
}

func (s *Service) gatewayCipherChecked() (*gatewayCipher, error) {
	cipher := s.gatewayCipher()
	if cipher == nil {
		return nil, errGatewayCipherUnavailable
	}
	return cipher, nil
}
