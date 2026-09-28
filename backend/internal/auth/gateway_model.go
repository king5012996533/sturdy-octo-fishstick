package auth

import "time"

// 投递网关配置（SMTP / 阿里云短信）。
//
// 这一层回答的是"验证码从哪儿发出去"。配置存在账号库里、由后台维护，而不是只靠
// 环境变量：签名、模板、发件人都属于运营日常要动的东西，改一次重启一次服务不现实。
// 环境变量仍然保留为兜底，方便本机联调与首次部署。
const (
	GatewayChannelSMTP = "SMTP"
	GatewayChannelSMS  = "SMS"
)

// GatewayConfig 映射 auth_gateway_configs。
//
// ConfigJSON 是密文：里面装着 SMTP 密码和阿里云 AccessKeySecret。密钥来自
// StateSecret，因此换密钥等于让已存的配置读不出来（需要重新填一次）。
type GatewayConfig struct {
	Channel    string    `gorm:"column:channel;primaryKey;size:16"`
	ConfigJSON []byte    `gorm:"column:config_json"`
	Enabled    bool      `gorm:"column:enabled"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
	UpdatedBy  string    `gorm:"column:updated_by;size:36"`
}

func (GatewayConfig) TableName() string { return "auth_gateway_configs" }

// SMTPGatewayConfig 是邮件通道的一组配置。
//
// Password 只写不读：后台读取接口不回传密钥，管理员只能覆盖，不能"看回来"。
type SMTPGatewayConfig struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"hasPassword,omitempty"`
	From        string `json:"from"`
	FromName    string `json:"fromName"`
}

// SMSGatewayConfig 是短信通道的一组配置（当前实现为阿里云短信）。
type SMSGatewayConfig struct {
	AccessKeyID        string `json:"accessKeyId"`
	AccessKeySecret    string `json:"accessKeySecret,omitempty"`
	HasAccessKeySecret bool   `json:"hasAccessKeySecret,omitempty"`
	SignName           string `json:"signName"`
	TemplateCode       string `json:"templateCode"`
	TemplateParamKey   string `json:"templateParamKey"`
	RegionID           string `json:"regionId"`
	Endpoint           string `json:"endpoint"`
}

// GatewayChannelView 是后台的一个通道视图。
//
// Source 说明这份配置从哪儿来：database（后台配置）/ environment（环境变量）/
// console（没有真实通道，验证码只写日志）。前台不需要知道这些，但运营必须知道，
// 否则会出现"后台看起来配好了、实际根本没发出去"。
type GatewayChannelView struct {
	Channel   string             `json:"channel"`
	Enabled   bool               `json:"enabled"`
	Source    string             `json:"source"`
	Ready     bool               `json:"ready"`
	Detail    string             `json:"detail"`
	UpdatedAt *time.Time         `json:"updatedAt,omitempty"`
	UpdatedBy string             `json:"updatedBy,omitempty"`
	SMTP      *SMTPGatewayConfig `json:"smtp,omitempty"`
	SMS       *SMSGatewayConfig  `json:"sms,omitempty"`
}

// GatewaySettingsView 是两个通道的合并视图。
type GatewaySettingsView struct {
	SMTP GatewayChannelView `json:"smtp"`
	SMS  GatewayChannelView `json:"sms"`
}

// GatewayUpdateInput 是一次保存请求。
//
// 密钥留空表示"保持原值"：后台回显不了密钥，如果空值被当成清空，管理员每次改
// 发件人都得重新粘贴一遍密钥，实际结果一定是密钥被弄丢。
type GatewayUpdateInput struct {
	Enabled bool               `json:"enabled"`
	SMTP    *SMTPGatewayConfig `json:"smtp,omitempty"`
	SMS     *SMSGatewayConfig  `json:"sms,omitempty"`
}
