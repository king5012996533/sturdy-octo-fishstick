package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

// Error 是本模块统一的可投影错误。
//
// Status 决定 HTTP 状态，Reason 是前端应判断的机器可读原因；Message 才是展示文案。
// 直接返回裸 error 会让「密码打错」和「服务端故障」在监控里混成同一个 500。
type Error struct {
	Status  int
	Code    int
	Reason  kernel.ErrorReason
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func invalidArgument(message string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: kernel.CodeInvalidArgument, Reason: kernel.ReasonInvalidArgument, Message: message}
}

func unauthorized(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: kernel.CodeUnauthorized, Reason: kernel.ReasonUnauthorized, Message: message}
}

func forbidden(message string) *Error {
	return &Error{Status: http.StatusForbidden, Code: kernel.CodeForbidden, Reason: kernel.ReasonForbidden, Message: message}
}

func rateLimited(message string) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: kernel.CodeRateLimited, Reason: kernel.ReasonRateLimited, Message: message}
}

// notFound 表示「按标识查不到账号」。
//
// 这是可对外的信息：注册入口需要据此把用户引导到注册，而不是让他在登录页反复重试。
func notFound(message string) *Error {
	return &Error{Status: http.StatusNotFound, Code: kernel.CodeNotFound, Reason: kernel.ReasonNotFound, Message: message}
}

// conflict 表示与既有状态冲突（例如邮箱已被注册）。
func conflict(message string) *Error {
	return &Error{Status: http.StatusConflict, Code: kernel.CodeConflict, Reason: kernel.ReasonConflict, Message: message}
}

func internalFailure(cause error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: kernel.CodeInternal, Reason: kernel.ReasonInternal, Message: "系统处理失败，请稍后重试", Cause: cause}
}

// ErrNotAuthenticated 表示请求没有有效会话。
var ErrNotAuthenticated = unauthorized("当前未登录或登录已失效")

// Options 是服务装配参数。
type Options struct {
	Store       *Store
	EmailSender EmailSender
	SMSSender   SMSSender

	// SessionDays 是会话滑动有效期，AbsoluteSessionDays 是绝对上限。
	SessionDays         int
	AbsoluteSessionDays int
	CodeExpireMinutes   int
	// CodeCooldownSeconds 限制同一目标重复下发验证码的间隔。
	CodeCooldownSeconds int
	// DevEchoCode 允许在本地投递通道下把验证码随响应回显，省掉「去日志里捞码」。
	// 只对没有真实投递器的联调环境有意义，判断条件见 Service.devEcho。
	DevEchoCode bool

	// Now 可在测试中注入固定时钟。
	Now func() time.Time
	// StateSecret 用于给 OAuth state 签名。留空时读取 BEEFTV_AUTH_STATE_SECRET。
	StateSecret []byte
	// HTTPClient 供 OAuth 上游调用注入，留空时使用带超时的默认客户端。
	HTTPClient *http.Client
	// OnUserRegistered 在自动注册后触发，用于发放奖励等副作用。
	OnUserRegistered func(ctx context.Context, userID string) error
}

// Service 承载登录方式编排、会话签发与身份解析。
type Service struct {
	store       *Store
	emailSender EmailSender
	smsSender   SMSSender
	strategies  map[MethodType]*Strategy

	sessionDays         int
	absoluteSessionDays int
	codeExpireMinutes   int
	codeCooldownSeconds int
	devEchoCode         bool

	now              func() time.Time
	onUserRegistered func(ctx context.Context, userID string) error
	stateSecret      []byte
	client           *http.Client
	passwords        *passwordThrottle
}

// NewService 装配服务并注册全部登录方式。
func NewService(options Options) (*Service, error) {
	if options.Store == nil {
		return nil, errors.New("auth: Store 不能为空")
	}
	service := &Service{
		store:               options.Store,
		emailSender:         options.EmailSender,
		smsSender:           options.SMSSender,
		sessionDays:         options.SessionDays,
		absoluteSessionDays: options.AbsoluteSessionDays,
		codeExpireMinutes:   options.CodeExpireMinutes,
		codeCooldownSeconds: options.CodeCooldownSeconds,
		devEchoCode:         options.DevEchoCode,
		now:                 options.Now,
		onUserRegistered:    options.OnUserRegistered,
		stateSecret:         options.StateSecret,
		client:              options.HTTPClient,
	}
	if len(service.stateSecret) == 0 {
		if raw := strings.TrimSpace(os.Getenv("BEEFTV_AUTH_STATE_SECRET")); raw != "" {
			service.stateSecret = []byte(raw)
		}
	}
	if service.sessionDays <= 0 {
		service.sessionDays = DefaultSessionDays
	}
	if service.absoluteSessionDays <= 0 {
		service.absoluteSessionDays = AbsoluteSessionDays
	}
	if service.codeExpireMinutes <= 0 {
		service.codeExpireMinutes = DefaultCodeExpireMinutes
	}
	if service.codeCooldownSeconds <= 0 {
		service.codeCooldownSeconds = 60
	}
	if service.now == nil {
		service.now = time.Now
	}
	// 节流窗口用的时钟必须和会话/验证码完全一致，否则测试里注入的固定时钟
	// 只能推进一部分逻辑，失败计数会按真实时间流逝。
	service.passwords = newPasswordThrottle(service.now)
	// 让存储与服务共用同一个时钟，否则注入的测试时钟只在服务层生效。
	service.store.UseClock(service.now)
	service.strategies = service.buildStrategies()
	return service, nil
}

// Store 暴露底层存储，供宿主做账号侧联查。
func (s *Service) Store() *Store { return s.store }

// SessionCookieMaxAge 返回会话 Cookie 的最大存活秒数。
func (s *Service) SessionCookieMaxAge() int {
	if s == nil {
		return 0
	}
	return s.sessionDays * 24 * 60 * 60
}

// Strategy 返回某种登录方式，不存在时返回 nil。
func (s *Service) Strategy(methodType MethodType) *Strategy {
	if s == nil {
		return nil
	}
	return s.strategies[methodType]
}

// EnabledMethods 返回当前可用的登录方式配置，供前端渲染登录页。
func (s *Service) EnabledMethods(ctx context.Context) ([]MethodConfig, error) {
	return s.store.EnabledMethods()
}

// SendCode 下发验证码。
func (s *Service) SendCode(ctx context.Context, input SendCodeInput) (*SendCodeOutput, error) {
	config, strategy, err := s.requireStrategy(input.MethodType, func(item *Strategy) bool { return item.SendCode != nil })
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(string(config.MethodType), string(input.MethodType)) {
		return nil, invalidArgument("登录方式配置不匹配")
	}
	input.Config = *config

	issuedAt, err := s.store.LatestCodeIssuedAt(input.MethodType, strings.TrimSpace(input.Target), "login")
	if err != nil {
		return nil, internalFailure(err)
	}
	if issuedAt != nil {
		elapsed := s.now().Sub(*issuedAt)
		if elapsed < time.Duration(s.codeCooldownSeconds)*time.Second {
			return nil, rateLimited("验证码发送过于频繁，请稍后重试")
		}
	}

	output, err := strategy.SendCode(ctx, input)
	if err != nil {
		return nil, err
	}
	output.Cooldown = s.codeCooldownSeconds
	return output, nil
}

// devEcho 在本地投递通道下把验证码回显给调用方。
//
// 两个条件必须同时成立：显式配置 + 该通道确实在用只写日志的投递器。因此它是
// 「本地联调」的属性而不是一个可被误开的生产开关：通道一旦配上真实投递器
// （SMTP、阿里云短信），devOnlySender 断言不再成立，验证码就不会再出现在响应里。
func (s *Service) devEcho(code string, sender any) string {
	if s == nil || !s.devEchoCode {
		return ""
	}
	if _, isDevOnly := sender.(devOnlySender); !isDevOnly {
		return ""
	}
	return code
}

// Login 校验凭据并签发会话。
func (s *Service) Login(ctx context.Context, input LoginInput) (*LoginOutput, error) {
	config, strategy, err := s.requireStrategy(input.MethodType, func(item *Strategy) bool { return item.Login != nil })
	if err != nil {
		return nil, err
	}
	input.Config = *config
	return strategy.Login(ctx, input)
}

// Register 走显式注册入口建号，成功后直接签发会话。
//
// 三条前置校验缺一不可：登录方式允许注册、协议版本与当前版本一致、验证码有效。
// 协议版本必须由服务端比对——前端展示的条款和落库的版本号不能各说各话。
func (s *Service) Register(ctx context.Context, input RegisterInput) (*LoginOutput, error) {
	config, strategy, err := s.requireStrategy(input.MethodType, func(item *Strategy) bool { return item.Register != nil })
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(string(config.MethodType), string(input.MethodType)) {
		return nil, invalidArgument("登录方式配置不匹配")
	}
	if !config.AllowSignUp {
		return nil, forbidden("当前未开放注册")
	}
	if strings.TrimSpace(input.AgreementVersion) != agreementVersion {
		return nil, invalidArgument("用户协议已更新，请刷新页面后重试")
	}
	input.Config = *config
	return strategy.Register(ctx, input)
}

// AuthorizeURL 生成第三方授权跳转地址。
func (s *Service) AuthorizeURL(ctx context.Context, input AuthorizeInput) (*AuthorizeOutput, error) {
	config, strategy, err := s.requireStrategy(input.MethodType, func(item *Strategy) bool { return item.AuthorizeURL != nil })
	if err != nil {
		return nil, err
	}
	input.Config = *config
	return strategy.AuthorizeURL(ctx, input)
}

// HandleCallback 处理第三方授权回调。
func (s *Service) HandleCallback(ctx context.Context, input CallbackInput) (*LoginOutput, error) {
	config, strategy, err := s.requireStrategy(input.MethodType, func(item *Strategy) bool { return item.HandleCallback != nil })
	if err != nil {
		return nil, err
	}
	input.Config = *config
	return strategy.HandleCallback(ctx, input)
}

// requireStrategy 读取登录方式配置并确认其支持所需能力。
//
// 未启用和不存在都返回同一类错误，避免把「有哪些登录方式」暴露给探测请求。
func (s *Service) requireStrategy(methodType MethodType, supports func(*Strategy) bool) (*MethodConfig, *Strategy, error) {
	if strings.TrimSpace(string(methodType)) == "" {
		return nil, nil, invalidArgument("缺少登录方式类型")
	}
	strategy := s.strategies[methodType]
	if strategy == nil || !supports(strategy) {
		return nil, nil, invalidArgument("当前登录方式暂不支持该操作")
	}
	config, err := s.store.MethodConfig(methodType)
	if errors.Is(err, ErrNotFound) {
		return nil, nil, invalidArgument("当前登录方式未启用")
	}
	if err != nil {
		return nil, nil, internalFailure(err)
	}
	if !config.IsEnabled {
		return nil, nil, invalidArgument("当前登录方式未启用")
	}
	return config, strategy, nil
}

// SessionUser 用会话令牌解析当前用户。
//
// 这里同时施加三道额外约束，CanvasMind 原实现都缺：
//   - 绝对有效期：滑动续期不允许无限延长会话寿命
//   - 账号状态：DISABLED 用户立即失效，不等会话自然过期
//   - 活跃时间节流：避免每个请求都写一次 MySQL
func (s *Service) SessionUser(ctx context.Context, token string) (*AuthUser, error) {
	normalized := strings.TrimSpace(token)
	if normalized == "" {
		return nil, ErrNotAuthenticated
	}
	session, err := s.store.SessionByTokenHash(hashSessionToken(normalized))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotAuthenticated
	}
	if err != nil {
		return nil, internalFailure(err)
	}

	now := s.now()
	if now.Sub(session.CreatedAt) > time.Duration(s.absoluteSessionDays)*24*time.Hour {
		if revokeErr := s.store.RevokeSession(session.TokenHash, now); revokeErr != nil {
			return nil, internalFailure(revokeErr)
		}
		return nil, ErrNotAuthenticated
	}

	user, err := s.store.UserByID(session.UserID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotAuthenticated
	}
	if err != nil {
		return nil, internalFailure(err)
	}
	if user.Status == StatusDisabled {
		if revokeErr := s.store.RevokeUserSessions(user.ID, now); revokeErr != nil {
			return nil, internalFailure(revokeErr)
		}
		return nil, forbidden("账号已被禁用")
	}

	// 滑动续期：把到期时间推到 now + 会话有效期，但绝不越过绝对上限。
	// 这里只在真正延长时才写库，避免每次请求都产生一次 UPDATE。
	expiresAt := session.ExpiresAt
	if session.LastActiveAt == nil || now.Sub(*session.LastActiveAt) >= lastActiveWriteInterval {
		renewed := now.Add(time.Duration(s.sessionDays) * 24 * time.Hour)
		if hardLimit := session.CreatedAt.Add(time.Duration(s.absoluteSessionDays) * 24 * time.Hour); renewed.After(hardLimit) {
			renewed = hardLimit
		}
		if renewed.After(expiresAt) {
			if renewErr := s.store.RefreshSession(session.ID, now, renewed); renewErr != nil {
				return nil, internalFailure(renewErr)
			}
			expiresAt = renewed
		} else if touchErr := s.store.TouchSession(session.ID, now); touchErr != nil {
			return nil, internalFailure(touchErr)
		}
	}

	return toAuthUser(user, session.AuthMethodType, expiresAt), nil
}

// Logout 吊销当前会话。
func (s *Service) Logout(token string) error {
	normalized := strings.TrimSpace(token)
	if normalized == "" {
		return nil
	}
	if err := s.store.RevokeSession(hashSessionToken(normalized), s.now()); err != nil {
		return internalFailure(err)
	}
	return nil
}

// issueSession 生成令牌并落库，返回明文令牌供写入 Cookie。
func (s *Service) issueSession(user *User, methodType MethodType, identifier string, ip string, userAgent string) (*LoginOutput, error) {
	token, err := generateSessionToken()
	if err != nil {
		return nil, internalFailure(err)
	}
	now := s.now()
	expiresAt := now.Add(time.Duration(s.sessionDays) * 24 * time.Hour)

	session := &Session{
		UserID:             user.ID,
		TokenHash:          hashSessionToken(token),
		AuthMethodType:     methodType,
		IdentifierSnapshot: optionalString(identifier),
		IPAddress:          optionalString(ip),
		UserAgent:          optionalString(truncate(userAgent, 500)),
		ExpiresAt:          expiresAt,
		CreatedAt:          now,
		LastActiveAt:       &now,
	}
	if err := s.store.CreateSession(session); err != nil {
		return nil, internalFailure(err)
	}

	return &LoginOutput{
		Token:     token,
		ExpiresAt: expiresAt,
		User:      *toAuthUser(user, methodType, expiresAt),
	}, nil
}

// resolveExistingUser 按「身份绑定 → 同标识存量用户」的顺序解析账号。
//
// 这里刻意不建号：账号只能由注册入口创建。若允许登录时静默建号，用户就会在没有
// 看到协议的情况下被建出来，协议留痕也就无从谈起。第三方首次登录是否建号，由各自
// 的 OAuth 策略自行决定（那是 OAuth 的既有语义）。
func (s *Service) resolveExistingUser(methodType MethodType, identifier string) (*User, error) {
	if identity, err := s.store.IdentityByIdentifier(methodType, identifier); err == nil {
		user, userErr := s.store.UserByID(identity.UserID)
		if userErr == nil {
			return user, nil
		}
		if !errors.Is(userErr, ErrNotFound) {
			return nil, internalFailure(userErr)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, internalFailure(err)
	}

	existing, err := s.userByIdentifier(methodType, identifier)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, internalFailure(err)
	}
	if existing == nil {
		return nil, ErrNotFound
	}

	// 存量账号可能缺身份绑定（例如历史数据只落了邮箱列）：这里补齐，让后续登录
	// 统一走身份查询，而不是每次都回落到按列查找。
	now := s.now()
	identity := &AuthIdentity{
		UserID:     existing.ID,
		MethodType: methodType,
		Identifier: identifier,
		IsVerified: true,
		VerifiedAt: &now,
	}
	if err := s.store.CreateIdentity(identity); err != nil {
		return nil, internalFailure(err)
	}
	return existing, nil
}

// newUserCredential 描述一个待建账号以及它的首个登录标识。
//
// Verified 必须由调用方按「是否真的验证过这个标识」填写：验证码通道消费过验证码，
// 所以为 true；密码通道没有验证过标识归属，必须是 false。把它默认成 true 会让
// identity 表开始撒谎，而下游（例如第三方账号合并）会据此做错误决策。
type newUserCredential struct {
	MethodType   MethodType
	Identifier   string
	PasswordHash string
	Verified     bool
}

// createUserWithIdentity 建号并绑定登录标识（验证码/第三方通道，标识已验证）。
func (s *Service) createUserWithIdentity(ctx context.Context, methodType MethodType, identifier string) (*User, error) {
	return s.createUserWithIdentityCredential(ctx, newUserCredential{MethodType: methodType, Identifier: identifier, Verified: true})
}

// createUserWithIdentityCredential 建号并绑定登录标识。
//
// 账号与身份分两次写入：身份表是「这个标识属于这个账号」的唯一依据，缺了它下次
// 登录会退化成按邮箱列查找，第三方绑定也无法复用同一套解析逻辑。
func (s *Service) createUserWithIdentityCredential(ctx context.Context, in newUserCredential) (*User, error) {
	now := s.now()
	user := &User{Name: optionalString(defaultUserName(in.Identifier)), Status: StatusActive}
	switch in.MethodType {
	case MethodPhoneCode:
		user.Phone = optionalString(in.Identifier)
	case MethodEmailCode:
		user.Email = optionalString(in.Identifier)
	case MethodPassword:
		// 密码通道的标识既可能是邮箱也可能是手机号，落哪一列取决于形态而不是方法名：
		// 落错列会让唯一索引失去作用，同一个标识能建出两个账号。
		kind, _, err := classifyPasswordTarget(in.Identifier)
		switch kind {
		case targetPhone:
			user.Phone = optionalString(in.Identifier)
		case targetEmail:
			user.Email = optionalString(in.Identifier)
		default:
			return nil, internalFailure(err)
		}
	}
	if in.PasswordHash != "" {
		user.PasswordHash = optionalString(in.PasswordHash)
	}
	if err := s.store.CreateUser(user); err != nil {
		return nil, internalFailure(err)
	}
	identity := &AuthIdentity{
		UserID:     user.ID,
		MethodType: in.MethodType,
		Identifier: in.Identifier,
	}
	if in.Verified {
		identity.IsVerified = true
		identity.VerifiedAt = &now
	}
	if err := s.store.CreateIdentity(identity); err != nil {
		return nil, internalFailure(err)
	}
	if s.onUserRegistered != nil {
		if err := s.onUserRegistered(ctx, user.ID); err != nil {
			return nil, internalFailure(err)
		}
	}
	return user, nil
}

// recordAgreements 落下一组协议接受记录。
//
// 正文来自服务端当前版本；调用方负责在此之前确认入参的版本号与当前版本一致，
// 避免出现「同意的是旧条款、落库的是新版本」。
func (s *Service) recordAgreements(userID string, version string, ip string, userAgent string) error {
	documents := Agreements().Documents
	recordedAt := s.now()
	records := make([]UserAgreement, 0, len(documents))
	for _, document := range documents {
		records = append(records, UserAgreement{
			UserID:        userID,
			AgreementType: document.Type,
			Version:       version,
			AcceptedAt:    recordedAt,
			IPAddress:     optionalString(ip),
			UserAgent:     optionalString(truncate(userAgent, 500)),
		})
	}
	return s.store.CreateAgreements(records)
}

func (s *Service) userByIdentifier(methodType MethodType, identifier string) (*User, error) {
	switch methodType {
	case MethodPhoneCode:
		var user User
		err := s.store.db.Where("phone = ?", identifier).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return &user, nil
	case MethodEmailCode:
		return s.store.UserByEmail(identifier)
	case MethodPassword:
		// 与服务端归一后的标识保持一致：归一成邮箱就查邮箱列，否则查手机号列。
		if kind, _, err := classifyPasswordTarget(identifier); err == nil && kind == targetEmail {
			return s.store.UserByEmail(identifier)
		}
		return s.store.UserByPhone(identifier)
	default:
		return nil, ErrNotFound
	}
}

func toAuthUser(user *User, methodType MethodType, expiresAt time.Time) *AuthUser {
	avatar := ""
	if user.AvatarURL != nil {
		avatar = *user.AvatarURL
	}
	return &AuthUser{
		ID:       user.ID,
		Name:     user.DisplayName(),
		Email:    user.EmailValue(),
		Avatar:   avatar,
		Role:     user.Role,
		Method:   methodType,
		Status:   user.Status,
		ExpireAt: expiresAt,
	}
}

func optionalString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func truncate(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit]
}

// defaultUserName 生成默认展示名，与 CanvasMind 的「用户+后四位」保持一致。
//
// 邮箱先取 @ 之前的本地部分：直接对整串邮箱取后四位得到的是域名后缀（"用户.com"）。
func defaultUserName(identifier string) string {
	trimmed := strings.TrimSpace(identifier)
	if at := strings.Index(trimmed, "@"); at > 0 {
		trimmed = strings.TrimSpace(trimmed[:at])
	}
	if len(trimmed) <= 4 {
		return "用户" + trimmed
	}
	return "用户" + trimmed[len(trimmed)-4:]
}
