package hosted

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	mcpTokenPrefix          = "ktv_mcp_"
	mcpTokenDefaultDays     = 30
	mcpTokenMaxDays         = 365
	mcpTokenMaxPerUser      = 20
	mcpTokenLastUsedSpacing = 5 * time.Minute
)

var mcpTokenScopes = []string{"agent", "canvas", "tasks", "models"}

type mcpTokenView struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Prefix           string     `json:"prefix"`
	GrantID          string     `json:"grantId,omitempty"`
	Scopes           []string   `json:"scopes"`
	PermissionMode   string     `json:"permissionMode,omitempty"`
	MaxCreditsPerRun int64      `json:"maxCreditsPerRun,omitempty"`
	MaxCreditsPerDay int64      `json:"maxCreditsPerDay,omitempty"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt       *time.Time `json:"lastUsedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	RevokedAt        *time.Time `json:"revokedAt,omitempty"`
}

type mcpTokenCreateInput struct {
	Name             string `json:"name"`
	ExpiresInDays    int    `json:"expiresInDays"`
	PermissionMode   string `json:"permissionMode"`
	MaxCreditsPerRun int64  `json:"maxCreditsPerRun"`
	MaxCreditsPerDay int64  `json:"maxCreditsPerDay"`
}

type mcpTokenCreateOutput struct {
	Token     string       `json:"token"`
	TokenView mcpTokenView `json:"tokenView"`
}

func (e *Extension) registerMCPIntegrationRoutes(api *gin.RouterGroup) {
	group := api.Group("/integrations/mcp")
	group.GET("/status", e.handleMCPStatus)
	group.GET("/tokens", e.handleMCPTokenList)
	group.POST("/tokens", e.handleMCPTokenCreate)
	group.DELETE("/tokens/:id", e.handleMCPTokenRevoke)
	group.GET("/grants", e.handleAgentGrantList)
	group.DELETE("/grants/:id", e.handleAgentGrantRevoke)
}

func (e *Extension) handleAgentGrantList(c *gin.Context) {
	if !e.mcpTokenStoreReady(c) {
		return
	}
	user, ok := e.mcpSessionUser(c)
	if !ok {
		return
	}
	var grants []model.AgentGrant
	if err := e.canvasDB.Where("user_id = ?", user.ID).Order("created_at desc").Find(&grants).Error; err != nil {
		respondFailure(c, http.StatusInternalServerError, "读取 Agent 授权失败")
		return
	}
	respondOK(c, gin.H{"grants": grants})
}

// handleAgentGrantRevoke 撤销授权并递增 Version。旧 Run 的下一次额度预留会
// 因版本不匹配被拒绝，不能继续使用过期权限；令牌本身保留画布能力，只是不能再
// 发起 Agent 运行。
func (e *Extension) handleAgentGrantRevoke(c *gin.Context) {
	if !e.mcpTokenStoreReady(c) {
		return
	}
	user, ok := e.mcpSessionUser(c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	result := e.canvasDB.Model(&model.AgentGrant{}).
		Where("id = ? AND user_id = ?", strings.TrimSpace(c.Param("id")), user.ID).
		Updates(map[string]any{
			"status":     model.AgentGrantStatusRevoked,
			"revoked_at": now,
			"updated_at": now,
			"version":    gorm.Expr("version + 1"),
		})
	if result.Error != nil {
		respondFailure(c, http.StatusInternalServerError, "撤销 Agent 授权失败")
		return
	}
	if result.RowsAffected == 0 {
		respondFailure(c, http.StatusNotFound, "Agent 授权不存在")
		return
	}
	respondOK(c, gin.H{"revoked": true})
}

func (e *Extension) mcpTokenStoreReady(c *gin.Context) bool {
	if e.canvasDB != nil {
		return true
	}
	respondFailure(c, http.StatusServiceUnavailable, "MCP 连接存储未配置")
	return false
}

func (e *Extension) handleMCPStatus(c *gin.Context) {
	if !e.mcpTokenStoreReady(c) {
		return
	}
	user, ok := e.mcpSessionUser(c)
	if !ok {
		return
	}
	var count int64
	if err := e.canvasDB.Model(&model.MCPToken{}).
		Where("user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", user.ID, time.Now()).
		Count(&count).Error; err != nil {
		respondFailure(c, http.StatusInternalServerError, "读取连接状态失败")
		return
	}
	respondOK(c, gin.H{"enabled": true, "baseUrl": mcpBaseURL(c), "tokenCount": count})
}

func (e *Extension) handleMCPTokenList(c *gin.Context) {
	if !e.mcpTokenStoreReady(c) {
		return
	}
	user, ok := e.mcpSessionUser(c)
	if !ok {
		return
	}
	var tokens []model.MCPToken
	if err := e.canvasDB.Where("user_id = ?", user.ID).Order("created_at desc").Find(&tokens).Error; err != nil {
		respondFailure(c, http.StatusInternalServerError, "读取连接令牌失败")
		return
	}
	grantIDs := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token.GrantID != "" {
			grantIDs = append(grantIDs, token.GrantID)
		}
	}
	grants := map[string]model.AgentGrant{}
	if len(grantIDs) > 0 {
		var rows []model.AgentGrant
		if err := e.canvasDB.Where("user_id = ? AND id IN ?", user.ID, grantIDs).Find(&rows).Error; err != nil {
			respondFailure(c, http.StatusInternalServerError, "读取 Agent 授权失败")
			return
		}
		for _, grant := range rows {
			grants[grant.ID] = grant
		}
	}
	views := make([]mcpTokenView, 0, len(tokens))
	for _, token := range tokens {
		view := mcpTokenViewOf(token)
		if grant, ok := grants[token.GrantID]; ok {
			view.applyGrant(grant)
		}
		views = append(views, view)
	}
	respondOK(c, gin.H{"tokens": views})
}

func (e *Extension) handleMCPTokenCreate(c *gin.Context) {
	if !e.mcpTokenStoreReady(c) {
		return
	}
	user, ok := e.mcpSessionUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var input mcpTokenCreateInput
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "连接令牌参数无效")
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 100 {
		respondFailure(c, http.StatusBadRequest, "令牌名称需要 1–100 个字符")
		return
	}
	days := input.ExpiresInDays
	if days == 0 {
		days = mcpTokenDefaultDays
	}
	if days < 1 || days > mcpTokenMaxDays {
		respondFailure(c, http.StatusBadRequest, "有效期需要在 1–365 天之间")
		return
	}
	now := time.Now().UTC()
	var active int64
	if err := e.canvasDB.Model(&model.MCPToken{}).
		Where("user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", user.ID, now).
		Count(&active).Error; err != nil {
		respondFailure(c, http.StatusInternalServerError, "读取连接令牌失败")
		return
	}
	if active >= mcpTokenMaxPerUser {
		respondFailure(c, http.StatusConflict, "连接令牌数量已达上限，请先吊销不用的令牌")
		return
	}
	permissionMode := strings.TrimSpace(input.PermissionMode)
	if permissionMode == "" {
		permissionMode = "auto"
	}
	switch permissionMode {
	case "read_only", "request_approval", "auto":
	default:
		respondFailure(c, http.StatusBadRequest, "权限模式无效")
		return
	}
	maxRun, maxDay := input.MaxCreditsPerRun, input.MaxCreditsPerDay
	if maxRun == 0 {
		maxRun = model.AgentGrantDefaultMaxCreditsPerRun
	}
	if maxDay == 0 {
		maxDay = model.AgentGrantDefaultMaxCreditsPerDay
	}
	if maxRun < 0 || maxDay < 0 || maxRun > 10_000_000 || maxDay > 10_000_000 {
		respondFailure(c, http.StatusBadRequest, "积分额度需要在 0–10,000,000 之间")
		return
	}
	if permissionMode == "auto" && (maxRun <= 0 || maxDay <= 0) {
		respondFailure(c, http.StatusBadRequest, "自主执行必须同时设置单轮与每日积分上限")
		return
	}
	plain, hash, prefix, err := generateMCPToken()
	if err != nil {
		respondFailure(c, http.StatusInternalServerError, "生成连接令牌失败")
		return
	}
	expiresAt := now.Add(time.Duration(days) * 24 * time.Hour)
	grant := model.AgentGrant{
		ID:               mcpTokenID(),
		UserID:           user.ID,
		Client:           model.AgentGrantClientMCP,
		ScopesJSON:       mustJSON(mcpTokenScopes),
		PermissionMode:   permissionMode,
		MaxCreditsPerRun: maxRun,
		MaxCreditsPerDay: maxDay,
		AutoRetryEnabled: false,
		Status:           model.AgentGrantStatusActive,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	token := model.MCPToken{
		ID:         mcpTokenID(),
		UserID:     user.ID,
		Name:       name,
		TokenHash:  hash,
		Prefix:     prefix,
		GrantID:    grant.ID,
		ScopesJSON: mustJSON(mcpTokenScopes),
		ExpiresAt:  &expiresAt,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := e.canvasDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&grant).Error; err != nil {
			return err
		}
		return tx.Create(&token).Error
	}); err != nil {
		respondFailure(c, http.StatusInternalServerError, "保存连接令牌失败")
		return
	}
	view := mcpTokenViewOf(token)
	view.applyGrant(grant)
	respondOK(c, mcpTokenCreateOutput{Token: plain, TokenView: view})
}

func (e *Extension) handleMCPTokenRevoke(c *gin.Context) {
	if !e.mcpTokenStoreReady(c) {
		return
	}
	user, ok := e.mcpSessionUser(c)
	if !ok {
		return
	}
	var token model.MCPToken
	if err := e.canvasDB.Where("id = ? AND user_id = ?", strings.TrimSpace(c.Param("id")), user.ID).First(&token).Error; err != nil {
		respondFailure(c, http.StatusNotFound, "连接令牌不存在")
		return
	}
	if token.RevokedAt == nil {
		now := time.Now().UTC()
		if err := e.canvasDB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&token).Updates(map[string]any{"revoked_at": now, "updated_at": now}).Error; err != nil {
				return err
			}
			if token.GrantID == "" {
				return nil
			}
			return tx.Model(&model.AgentGrant{}).
				Where("id = ? AND user_id = ?", token.GrantID, user.ID).
				Updates(map[string]any{
					"status":     model.AgentGrantStatusRevoked,
					"revoked_at": now,
					"updated_at": now,
					"version":    gorm.Expr("version + 1"),
				}).Error
		}); err != nil {
			respondFailure(c, http.StatusInternalServerError, "吊销连接令牌失败")
			return
		}
	}
	respondOK(c, gin.H{"revoked": true})
}

func (e *Extension) mcpSessionUser(c *gin.Context) (*auth.AuthUser, bool) {
	user, err := e.service.SessionUser(c.Request.Context(), auth.ReadSessionToken(c.Request))
	if err != nil {
		respondFailure(c, http.StatusUnauthorized, "当前未登录或登录已失效")
		return nil, false
	}
	return user, true
}

// authenticateMCPToken validates a Bearer token and returns its account.
func (e *Extension) authenticateMCPToken(raw string) (*auth.AuthUser, *model.MCPToken, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return nil, nil, errors.New("missing token")
	}
	if e.canvasDB == nil {
		return nil, nil, errors.New("token store unavailable")
	}
	var token model.MCPToken
	if err := e.canvasDB.Where("token_hash = ?", hashMCPToken(normalized)).First(&token).Error; err != nil {
		return nil, nil, errors.New("invalid token")
	}
	now := time.Now().UTC()
	if token.RevokedAt != nil {
		return nil, nil, errors.New("revoked token")
	}
	if token.ExpiresAt != nil && !token.ExpiresAt.After(now) {
		return nil, nil, errors.New("expired token")
	}
	user, err := e.service.UserByID(token.UserID)
	if err != nil {
		return nil, nil, err
	}
	if token.LastUsedAt == nil || now.Sub(*token.LastUsedAt) >= mcpTokenLastUsedSpacing {
		_ = e.canvasDB.Model(&model.MCPToken{}).Where("id = ?", token.ID).
			Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error
		token.LastUsedAt = &now
	}
	return user, &token, nil
}

func mcpTokenViewOf(token model.MCPToken) mcpTokenView {
	scopes := []string{}
	_ = json.Unmarshal([]byte(token.ScopesJSON), &scopes)
	return mcpTokenView{
		ID: token.ID, Name: token.Name, Prefix: token.Prefix, GrantID: token.GrantID, Scopes: scopes,
		ExpiresAt: token.ExpiresAt, LastUsedAt: token.LastUsedAt,
		CreatedAt: token.CreatedAt, RevokedAt: token.RevokedAt,
	}
}

func (v *mcpTokenView) applyGrant(grant model.AgentGrant) {
	v.PermissionMode = grant.PermissionMode
	v.MaxCreditsPerRun = grant.MaxCreditsPerRun
	v.MaxCreditsPerDay = grant.MaxCreditsPerDay
}

func generateMCPToken() (string, string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", err
	}
	plain := mcpTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	prefix := plain
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	return plain, hashMCPToken(plain), prefix, nil
}

func hashMCPToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func mcpTokenID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "mcp-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(raw)
}

func mustJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func readBearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 7 || !strings.EqualFold(value[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

func mcpBaseURL(c *gin.Context) string {
	if value := strings.TrimSpace(os.Getenv("CANVAS_PUBLIC_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); forwarded != "" {
		scheme = forwarded
	}
	return scheme + "://" + c.Request.Host
}

// mcpPathScope returns the token capability required for a business route.
// Unknown paths are denied: an MCP token is not a general session.
func mcpPathScope(path string) string {
	switch {
	case path == "/api/workspace/model-config" || path == "/api/model-catalog":
		return "models"
	case strings.HasPrefix(path, "/api/agent/runs"):
		return "agent"
	case strings.HasPrefix(path, "/api/canvas-projects"):
		return "canvas"
	case strings.HasPrefix(path, "/api/tasks"):
		return "tasks"
	default:
		return ""
	}
}

func mcpTokenAllows(token *model.MCPToken, method, path string) bool {
	required := mcpPathScope(path)
	if required == "" {
		return false
	}
	// Tasks and model/workspace reads are read-only for an MCP token. The
	// adapter never creates raw tasks or edits model config; keeping those
	// writes on the session path prevents an external Agent from bypassing
	// the Agent admission and budget flow.
	if (required == "tasks" || required == "models") && method != http.MethodGet {
		return false
	}
	if required == "canvas" && !mcpCanvasMethodAllowed(method, path) {
		return false
	}
	var scopes []string
	if err := json.Unmarshal([]byte(token.ScopesJSON), &scopes); err != nil {
		return false
	}
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}

// mcpCanvasMethodAllowed 把画布令牌限制在"编辑节点"，不再等同于整个画布会话。
//
// 节点级的增删改与连线是 Agent 的日常创作动作；删除整个项目、恢复历史快照是
// 不可逆操作，必须走会话界面并触发审批（见 cloudAgentApprovalWhitelist）。
func mcpCanvasMethodAllowed(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return true
	case http.MethodDelete:
		// 只允许删除单个节点；DELETE /api/canvas-projects/:id 是删整个作品。
		return strings.Contains(path, "/nodes/")
	case http.MethodPost:
		if strings.Contains(path, "/history/") && strings.HasSuffix(path, "/restore") {
			return false
		}
		return true
	default:
		return true
	}
}

// mcpRequestToken is used by the workspace middleware before session lookup.
func (e *Extension) mcpRequestToken(c *gin.Context) (*auth.AuthUser, *model.MCPToken, bool) {
	raw := readBearerToken(c.Request)
	if raw == "" {
		return nil, nil, false
	}
	user, token, err := e.authenticateMCPToken(raw)
	if err != nil {
		handler.AbortUnauthorized(c, "连接令牌无效或已失效")
		return nil, nil, true
	}
	if !mcpTokenAllows(token, c.Request.Method, c.Request.URL.Path) {
		handler.AbortForbidden(c, "该连接令牌不允许访问此接口")
		return nil, nil, true
	}
	return user, token, true
}
