package handler

import (
	"errors"
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/beefapi"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// /workspace/model-config 在托管形态下指向的是平台级运营配置：整个进程只有一份，
// 写它等于改所有账号的模型路由。因此这里按访问者收敛成三种视图，光靠前端隐藏入口
// 挡不住直接调接口的请求。
type modelConfigAccess uint8

const (
	// modelConfigAccessLocal 桌面形态：本地工作区照旧读写自己的配置。
	modelConfigAccessLocal modelConfigAccess = iota
	// modelConfigAccessFull 托管管理员：读写平台配置。
	modelConfigAccessFull
	// modelConfigAccessCatalog 托管普通账号：只读脱敏后的可用模型目录。
	modelConfigAccessCatalog
)

func modelConfigAccessFor(c *gin.Context, svc *app.Service) (modelConfigAccess, error) {
	if svc.IsLocalMode() {
		return modelConfigAccessLocal, nil
	}
	user, err := currentUser(c, svc)
	if err != nil {
		return modelConfigAccessLocal, err
	}
	if err := svc.RequireAdmin(user); err != nil {
		// 普通账号不是"无权打开这一页"：登录后仍然要能选模型，所以读取降级成
		// 可用目录，只有写入才是硬拒绝。
		var appErr *app.AppError
		if errors.As(err, &appErr) && appErr.Status == http.StatusForbidden {
			return modelConfigAccessCatalog, nil
		}
		return modelConfigAccessLocal, err
	}
	return modelConfigAccessFull, nil
}

// modelConfigCredentialFields 是普通账号视图里必须抹掉的字段名，按「去掉大小写与
// _ - 分隔符」后的形态收录，因此 apiKey / api_key / api-key / API_KEY 命中同一条。
//
// 用精确匹配而不是子串匹配：token 只该命中 token，不能把 inputTokens / maxTokens /
// contextTokens 这类计费与容量字段一起抹掉；同理由 key 派生出的 modelKey、itemKey、
// objectKey 也不在名单里。
//
// headers 也算凭据：渠道自定义头里经常直接放 Authorization。
// credentialRef 只存引用，但引用本身就是可用来换取凭据的句柄，同样不对外。
var modelConfigCredentialFields = []string{
	"apikey", "secretkey", "accesskey", "accesskeyid", "accesskeysecret",
	"secret", "clientsecret", "webhooksecret", "privatekey", "signingkey",
	"token", "accesstoken", "refreshtoken", "authtoken", "sessiontoken", "bearer",
	"password", "passwd", "authorization", "cookie", "headers",
	"encryptedapikey", "credential", "credentialref", "devicecode",
}

// isModelConfigCredentialKey 判定一个字段名是否是凭据。
func isModelConfigCredentialKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("_", "", "-", "", " ", "").Replace(normalized)
	for _, field := range modelConfigCredentialFields {
		if normalized == field {
			return true
		}
	}
	return false
}

// redactModelConfigCredentials 返回脱敏后的副本，凭据字段一律置空。
//
// 必须递归：凭据不只挂在渠道顶层，还可能藏在 modelProfiles[]、headers、或任意嵌套
// 对象里，只看一层等于没脱敏。
//
// 必须是「复制」而不是「就地改」：上层的浅拷贝让嵌套 map 与已保存的真实配置共享同一
// 个底层对象，就地抹掉会连平台自己的凭据一起清空——既泄不了密，还会把线上渠道打断。
func redactModelConfigCredentials(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if isModelConfigCredentialKey(key) {
				out[key] = ""
				continue
			}
			out[key] = redactModelConfigCredentials(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = redactModelConfigCredentials(item)
		}
		return out
	default:
		return value
	}
}

// platformModelCatalogView 生成平台允许普通账号看到的模型目录：只保留平台自己的
// 渠道，并抹掉全部凭据。用户自定义渠道属于账号私有配置，不进这份视图。
func platformModelCatalogView(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	redacted, _ := redactModelConfigCredentials(config).(map[string]any)
	if redacted == nil {
		return nil
	}

	channels, _ := redacted["channels"].([]any)
	allowed := make([]any, 0, len(channels))
	for _, raw := range channels {
		channel, ok := raw.(map[string]any)
		if !ok || !platformVisibleChannel(channel) {
			continue
		}
		allowed = append(allowed, channel)
	}
	redacted["channels"] = allowed
	return redacted
}

// platformVisibleChannel 判定一个渠道是否属于平台能力而非账号私有配置。
func platformVisibleChannel(channel map[string]any) bool {
	if scope, _ := channel["scope"].(string); scope == string(model.ChannelScopeSystem) {
		return true
	}
	if pinned, _ := channel["pinned"].(bool); pinned {
		return true
	}
	id, _ := channel["id"].(string)
	return id == beefapi.ChannelID
}
