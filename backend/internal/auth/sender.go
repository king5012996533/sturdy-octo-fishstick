package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strconv"
	"strings"
)

// EmailSender 负责投递邮箱验证码。
//
// 策略层只依赖这个接口，因此换服务商不需要改业务代码。验证码只经由此通道
// 下发，任何实现都不得把它写进 HTTP 响应。
type EmailSender interface {
	SendLoginCode(ctx context.Context, to string, code string, expireMinutes int) error
}

// ConsoleSender 只把验证码写进服务端日志，用于本地开发和联调。
//
// 它不适用于生产：日志可见者等于可以登录任意账号。
type ConsoleSender struct{}

func (ConsoleSender) SendLoginCode(_ context.Context, to string, code string, expireMinutes int) error {
	log.Printf("auth: 邮箱验证码（本地投递，未真实发送）to=%s code=%s 有效期=%d分钟", to, code, expireMinutes)
	return nil
}

// SMTPSender 通过 SMTP 投递验证码。
type SMTPSender struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// FromName 是发件人展示名，留空时只发地址。
	FromName string
}

// SMTPSenderFromEnv 从环境变量构造 SMTP 投递器。
//
// 密钥只从环境读取，不提供硬编码默认值：CanvasMind 的 provider/storage 加密
// 密钥有硬编码兜底（见其安全审计第 7 条），这里不重复那个模式。
func SMTPSenderFromEnv() (*SMTPSender, error) {
	host := strings.TrimSpace(os.Getenv("BEEFTV_SMTP_HOST"))
	if host == "" {
		return nil, errors.New("缺少 BEEFTV_SMTP_HOST")
	}
	port := 587
	if raw := strings.TrimSpace(os.Getenv("BEEFTV_SMTP_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("BEEFTV_SMTP_PORT 不是有效端口: %w", err)
		}
		port = parsed
	}
	username := strings.TrimSpace(os.Getenv("BEEFTV_SMTP_USERNAME"))
	password := os.Getenv("BEEFTV_SMTP_PASSWORD")
	from := strings.TrimSpace(os.Getenv("BEEFTV_SMTP_FROM"))
	if from == "" {
		from = username
	}
	if from == "" {
		return nil, errors.New("缺少 BEEFTV_SMTP_FROM")
	}
	return &SMTPSender{
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
		From:     from,
		FromName: strings.TrimSpace(os.Getenv("BEEFTV_SMTP_FROM_NAME")),
	}, nil
}

func (s *SMTPSender) SendLoginCode(_ context.Context, to string, code string, expireMinutes int) error {
	if s == nil {
		return errors.New("SMTP 投递器未初始化")
	}
	from := s.From
	if s.FromName != "" {
		from = fmt.Sprintf("%s <%s>", s.FromName, s.From)
	}
	message := strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Subject: 登录验证码",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		fmt.Sprintf("你的登录验证码是 %s，%d 分钟内有效。", code, expireMinutes),
		"如果这不是你本人的操作，请忽略本邮件。",
	}, "\r\n")

	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}
	address := fmt.Sprintf("%s:%d", s.Host, s.Port)
	return smtp.SendMail(address, auth, s.From, []string{to}, []byte(message))
}
