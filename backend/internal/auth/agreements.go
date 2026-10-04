package auth

// 协议版本与正文。
//
// 版本号是留痕的一部分：用户接受的就是这个字符串，改了版本即意味着需要重新同意。
// 正文放在服务端而不是前端产物里，是为了让协议更新不必等一次前端发布——条款必须能
// 独立于客户端版本更新，否则停留在旧页面的用户会一直看到旧条款。
//
// 下面是新装实例的兜底正文：主体、所在地、数据存储地与联系方式都已按备案信息填好，
// 后台一旦发布过版本就以后台那版为准（见 agreements_admin.go）。改这里只影响
// 还没发过版本的实例，线上要改条款仍然走后台发布。
const (
	// agreementVersion 是当前生效的版本，随正文变更一起递增。
	agreementVersion = "2026-10-04"

	// AgreementTypeTerms / AgreementTypePrivacy 与 Prisma 的协议类型取值保持一致。
	AgreementTypeTerms   = "TERMS"
	AgreementTypePrivacy = "PRIVACY"
)

// AgreementDocument 是一份可展示的协议。
type AgreementDocument struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// AgreementsPayload 是协议接口的响应。
type AgreementsPayload struct {
	Version   string              `json:"version"`
	Documents []AgreementDocument `json:"documents"`
}

// CurrentAgreementVersion 返回当前生效的协议版本。
func CurrentAgreementVersion() string { return agreementVersion }

// Agreements 返回注册页需要展示与留痕的协议。
func Agreements() AgreementsPayload {
	return AgreementsPayload{
		Version: agreementVersion,
		Documents: []AgreementDocument{
			{Type: AgreementTypeTerms, Title: "用户协议", Body: termsBody},
			{Type: AgreementTypePrivacy, Title: "隐私政策", Body: privacyBody},
		},
	}
}

const termsBody = `欢迎使用 KinoTV（以下称“本服务”）。本协议是你与深圳市光启云科电子商务有限公司（以下称“我们”）之间就使用本服务所订立的协议。请完整阅读并同意本协议后再注册。

一、账号
1. 你需要使用真实、有效的邮箱完成注册，并对账号下发生的一切行为负责。
2. 账号仅限本人使用，不得出租、出借、转让或与第三方共享。
3. 你应妥善保管登录凭据；发现账号被盗用，应立即通过文末联系方式告知我们。

二、服务内容
1. 本服务提供 AI 创作、画布编排、模型调用与素材管理能力。
2. 具体功能、可用模型与额度以页面实际展示为准，我们可能根据运营需要调整。

三、付费与额度
1. 需要付费的功能以下单页展示的价格与额度为准；充值后不支持无理由退款，法律另有规定的除外。
2. 因你自身原因（含违规使用）导致的账号受限，未消费部分按法律规定处理。

四、你的内容与责任
1. 你对自己上传、生成、发布的内容负责，并保证拥有相应权利。
2. 不得利用本服务制作或传播违反法律法规、侵害他人权益、危害未成年人的内容。

五、服务变更与终止
1. 因升级、维护或不可抗力，我们可能暂停服务，并尽可能提前通知。
2. 你若严重违反本协议，我们有权限制或终止你对本服务的使用。

六、其他
1. 本协议的订立、效力与争议解决适用中华人民共和国法律。
2. 争议协商不成的，提交深圳市龙华区有管辖权的人民法院解决。
3. 协议更新后我们会在注册页与站内提示；继续使用即视为接受更新后的条款。

联系方式：站内「帮助与反馈」提交工单，或发送邮件至 samwork2026@126.com。`

const privacyBody = `本政策说明我们在你使用 KinoTV 期间如何收集、使用、存储和保护你的个人信息。

一、我们收集的信息
1. 账号信息：你注册时提供的邮箱，以及登录时间、登录 IP 与浏览器 User-Agent。
2. 使用信息：你创建的项目、上传的素材、调用的模型与产生的任务记录。
3. 支付信息：充值与订单记录；银行卡等支付要素由支付机构直接处理，我们不存储。

二、我们如何使用这些信息
1. 提供并维护服务，完成账号鉴权与订单结算。
2. 保障安全：识别异常登录、防止刷量与滥用。
3. 履行法律法规规定的义务。

三、我们如何共享
1. 除以下情形外，我们不会向第三方提供你的个人信息：取得你的单独同意；为完成你请求的服务而必须共享（例如你自行配置的模型服务商、支付机构）；法律法规要求。
2. 我们不会出售你的个人信息。

四、存储与保护
1. 你的信息存储于中国大陆境内的服务器。
2. 我们采取访问控制、传输加密等措施保护你的信息；会话凭据在服务端仅以摘要形式保存。

五、你的权利
你可以通过文末联系方式要求查询、更正、删除你的个人信息或注销账号。注销后我们将删除或匿名化你的相关信息，法律法规要求保留的除外。

六、未成年人
本服务不面向未满 14 周岁的未成年人。如你未满 18 周岁，请在监护人陪同下阅读并决定是否使用。

七、联系我们
联系方式：站内「帮助与反馈」提交工单，或发送邮件至 samwork2026@126.com。

本政策更新后我们会在站内提示；继续使用即视为接受更新后的版本。`
