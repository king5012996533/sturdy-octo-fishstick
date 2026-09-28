package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// systemRelayProtocolCandidates 决定「请求里没有 model」时的协议探测顺序。
//
// 模型列表和异步任务轮询不带 model 字段，只能按渠道实际配置过的协议逐个套路径白名单；
// 顺序固定是为了让版本前缀（/v1、/v1beta）在同一请求上稳定可复现。
var systemRelayProtocolCandidates = []model.ChannelInterfaceType{
	model.ChannelInterfaceChatCompletion,
	model.ChannelInterfaceOpenAIResponse,
	model.ChannelInterfaceClaudeAPI,
	model.ChannelInterfaceOpenAIImage,
	model.ChannelInterfaceGrokImage,
	model.ChannelInterfaceVolcengineArkImage,
	model.ChannelInterfaceOpenAIAudio,
	model.ChannelInterfaceGeminiImage,
	model.ChannelInterfaceGeminiVeo,
	model.ChannelInterfaceMiniMaxVideo,
	model.ChannelInterfaceAgnesVideo,
	model.ChannelInterfaceNovitaVideo,
	model.ChannelInterfaceXAIVideo,
}

// RegisterSystemRelayRoutes 注册平台托管的系统渠道转发端点。
//
// 浏览器侧只持有 `/api/ai/system/<channelId>` 这一条相对地址：上游 URL、平台密钥、
// 渠道级请求头全部留在服务端注入。调用前必须已经挂上 RuntimeDependenciesMiddleware
// （频控与并发协调都从它取依赖），因此注册顺序必须晚于 desktop 路由注册。
func RegisterSystemRelayRoutes(r *gin.RouterGroup, svc *app.Service) {
	r.Any("/ai/system/:channelId/*path", func(c *gin.Context) {
		proxySystemRelayRequest(c, svc)
	})
}

func proxySystemRelayRequest(c *gin.Context, svc *app.Service) {
	user, err := currentUser(c, svc)
	if err != nil {
		failService(c, err)
		return
	}
	// 平台转发是「模型由平台托管」这一形态的执行面：前台模型目录打开时渠道来自
	// 用户自建（走 /api/ai/custom），不能让平台凭证被那条路径借走。
	frontendModels, err := svc.FeatureEnabled(app.FeatureFrontendModels)
	if err != nil {
		failService(c, err)
		return
	}
	if frontendModels {
		fail(c, http.StatusForbidden, errors.New("平台托管模型未启用"))
		return
	}
	channel, err := svc.SystemChannel(strings.TrimSpace(c.Param("channelId")))
	if err != nil {
		failService(c, err)
		return
	}
	policy, available := loadRuntimePolicy(c, svc)
	if !available {
		return
	}
	if !enforceRateLimit(c, "system-relay:"+user.ID, policy.Request.SystemRelayPerMinute, time.Minute) {
		return
	}
	requestPath, err := normalizedProxyPath(c.Param("path"))
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	query, err := systemRelayQuery(c.Request.URL.RawQuery)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	requestLimit := policy.Request.SystemRelayRequestMB << 20
	if c.Request.ContentLength > requestLimit {
		fail(c, http.StatusRequestEntityTooLarge, errors.New("系统渠道请求超过配置上限"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, requestLimit)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			fail(c, http.StatusRequestEntityTooLarge, errors.New("系统渠道请求超过配置上限"))
			return
		}
		fail(c, http.StatusBadRequest, errors.New("读取系统渠道请求失败"))
		return
	}
	if c.Request.Method == http.MethodGet && len(body) != 0 {
		fail(c, http.StatusBadRequest, errors.New("模型列表请求不允许携带请求体"))
		return
	}
	contentType := c.GetHeader("Content-Type")
	authorizationChannel, err := systemRelayAuthorizationChannel(svc, channel)
	if err != nil {
		failService(c, err)
		return
	}
	protocol, providerModelKey, err := authorizeSystemRelayRequest(svc, authorizationChannel, c.Request.Method, requestPath, contentType, body)
	if err != nil {
		fail(c, http.StatusForbidden, err)
		return
	}
	// 模型列表由平台目录回答：上游会回真实 SKU 名，转发出去等于公布平台后端是谁。
	if c.Request.Method == http.MethodGet && requestPath == "/models" {
		writeSystemRelayModelList(c, svc, channel.ID, protocol, c.GetHeader("x-goog-api-key") != "")
		return
	}
	// 计费卡点：这里是平台凭证唯一的出网点。接入计费时，quote（按模型与能力预估）
	// 必须在下面真正发请求之前完成并失败关闭，settle（按 token/媒体数量结算）
	// 必须在响应写完之后按同一笔引用回写；两者都不该下沉到浏览器或任务管线。
	requestPath, body = rewriteSystemRelayModel(requestPath, providerModelKey, contentType, body)

	target := app.ChannelAPIURLForProtocol(channel.BaseURL, requestPath, protocol)
	parsed, err := app.ValidateOutboundURL(target)
	if err != nil {
		fail(c, http.StatusBadGateway, errors.New("系统渠道上游地址不可用"))
		return
	}
	if query != "" {
		parsed.RawQuery = query
	}
	timeout := time.Duration(policy.Request.SystemRelayTimeoutMinutes) * time.Minute
	release, _, err := svc.AcquireChannelSlot(c.Request.Context(), channel.ID, "system-relay", timeout+time.Minute)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, errors.New("系统渠道并发协调服务不可用"))
		return
	}
	defer release()

	upstreamReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, parsed.String(), bytes.NewReader(body))
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("构造系统渠道请求失败"))
		return
	}
	if contentType != "" && len(body) > 0 {
		upstreamReq.Header.Set("Content-Type", contentType)
	}
	if strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/event-stream") {
		upstreamReq.Header.Set("Accept", "text/event-stream")
	} else {
		upstreamReq.Header.Set("Accept", "application/json")
	}
	headers, err := app.ParseOutboundHeadersJSON(channel.HeadersJSON)
	if err != nil {
		failService(c, err)
		return
	}
	app.ApplyOutboundHeaders(upstreamReq, headers)
	app.ApplyDefaultOutboundHeaders(upstreamReq)
	applySystemRelayCredential(upstreamReq, protocol, channel)

	resp, err := app.OutboundHTTPClient(timeout).Do(upstreamReq)
	if err != nil {
		fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("系统渠道上游连接失败"))))
		return
	}
	defer resp.Body.Close()
	_ = svc.RecordChannelResult(c.Request.Context(), channel.ID, resp.StatusCode >= http.StatusInternalServerError)
	relay := systemRelayTarget{
		channel:       channel,
		requestPath:   requestPath,
		responseLimit: policy.Request.SystemRelayResponseMB << 20,
		allowBinary:   customVideoContentPath.MatchString(requestPath) || strings.HasSuffix(requestPath, "/audio/speech"),
	}
	relay.writeResponse(c, svc, resp)
}

// systemRelayTarget 汇总一次转发需要的渠道信息与响应约束。
type systemRelayTarget struct {
	channel       *model.ModelChannel
	requestPath   string
	responseLimit int64
	allowBinary   bool
}

func (t systemRelayTarget) secrets() []string {
	return []string{t.channel.APIKey, t.channel.SecretKey}
}

func (t systemRelayTarget) writeResponse(c *gin.Context, svc *app.Service, resp *http.Response) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		t.writeError(c, svc, resp, mediaType)
		return
	}
	if mediaType == "text/event-stream" {
		// 事件流必须边到边发，不能先读完再整包返回，否则前端只能看到“转圈”。
		if _, err := streamSystemProxyResponse(c, resp, t.responseLimit, t.secrets()...); err != nil {
			// 响应头已经发出，这里无法再改状态码；超限或断开都只能留诊断。
			log.Printf("system relay stream aborted channel=%s path=%s: %v", t.channel.ID, t.requestPath, err)
		}
		return
	}
	if t.allowBinary && (strings.HasPrefix(mediaType, "video/") || strings.HasPrefix(mediaType, "audio/") || mediaType == "application/octet-stream") {
		body, err := readLimitedRelayBody(resp.Body, t.responseLimit)
		if err != nil {
			fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("系统渠道上游返回了过大的媒体文件"))))
			return
		}
		c.Data(resp.StatusCode, mediaType, redactRelaySecret(body, t.secrets()...))
		return
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("系统渠道上游返回了不支持的内容类型"))))
		return
	}
	body, err := readLimitedRelayBody(resp.Body, t.responseLimit)
	if err != nil || !json.Valid(body) {
		fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("系统渠道上游返回无效或过大的 JSON"))))
		return
	}
	// 成功响应不做文本改写（与自定义渠道中转一致）：模型输出里的文本替换只属于
	// 错误路径的运维口径，改写正常结果会破坏结构化数据。
	c.Data(resp.StatusCode, "application/json; charset=utf-8", redactRelaySecret(body, t.secrets()...))
}

func (t systemRelayTarget) writeError(c *gin.Context, svc *app.Service, resp *http.Response, mediaType string) {
	body, err := readLimitedRelayBody(resp.Body, maxCustomRelayErrorResponseBytes)
	if err != nil {
		fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("系统渠道上游请求失败"))))
		return
	}
	body = redactRelaySecret(body, t.secrets()...)
	rawMessage := strings.TrimSpace(string(body))
	if intercepted := interceptedRelayText(svc, rawMessage); intercepted != rawMessage {
		fail(c, resp.StatusCode, errors.New(intercepted))
		return
	}
	if (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")) && json.Valid(body) {
		c.Data(resp.StatusCode, "application/json; charset=utf-8", body)
		return
	}
	fail(c, resp.StatusCode, errors.New(svc.UserFacingProviderHTTPError(resp.StatusCode, resp.Status, string(body))))
}

// systemRelayAuthorizationChannel 用可执行模型（channel_models）补齐渠道的授权视图。
//
// ModelsJSON 只是旧渠道表上的目录缓存，模型增删后不会同步；只信缓存会把已配置模型
// 误判成未授权。这里只改副本，不动仓储返回的对象。
func systemRelayAuthorizationChannel(svc *app.Service, channel *model.ModelChannel) (*model.ModelChannel, error) {
	models, err := svc.SystemChannelEnabledModels(channel.ID)
	if err != nil {
		return nil, err
	}
	allowed := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	appendKey := func(value string) {
		key := strings.TrimSpace(value)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		allowed = append(allowed, key)
	}
	for _, item := range models {
		appendKey(item.ModelKey)
	}
	var cached []string
	_ = json.Unmarshal([]byte(channel.ModelsJSON), &cached)
	for _, key := range cached {
		appendKey(key)
	}
	encoded, err := json.Marshal(allowed)
	if err != nil {
		return nil, err
	}
	copied := *channel
	copied.ModelsJSON = string(encoded)
	return &copied, nil
}

// authorizeSystemRelayRequest 解析协议与执行模型键，并完成路径授权。
//
// 带 model 的请求以 channel_models 为准：协议、别名、真实 SKU 都只认服务端记录，
// 客户端给的模型名既不能绕过授权，也不能决定上游型号。
func authorizeSystemRelayRequest(svc *app.Service, authorizationChannel *model.ModelChannel, method string, requestPath string, contentType string, body []byte) (model.ChannelInterfaceType, string, error) {
	requestedModel := proxyRequestModelForPath(requestPath, contentType, body)
	if requestedModel != "" {
		channelModel, err := svc.SystemChannelModel(authorizationChannel.ID, requestedModel)
		if err != nil || !channelModel.Enabled {
			return "", "", errors.New("当前系统渠道未授权该模型")
		}
		if channelModel.Protocol == "" {
			return "", "", errors.New("当前模型尚未配置请求协议")
		}
		if err := authorizeSystemProxy(authorizationChannel, channelModel.Protocol, method, requestPath, contentType, body); err != nil {
			return "", "", err
		}
		return channelModel.Protocol, strings.TrimPrefix(strings.TrimSpace(channelModel.ProviderModelKey), "models/"), nil
	}
	for _, protocol := range systemRelayProtocolCandidates {
		configured, err := svc.SystemChannelHasProtocol(authorizationChannel.ID, protocol)
		if err != nil {
			return "", "", err
		}
		if !configured {
			continue
		}
		if err := authorizeSystemProxy(authorizationChannel, protocol, method, requestPath, contentType, body); err == nil {
			return protocol, "", nil
		}
	}
	return "", "", errors.New("系统渠道不允许访问该上游接口")
}

// rewriteSystemRelayModel 把客户端使用的模型别名换成渠道真正的上游执行 SKU。
// 别名是平台对外的稳定标识；不重写就等于要么调用失败，要么把内部命名暴露给上游日志。
func rewriteSystemRelayModel(requestPath string, providerModelKey string, contentType string, body []byte) (string, []byte) {
	if providerModelKey == "" {
		return requestPath, body
	}
	if matches := geminiGeneratePath.FindStringSubmatch(requestPath); len(matches) == 3 {
		return "/models/" + url.PathEscape(providerModelKey) + ":" + matches[2], body
	}
	if mediaType, _, _ := mime.ParseMediaType(contentType); strings.HasPrefix(mediaType, "multipart/") {
		// multipart 请求体（图片编辑、图生视频）按原样透传：重建分片会破坏二进制边界，
		// 这类模型的别名只能靠上游自身兼容别名。
		return requestPath, body
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return requestPath, body
	}
	if _, exists := payload["model"]; !exists {
		return requestPath, body
	}
	payload["model"] = providerModelKey
	encoded, err := json.Marshal(payload)
	if err != nil {
		return requestPath, body
	}
	return requestPath, encoded
}

func applySystemRelayCredential(req *http.Request, protocol model.ChannelInterfaceType, channel *model.ModelChannel) {
	switch protocol {
	case model.ChannelInterfaceGeminiVeo, model.ChannelInterfaceGeminiImage:
		req.Header.Set("x-goog-api-key", channel.APIKey)
	case model.ChannelInterfaceClaudeAPI:
		req.Header.Set("x-api-key", channel.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+channel.APIKey)
	}
}

// systemRelayQuery 转发查询串（Gemini 的 alt=sse 依赖它），但不允许携带密钥：
// 否则浏览器可以把平台转发端点当成密钥透传通道。
func systemRelayQuery(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", errors.New("系统渠道查询参数无效")
	}
	for key := range values {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "key", "api_key", "access_token", "token":
			return "", errors.New("系统渠道地址不允许在查询参数中携带密钥")
		}
	}
	return values.Encode(), nil
}

// writeSystemRelayModelList 用平台目录回答模型列表请求。
//
// 上游返回的是真实 SKU，原样转发等于公布平台后端是谁，也让别名失去意义；
// 这里只回该渠道已授权且与请求协议同族的模型键。
func writeSystemRelayModelList(c *gin.Context, svc *app.Service, channelID string, protocol model.ChannelInterfaceType, geminiClient bool) {
	models, err := svc.SystemChannelEnabledModels(channelID)
	if err != nil {
		failService(c, err)
		return
	}
	gemini := geminiClient || isGeminiRelayProtocol(protocol)
	keys := make([]string, 0, len(models))
	for _, item := range models {
		if !item.Enabled || isGeminiRelayProtocol(item.Protocol) != gemini {
			continue
		}
		if key := strings.TrimSpace(item.ModelKey); key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if gemini {
		entries := make([]gin.H, 0, len(keys))
		for _, key := range keys {
			entries = append(entries, gin.H{"name": "models/" + key, "displayName": key, "supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"}})
		}
		c.JSON(http.StatusOK, gin.H{"models": entries})
		return
	}
	entries := make([]gin.H, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, gin.H{"id": key, "object": "model", "created": 0, "owned_by": "platform"})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": entries})
}

func isGeminiRelayProtocol(protocol model.ChannelInterfaceType) bool {
	return protocol == model.ChannelInterfaceGeminiVeo || protocol == model.ChannelInterfaceGeminiImage
}
