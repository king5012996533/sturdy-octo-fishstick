package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubUserURL      = "https://api.github.com/user"
	githubEmailsURL    = "https://api.github.com/user/emails"
	githubDefaultScope = "read:user user:email"

	// oauthStateTTL 限制授权跳转的有效期，防止回调被重放。
	oauthStateTTL = 10 * time.Minute
)

// githubProfile 是上游用户信息的子集。
type githubProfile struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// githubOAuthStrategy 实现 GitHub 授权登录。
//
// CanvasMind 只实现了 authorize 一步，没有回调、没有换 token、没有取用户信息，
// 因此这里不是移植而是新写；state 校验也必须补齐，否则回调可被伪造。
func (s *Service) githubOAuthStrategy() *Strategy {
	return &Strategy{
		MethodType: MethodGithubOAuth,
		Category:   CategoryOAuth,
		AuthorizeURL: func(_ context.Context, in AuthorizeInput) (*AuthorizeOutput, error) {
			config, err := ParseOAuthConfig(in.Config.ConfigJSON)
			if err != nil {
				return nil, internalFailure(err)
			}
			clientID := firstNonEmpty(config.ClientID, os.Getenv("BEEFTV_GITHUB_CLIENT_ID"))
			if clientID == "" {
				return nil, invalidArgument("GitHub 登录未配置 Client ID")
			}
			redirectURI := firstNonEmpty(in.RedirectURI, config.RedirectURI, os.Getenv("BEEFTV_GITHUB_REDIRECT_URI"))
			if redirectURI == "" {
				return nil, invalidArgument("GitHub 登录未配置回调地址")
			}
			state := strings.TrimSpace(in.State)
			if state == "" {
				generated, stateErr := s.signOAuthState()
				if stateErr != nil {
					return nil, stateErr
				}
				state = generated
			}
			target, parseErr := url.Parse(firstNonEmpty(config.AuthorizeURL, githubAuthorizeURL))
			if parseErr != nil {
				return nil, internalFailure(parseErr)
			}
			query := target.Query()
			query.Set("client_id", clientID)
			query.Set("redirect_uri", redirectURI)
			query.Set("response_type", firstNonEmpty(config.ResponseType, "code"))
			query.Set("scope", firstNonEmpty(config.Scope, githubDefaultScope))
			query.Set("state", state)
			target.RawQuery = query.Encode()
			return &AuthorizeOutput{AuthURL: target.String(), State: state}, nil
		},
		HandleCallback: func(ctx context.Context, in CallbackInput) (*LoginOutput, error) {
			if err := s.verifyOAuthState(in.State); err != nil {
				return nil, err
			}
			config, err := ParseOAuthConfig(in.Config.ConfigJSON)
			if err != nil {
				return nil, internalFailure(err)
			}
			clientID := firstNonEmpty(config.ClientID, os.Getenv("BEEFTV_GITHUB_CLIENT_ID"))
			clientSecret := firstNonEmpty(config.ClientSecret, os.Getenv("BEEFTV_GITHUB_CLIENT_SECRET"))
			if clientID == "" || clientSecret == "" {
				return nil, invalidArgument("GitHub 登录未完成配置")
			}
			redirectURI := firstNonEmpty(in.RedirectURI, config.RedirectURI, os.Getenv("BEEFTV_GITHUB_REDIRECT_URI"))

			accessToken, err := s.exchangeGithubCode(ctx, firstNonEmpty(config.TokenURL, githubTokenURL), clientID, clientSecret, in.Code, redirectURI)
			if err != nil {
				return nil, err
			}
			profile, err := s.fetchGithubProfile(ctx, firstNonEmpty(config.UserInfoURL, githubUserURL), accessToken)
			if err != nil {
				return nil, err
			}
			emails, err := s.fetchGithubEmails(ctx, firstNonEmpty(config.EmailURL, githubEmailsURL), accessToken)
			if err != nil {
				return nil, err
			}
			email := verifiedPrimaryEmail(emails)

			providerUserID := strconv.FormatInt(profile.ID, 10)
			user, err := s.resolveGithubUser(ctx, profile, providerUserID, email, in.Config.AllowSignUp)
			if err != nil {
				return nil, err
			}
			return s.issueSession(user, MethodGithubOAuth, providerUserID, in.RequesterIP, in.UserAgent)
		},
	}
}

// resolveGithubUser 绑定或创建 GitHub 用户。
//
// 绑定规则：只有上游明确标记 email_verified 时才允许按邮箱合并到存量账号。
// GitHub 允许用户填写任意邮箱，若不加这个限制，攻击者在自己的 GitHub 账号里
// 填上受害者邮箱就能接管账号。
func (s *Service) resolveGithubUser(ctx context.Context, profile *githubProfile, providerUserID string, verifiedEmail string, allowSignUp bool) (*User, error) {
	if identity, err := s.store.Identity(MethodGithubOAuth, providerUserID); err == nil {
		user, userErr := s.store.UserByID(identity.UserID)
		if userErr == nil {
			if user.Status == StatusDisabled {
				return nil, forbidden("账号已被禁用")
			}
			return user, nil
		}
		if !errors.Is(userErr, ErrNotFound) {
			return nil, internalFailure(userErr)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, internalFailure(err)
	}

	if verifiedEmail != "" {
		if existing, err := s.store.UserByEmail(verifiedEmail); err == nil {
			if existing.Status == StatusDisabled {
				return nil, forbidden("账号已被禁用")
			}
			now := s.now()
			identity := &AuthIdentity{
				UserID:         existing.ID,
				MethodType:     MethodGithubOAuth,
				ProviderUserID: optionalString(providerUserID),
				Identifier:     firstNonEmpty(profile.Login, verifiedEmail),
				IsVerified:     true,
				VerifiedAt:     &now,
			}
			if err := s.store.CreateIdentity(identity); err != nil {
				return nil, internalFailure(err)
			}
			return existing, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, internalFailure(err)
		}
	}

	if !allowSignUp {
		return nil, unauthorized("当前登录方式不允许自动注册")
	}
	if verifiedEmail == "" {
		return nil, unauthorized("GitHub 账号没有已验证的邮箱，无法完成注册")
	}

	now := s.now()
	user := &User{
		Name:      optionalString(firstNonEmpty(profile.Name, profile.Login, defaultUserName(verifiedEmail))),
		Email:     optionalString(verifiedEmail),
		AvatarURL: optionalString(profile.AvatarURL),
		Status:    StatusActive,
	}
	if err := s.store.CreateUser(user); err != nil {
		return nil, internalFailure(err)
	}
	identity := &AuthIdentity{
		UserID:         user.ID,
		MethodType:     MethodGithubOAuth,
		ProviderUserID: optionalString(providerUserID),
		Identifier:     firstNonEmpty(profile.Login, verifiedEmail),
		IsVerified:     true,
		VerifiedAt:     &now,
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

// signOAuthState 生成带签名的 state。
//
// 用签名而不是服务端暂存：不需要额外存储，同时能校验来源和时效。
func (s *Service) signOAuthState() (string, error) {
	if len(s.stateSecret) == 0 {
		return "", invalidArgument("认证 state 密钥未配置")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", internalFailure(err)
	}
	payload := fmt.Sprintf("%s.%d", base64.RawURLEncoding.EncodeToString(nonce), s.now().Unix())
	mac := hmac.New(sha256.New, s.stateSecret)
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *Service) verifyOAuthState(state string) error {
	if len(s.stateSecret) == 0 {
		return invalidArgument("认证 state 密钥未配置")
	}
	parts := strings.Split(strings.TrimSpace(state), ".")
	if len(parts) != 3 {
		return invalidArgument("登录状态校验失败，请重新发起授权")
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.stateSecret)
	mac.Write([]byte(payload))
	expected, err := hex.DecodeString(parts[2])
	if err != nil {
		return invalidArgument("登录状态校验失败，请重新发起授权")
	}
	if subtle.ConstantTimeCompare(expected, mac.Sum(nil)) != 1 {
		return invalidArgument("登录状态校验失败，请重新发起授权")
	}
	issuedAt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return invalidArgument("登录状态校验失败，请重新发起授权")
	}
	if s.now().Sub(time.Unix(issuedAt, 0)) > oauthStateTTL {
		return invalidArgument("登录状态已过期，请重新发起授权")
	}
	return nil
}

func (s *Service) exchangeGithubCode(ctx context.Context, endpoint string, clientID string, clientSecret string, code string, redirectURI string) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "", invalidArgument("缺少授权码")
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", internalFailure(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := s.httpClient().Do(request)
	if err != nil {
		return "", internalFailure(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return "", internalFailure(err)
	}
	if response.StatusCode != http.StatusOK {
		return "", unauthorized("GitHub 授权失败")
	}
	var payload struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", internalFailure(err)
	}
	if payload.AccessToken == "" {
		if payload.Error != "" {
			return "", unauthorized("GitHub 授权失败")
		}
		return "", unauthorized("GitHub 授权失败")
	}
	return payload.AccessToken, nil
}

func (s *Service) fetchGithubProfile(ctx context.Context, endpoint string, accessToken string) (*githubProfile, error) {
	body, err := s.githubGet(ctx, endpoint, accessToken)
	if err != nil {
		return nil, err
	}
	var profile githubProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, internalFailure(err)
	}
	if profile.ID == 0 {
		return nil, unauthorized("无法读取 GitHub 账号信息")
	}
	return &profile, nil
}

func (s *Service) fetchGithubEmails(ctx context.Context, endpoint string, accessToken string) ([]githubEmail, error) {
	body, err := s.githubGet(ctx, endpoint, accessToken)
	if err != nil {
		return nil, err
	}
	var emails []githubEmail
	if err := json.Unmarshal(body, &emails); err != nil {
		return nil, internalFailure(err)
	}
	return emails, nil
}

func (s *Service) githubGet(ctx context.Context, endpoint string, accessToken string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, internalFailure(err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.httpClient().Do(request)
	if err != nil {
		return nil, internalFailure(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return nil, internalFailure(err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, unauthorized("无法读取 GitHub 账号信息")
	}
	return bytes.TrimSpace(body), nil
}

func (s *Service) httpClient() *http.Client {
	if s.client != nil {
		return s.client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// verifiedPrimaryEmail 取已验证的主邮箱；没有主邮箱时退而取第一个已验证邮箱。
func verifiedPrimaryEmail(emails []githubEmail) string {
	fallback := ""
	for _, item := range emails {
		if !item.Verified || strings.TrimSpace(item.Email) == "" {
			continue
		}
		if item.Primary {
			return strings.ToLower(strings.TrimSpace(item.Email))
		}
		if fallback == "" {
			fallback = strings.ToLower(strings.TrimSpace(item.Email))
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
