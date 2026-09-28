package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 支付渠道的适配层：发起支付、渠道配置读写、回调验签。
//
// 支付渠道一律被抽象成 paymentProvider，订单服务只关心"生成收银台"和"验签回调"
// 两件事。接具体聚合网关时只改 aggregatePaymentProvider 一个实现：字段名、签名
// 拼接、响应里收银台地址的位置有任何差异，都在这里吸收，订单状态机与后台配置读写
// 一行都不用动。

const (
	// paymentConfigAppSecret 是所有渠道统一的密钥字段名。
	//
	// 固定成同一个名字，是为了让"密钥只写不读"的逻辑能在所有渠道上共用一份：
	// 后台读不到密钥，若把空值当清空，改一次回调地址就会把商户密钥弄丢。
	paymentConfigAppSecret = "appSecret"
	// paymentConfigNotifyURL 是渠道回调地址的键名。
	paymentConfigNotifyURL = "notifyUrl"
	// paymentConfigCreateURL 是聚合网关下单接口的键名。
	paymentConfigCreateURL = "createUrl"

	paymentChannelManualDetail = "手工收款兜底：不接支付渠道时订单等待运营确认到账后手工补单"
)

// channelConfig 是解密后的渠道配置。
//
// 用 map[string]string 而不是结构体：不同聚合网关的字段集合并不一致，结构体会逼着
// 代码为每个网关加一段解析，也会让"未出现的字段保持原值"这条规则实现起来更绕。
type channelConfig map[string]string

// paymentCallback 是验签通过后的回调要点。
type paymentCallback struct {
	OrderNo   string
	TradeNo   string
	AmountFen int64
	Success   bool
}

// paymentLaunch 是渠道"发起支付"的产物：收银台地址或自定义跳转参数。
type paymentLaunch struct {
	payURL string
	params map[string]string
}

// paymentProvider 是支付渠道抽象。
//
// 只有两个动作：launch 生成收银台，verifyCallback 验签并解析回调。任何"渠道特有的
// 花样"都应该收敛在实现里，而不是泄漏成订单服务的 if-else。
type paymentProvider interface {
	launch(now time.Time, order BillingOrder, config channelConfig) (*paymentLaunch, error)
	verifyCallback(raw []byte, header http.Header, config channelConfig) (*paymentCallback, error)
}

// manualPaymentProvider 是不接任何外部支付时的兜底渠道。
//
// 它不发起任何外部调用：订单进入"等待运营手工确认"，运营在后台确认到账后走
// MarkBillingOrderPaid 补单。
type manualPaymentProvider struct{}

func (manualPaymentProvider) launch(_ time.Time, _ BillingOrder, _ channelConfig) (*paymentLaunch, error) {
	return &paymentLaunch{params: map[string]string{"hint": "等待运营手工确认"}}, nil
}

// manualPaymentProvider 没有外部网关可验签。
//
// 它面向的是已经过鉴权的运营侧确认入口，而不是公网网关回调，因此这里只解析字段、
// 不校验签名。接入任何真实渠道都必须改走带验签的 provider，绝不能复用这一段。
func (manualPaymentProvider) verifyCallback(raw []byte, _ http.Header, _ channelConfig) (*paymentCallback, error) {
	fields, err := paymentCallbackFields(raw)
	if err != nil {
		return nil, err
	}
	return paymentCallbackOf(fields)
}

// aggregatePaymentProvider 是通用聚合网关适配：POST JSON 下单、HMAC-SHA256 验签。
//
// 契约（与后端自建网关约定）：
//   - 下单：POST JSON 到 createUrl，字段 appId/mchId/orderNo/amountFen/subject/
//     notifyUrl/timestamp/nonce/sign
//   - sign：把所有字段（除 sign）按 key 升序拼成 k=v&k=v...&key=appSecret，
//     再以 appSecret 为密钥做 HMAC-SHA256，取小写十六进制
//   - 回调：同样的拼接与验签规则，接受 orderNo/tradeNo/status(SUCCESS|PAID)/
//     amountFen
//
// 接具体聚合网关时只改这一个 provider。
type aggregatePaymentProvider struct {
	client *http.Client
}

func (p aggregatePaymentProvider) launch(now time.Time, order BillingOrder, config channelConfig) (*paymentLaunch, error) {
	createURL := configValue(config, paymentConfigCreateURL)
	if createURL == "" {
		return nil, invalidArgument("支付渠道未配置网关下单地址")
	}
	secret := configValue(config, paymentConfigAppSecret)
	if secret == "" {
		return nil, invalidArgument("支付渠道未配置 appSecret")
	}
	nonce, err := randomBillingNonce()
	if err != nil {
		return nil, internalFailure(err)
	}
	fields := map[string]string{
		"appId":     configValue(config, "appId"),
		"mchId":     configValue(config, "mchId"),
		"orderNo":   order.OrderNo,
		"amountFen": strconv.FormatInt(order.PayableFen, 10),
		"subject":   billingOrderSubject(order),
		"notifyUrl": configValue(config, paymentConfigNotifyURL),
		"timestamp": strconv.FormatInt(now.Unix(), 10),
		"nonce":     nonce,
	}
	fields["sign"] = aggregatePaymentSignature(fields, secret)
	payload, err := json.Marshal(fields)
	if err != nil {
		return nil, internalFailure(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(payload))
	if err != nil {
		return nil, invalidArgument("支付渠道下单地址不合法")
	}
	request.Header.Set("Content-Type", "application/json")
	client := p.client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, internalFailure(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return nil, internalFailure(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, internalFailure(fmt.Errorf("支付网关返回状态码 %d", response.StatusCode))
	}
	payURL := extractBillingPayURL(body)
	if payURL == "" {
		return nil, internalFailure(errors.New("支付网关未返回收银台地址"))
	}
	return &paymentLaunch{payURL: payURL}, nil
}

func (p aggregatePaymentProvider) verifyCallback(raw []byte, _ http.Header, config channelConfig) (*paymentCallback, error) {
	secret := configValue(config, paymentConfigAppSecret)
	if secret == "" {
		return nil, invalidArgument("支付渠道未配置 appSecret")
	}
	fields, err := paymentCallbackFields(raw)
	if err != nil {
		return nil, err
	}
	provided := strings.ToLower(strings.TrimSpace(fields["sign"]))
	delete(fields, "sign")
	if provided == "" {
		return nil, invalidArgument("支付回调缺少签名")
	}
	expected := aggregatePaymentSignature(fields, secret)
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return nil, invalidArgument("支付回调验签失败")
	}
	return paymentCallbackOf(fields)
}

// aggregatePaymentSignature 计算聚合网关签名。
//
// 规则：所有字段（除 sign）按 key 升序拼成 k=v&...，末尾追 &key=appSecret，
// 以 appSecret 为密钥做 HMAC-SHA256 取小写十六进制。下单与回调共用同一函数，
// 任何一处拼接规则漂移都会让验签在两端一起失效，反而更容易被发现。
func aggregatePaymentSignature(fields map[string]string, secret string) string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if key == "sign" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(fields[key])
		builder.WriteByte('&')
	}
	builder.WriteString("key=")
	builder.WriteString(secret)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(builder.String()))
	return hex.EncodeToString(mac.Sum(nil))
}

// ---------- 发起支付 ----------

// StartBillingPayment 为待支付订单生成收银台。
func (s *Service) StartBillingPayment(userID string, orderID string) (*BillingPaymentLaunch, error) {
	order, err := s.loadOwnedOrder(userID, orderID)
	if err != nil {
		return nil, err
	}
	if order.Status != OrderStatusPending {
		return nil, conflict("订单当前状态不可发起支付")
	}
	now := s.now()
	if !now.Before(order.ExpiresAt) {
		return nil, invalidArgument("订单已超时，请重新下单")
	}
	config, _, err := s.billingChannelConfig(order.Provider)
	if err != nil {
		return nil, err
	}
	result, err := s.paymentProviderFor(order.Provider).launch(now, *order, config)
	if err != nil {
		return nil, err
	}
	launch := &BillingPaymentLaunch{
		Order:    *s.billingOrderView(*order),
		Provider: order.Provider,
	}
	if result != nil {
		launch.PayURL = result.payURL
		if len(result.params) > 0 {
			launch.Params = result.params
		}
	}
	return launch, nil
}

// ---------- 回调 ----------

// HandleBillingCallback 处理渠道异步回调：取配置 → 验签 → 定位订单 → 置为已支付。
//
// 全链路幂等：渠道会按自己的策略重复回调，已进入终态（已支付/已取消/已退款）的订单
// 直接返回 nil，不重复激活订阅。回调验签失败或订单不存在都返回错误，但绝不 panic。
func (s *Service) HandleBillingCallback(channel string, raw []byte, header http.Header) error {
	normalized := strings.ToUpper(strings.TrimSpace(channel))
	if !isKnownPaymentChannel(normalized) {
		return invalidArgument("未知的支付渠道")
	}
	// 手工渠道没有外部网关，也就没有可验签的报文。它的"确认到账"入口只能是后台补单
	// （/api/admin/orders/:id/mark-paid，带管理员守卫）。放行到这条匿名路由上，等于在
	// 公网挂了一个不带签名的"把订单改成已支付"按钮：任何人拿到订单号即可零元开通。
	if normalized == PaymentChannelManual {
		return invalidArgument("手工渠道不支持回调，请在后台补单")
	}
	config, _, err := s.billingChannelConfig(normalized)
	if err != nil {
		return err
	}
	callback, err := s.paymentProviderFor(normalized).verifyCallback(raw, header, config)
	if err != nil {
		return err
	}
	order, err := s.store.BillingOrderByNo(callback.OrderNo)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// 回调可能先于本地落库到达（极端竞态），返回错误让渠道稍后重试。
			return invalidArgument("订单不存在")
		}
		return internalFailure(err)
	}
	if order.Status != OrderStatusPending {
		return nil
	}
	// Provider 是下单时选定的渠道快照：拿 A 渠道签名的回调去核销 B 渠道的订单，
	// 说明两条链路的密钥边界被绕过了，必须拒绝。（空值只可能来自历史数据，放行。）
	if provider := strings.ToUpper(strings.TrimSpace(order.Provider)); provider != "" && provider != normalized {
		return invalidArgument("支付回调渠道与订单不一致")
	}
	if !callback.Success {
		// 失败/处理中的通知不改状态：订单继续保持 PENDING，等待下一次回调或超时关闭。
		return nil
	}
	// 金额必须回传且与应付一致。不能写成"金额大于 0 才比对"：回调报文里少一个字段
	// （字段名不匹配、网关省略零元位）就会让比对整体短路，1 分钱的订单也能被核销。
	if callback.AmountFen != order.PayableFen {
		return invalidArgument("支付回调金额与订单不一致")
	}
	if callback.TradeNo != "" {
		order.ProviderOrderNo = callback.TradeNo
	}
	_, err = s.markBillingOrderPaid(order, "支付回调")
	return err
}

// ---------- 渠道配置读写 ----------

// billingPaymentChannelOrder 是后台展示顺序：兜底渠道排最前，便于运营一眼看到当前状态。
var billingPaymentChannelOrder = []string{
	PaymentChannelManual,
	PaymentChannelAggregate,
	PaymentChannelWechat,
	PaymentChannelAlipay,
}

// AdminPaymentChannels 返回四个渠道的脱敏视图。
//
// 只回传非密钥字段；密钥一概不回传，只给 hasSecret。未配置的渠道给 source=console、
// ready=false 与可读的原因，让运营知道"为什么现在收不了款"。
func (s *Service) AdminPaymentChannels() (*PaymentChannelsView, error) {
	views := make([]PaymentChannelView, 0, len(billingPaymentChannelOrder))
	for _, channel := range billingPaymentChannelOrder {
		view, err := s.paymentChannelView(channel)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return &PaymentChannelsView{Channels: views}, nil
}

func (s *Service) paymentChannelView(channel string) (PaymentChannelView, error) {
	view := PaymentChannelView{
		Channel: channel,
		Source:  "console",
		Config:  map[string]string{},
	}
	record, err := s.store.PaymentConfig(channel)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return view, internalFailure(err)
	}
	if errors.Is(err, ErrNotFound) || record == nil {
		// 手工渠道没有配置也是可用的：它本来就是"不接支付渠道"的兜底。
		if channel == PaymentChannelManual {
			view.Enabled = true
			view.Ready = true
			view.Source = "builtin"
			view.Detail = paymentChannelManualDetail
			return view, nil
		}
		view.Detail = "未配置，暂不可用"
		return view, nil
	}

	view.Source = "database"
	view.Enabled = record.Enabled
	view.UpdatedBy = record.UpdatedBy
	updatedAt := record.UpdatedAt
	view.UpdatedAt = &updatedAt
	if channel == PaymentChannelManual {
		// 手工渠道是兜底，库里的停用状态不生效：它一旦消失，没接渠道时就收不了款。
		view.Enabled = true
	}
	plaintext, decryptErr := s.gatewayCipher().Decrypt(record.ConfigJSON)
	if decryptErr != nil {
		view.Detail = decryptErr.Error()
		return view, nil
	}
	config := channelConfig{}
	if len(bytes.TrimSpace(plaintext)) > 0 {
		if unmarshalErr := json.Unmarshal(plaintext, &config); unmarshalErr != nil {
			view.Detail = "渠道配置格式错误"
			return view, nil
		}
	}
	view.HasSecret = configValue(config, paymentConfigAppSecret) != ""
	sanitized := channelConfig{}
	for key, value := range config {
		if key == paymentConfigAppSecret {
			continue
		}
		sanitized[key] = value
	}
	if len(sanitized) > 0 {
		view.Config = sanitized
	}
	switch {
	case !view.Enabled:
		view.Detail = "后台已停用"
	case channel == PaymentChannelManual:
		view.Ready = true
		view.Detail = paymentChannelManualDetail
	case paymentChannelReady(channel, config):
		view.Ready = true
		view.Detail = "已配置，可用于收款"
	default:
		view.Detail = paymentChannelRequirement(channel)
	}
	return view, nil
}

// UpdatePaymentChannel 保存渠道配置。
//
// 两条硬规则：一是密钥留空即保持原值（后台读不到密钥，空值若按清空处理，改一次
// 回调地址就会把商户密钥弄丢）；二是手工渠道不允许停用（它是没接渠道时的兜底）。
func (s *Service) UpdatePaymentChannel(channel string, input PaymentChannelInput, actorID string) (*PaymentChannelsView, error) {
	normalized := strings.ToUpper(strings.TrimSpace(channel))
	if !isKnownPaymentChannel(normalized) {
		return nil, invalidArgument("未知的支付渠道")
	}
	if normalized == PaymentChannelManual && !input.Enabled {
		return nil, invalidArgument("手工渠道不能停用")
	}
	cipher, err := s.gatewayCipherChecked()
	if err != nil {
		return nil, internalFailure(err)
	}

	existing, _, err := s.billingChannelConfig(normalized)
	if err != nil {
		return nil, err
	}
	merged := channelConfig{}
	for key, value := range existing {
		if trimmedKey := strings.TrimSpace(key); trimmedKey != "" {
			merged[trimmedKey] = strings.TrimSpace(value)
		}
	}
	for key, value := range input.Config {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		merged[trimmedKey] = strings.TrimSpace(value)
	}
	// 覆盖后 appSecret 仍为空，说明这次请求没有携带新密钥：回退到原值（若原本也没有
	// 就删掉空键，避免落一个空密钥进库）。
	if configValue(merged, paymentConfigAppSecret) == "" {
		if secret := configValue(existing, paymentConfigAppSecret); secret != "" {
			merged[paymentConfigAppSecret] = secret
		} else {
			delete(merged, paymentConfigAppSecret)
		}
	}
	if input.Enabled {
		if err := validatePaymentChannelEnable(normalized, merged); err != nil {
			return nil, err
		}
	}

	plaintext, err := json.Marshal(merged)
	if err != nil {
		return nil, internalFailure(err)
	}
	encrypted, err := cipher.Encrypt(plaintext)
	if err != nil {
		return nil, internalFailure(err)
	}
	if err := s.store.SavePaymentConfig(&BillingPaymentConfig{
		Channel:    normalized,
		ConfigJSON: encrypted,
		Enabled:    input.Enabled,
		UpdatedAt:  s.now(),
		UpdatedBy:  strings.TrimSpace(actorID),
	}); err != nil {
		return nil, internalFailure(err)
	}
	return s.AdminPaymentChannels()
}

// billingChannelConfig 读取并解密一个渠道的配置；未配置时返回 (nil, false, nil)。
func (s *Service) billingChannelConfig(channel string) (channelConfig, bool, error) {
	record, err := s.store.PaymentConfig(channel)
	if errors.Is(err, ErrNotFound) || record == nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, internalFailure(err)
	}
	plaintext, err := s.gatewayCipher().Decrypt(record.ConfigJSON)
	if err != nil {
		return nil, record.Enabled, internalFailure(err)
	}
	config := channelConfig{}
	if len(bytes.TrimSpace(plaintext)) > 0 {
		if err := json.Unmarshal(plaintext, &config); err != nil {
			return nil, record.Enabled, internalFailure(err)
		}
	}
	return config, record.Enabled, nil
}

// paymentProviderFor 按渠道选择实现。
//
// 微信/支付宝也走聚合 provider：用户的既有系统由聚合网关统一对接这两家，这里的
// 差异（下单字段、验签口径）都由网关侧抹平，服务端只认一套契约即可。
func (s *Service) paymentProviderFor(channel string) paymentProvider {
	switch strings.ToUpper(strings.TrimSpace(channel)) {
	case PaymentChannelAggregate, PaymentChannelWechat, PaymentChannelAlipay:
		return aggregatePaymentProvider{client: s.httpClient()}
	default:
		return manualPaymentProvider{}
	}
}

func isKnownPaymentChannel(channel string) bool {
	switch channel {
	case PaymentChannelManual, PaymentChannelAggregate, PaymentChannelWechat, PaymentChannelAlipay:
		return true
	default:
		return false
	}
}

// paymentChannelReady 判断一个已配置渠道是否具备收款的最小要素。
func paymentChannelReady(channel string, config channelConfig) bool {
	switch channel {
	case PaymentChannelAggregate:
		return configValue(config, "appId") != "" && configValue(config, paymentConfigCreateURL) != ""
	case PaymentChannelWechat, PaymentChannelAlipay:
		return configValue(config, "mchId") != ""
	default:
		return false
	}
}

// validatePaymentChannelEnable 在启用渠道前校验必填项。
func validatePaymentChannelEnable(channel string, config channelConfig) error {
	switch channel {
	case PaymentChannelManual:
		return nil
	case PaymentChannelAggregate:
		if configValue(config, "appId") == "" || configValue(config, paymentConfigCreateURL) == "" {
			return invalidArgument("启用聚合渠道前必须填写 appId 与网关下单地址")
		}
	case PaymentChannelWechat, PaymentChannelAlipay:
		if configValue(config, "mchId") == "" {
			return invalidArgument("启用该渠道前必须填写商户号")
		}
	}
	return nil
}

// paymentChannelRequirement 给出"还差什么才能启用"的提示。
func paymentChannelRequirement(channel string) string {
	switch channel {
	case PaymentChannelAggregate:
		return "缺少 appId 或网关下单地址 createUrl"
	case PaymentChannelWechat, PaymentChannelAlipay:
		return "缺少商户号 mchId"
	default:
		return "配置不完整"
	}
}

// ---------- 通用工具 ----------

// configValue 读取并去空白，避免"填了空格看起来是空"这类配置事故。
func configValue(config channelConfig, key string) string {
	if config == nil {
		return ""
	}
	return strings.TrimSpace(config[key])
}

func billingOrderSubject(order BillingOrder) string {
	if name := strings.TrimSpace(order.PlanName); name != "" {
		return name
	}
	if code := strings.TrimSpace(order.PlanCode); code != "" {
		return code
	}
	return order.OrderNo
}

func randomBillingNonce() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// paymentCallbackFields 把回调内容解析成字段表。
//
// 优先按 JSON 解析（与下单请求同构），失败再按表单解析：不同聚合网关的回调编码
// 不一致，多认一种格式能少一次"回调收不到"的线上事故，而验签仍然照常进行。
func paymentCallbackFields(raw []byte) (map[string]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, invalidArgument("支付回调内容为空")
	}
	if trimmed[0] == '{' {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.UseNumber()
		var payload map[string]any
		if err := decoder.Decode(&payload); err != nil {
			return nil, invalidArgument("支付回调格式错误")
		}
		fields := make(map[string]string, len(payload))
		for key, value := range payload {
			fields[key] = paymentScalarString(value)
		}
		return fields, nil
	}
	values, err := url.ParseQuery(string(trimmed))
	if err != nil {
		return nil, invalidArgument("支付回调格式错误")
	}
	fields := make(map[string]string, len(values))
	for key, list := range values {
		if len(list) > 0 {
			fields[key] = list[0]
		}
	}
	return fields, nil
}

func paymentScalarString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func paymentCallbackOf(fields map[string]string) (*paymentCallback, error) {
	orderNo := strings.TrimSpace(fields["orderNo"])
	if orderNo == "" {
		return nil, invalidArgument("支付回调缺少订单号")
	}
	callback := &paymentCallback{
		OrderNo: orderNo,
		TradeNo: strings.TrimSpace(fields["tradeNo"]),
	}
	if raw := strings.TrimSpace(fields["amountFen"]); raw != "" {
		amount, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, invalidArgument("支付回调金额格式错误")
		}
		callback.AmountFen = amount
	}
	switch strings.ToUpper(strings.TrimSpace(fields["status"])) {
	case "SUCCESS", "PAID", "TRADE_SUCCESS":
		callback.Success = true
	}
	return callback, nil
}

// billingPayURLCandidates 是聚合网关可能回传收银台地址的字段名。
var billingPayURLCandidates = []string{
	"payUrl", "pay_url", "cashierUrl", "cashier_url", "codeUrl", "code_url", "url", "mwebUrl", "h5Url",
}

// extractBillingPayURL 从网关响应里挖出收银台地址，兼容一层 data 包装。
func extractBillingPayURL(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return findBillingPayURL(payload)
}

func findBillingPayURL(payload map[string]any) string {
	for _, key := range billingPayURLCandidates {
		if value, ok := payload[key]; ok {
			if text := strings.TrimSpace(paymentScalarString(value)); text != "" {
				return text
			}
		}
	}
	if data, ok := payload["data"].(map[string]any); ok {
		return findBillingPayURL(data)
	}
	return ""
}
