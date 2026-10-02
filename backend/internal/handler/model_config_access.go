package handler

import (
	"errors"
	"net/http"

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

// modelConfigCredentialFields 是普通账号视图里必须抹掉的字段。
// headers 也算凭据：渠道自定义头里经常直接放 Authorization。
var modelConfigCredentialFields = []string{
	"apiKey", "secretKey", "encryptedApiKey", "deviceCode", "device_code", "headers",
}

// platformModelCatalogView 生成平台允许普通账号看到的模型目录：只保留平台自己的
// 渠道，并抹掉全部凭据。用户自定义渠道属于账号私有配置，不进这份视图。
func platformModelCatalogView(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	view := make(map[string]any, len(config))
	for key, value := range config {
		view[key] = value
	}
	stripModelConfigCredentials(view)

	channels, _ := config["channels"].([]any)
	allowed := make([]any, 0, len(channels))
	for _, raw := range channels {
		channel, ok := raw.(map[string]any)
		if !ok || !platformVisibleChannel(channel) {
			continue
		}
		sanitized := make(map[string]any, len(channel))
		for key, value := range channel {
			sanitized[key] = value
		}
		stripModelConfigCredentials(sanitized)
		allowed = append(allowed, sanitized)
	}
	view["channels"] = allowed
	return view
}

func stripModelConfigCredentials(fields map[string]any) {
	for _, field := range modelConfigCredentialFields {
		if _, ok := fields[field]; ok {
			fields[field] = ""
		}
	}
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
