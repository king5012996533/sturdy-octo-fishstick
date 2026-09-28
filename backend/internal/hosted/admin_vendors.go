package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/app"

	"github.com/gin-gonic/gin"
)

// 模型厂商管理（平台供应配置，存画布库）。
//
// 厂商层是裸渠道之上的一层目录：运营先选“接谁”，再在厂商下挂凭据，凭据落到
// model_channels。这一组接口因此只暴露厂商视角的字段，不回显密钥，也不允许
// 直接填 URL 绕过目录——那正是这一层要收敛掉的做法。
//
// 每个写操作都要落审计：厂商与凭据决定线上供应是否可用，事后复盘“谁把哪条
// 凭据停掉/改地址了”只能靠留痕。审计元数据里绝不放 API Key 或 Secret Key。

// registerAdminVendorRoutes 挂载 /api/admin 下的厂商管理路由（group 已带管理员守卫）。
func (e *Extension) registerAdminVendorRoutes(group *gin.RouterGroup) {
	group.GET("/vendors", e.handleAdminVendorList)
	// 目录必须注册在 /vendors/:id 之前：两者在同一层，静态段先注册可以避免
	// 路径树把 catalog 当成厂商 ID。
	group.GET("/vendors/catalog", e.handleAdminVendorCatalog)
	group.POST("/vendors", e.handleAdminVendorCreate)
	group.PUT("/vendors/:id", e.handleAdminVendorUpdate)
	group.DELETE("/vendors/:id", e.handleAdminVendorDelete)

	group.GET("/vendors/:id/credentials", e.handleAdminVendorCredentialList)
	group.POST("/vendors/:id/credentials", e.handleAdminVendorCredentialCreate)
	group.PUT("/vendors/:id/credentials/:credentialId", e.handleAdminVendorCredentialUpdate)
	group.DELETE("/vendors/:id/credentials/:credentialId", e.handleAdminVendorCredentialDelete)
	group.GET("/vendors/:id/credentials/:credentialId/models", e.handleAdminVendorCredentialModels)
	group.POST("/vendors/:id/credentials/:credentialId/models/import", e.handleAdminVendorCredentialModelImport)
	group.POST("/vendors/:id/credentials/:credentialId/probe", e.handleAdminVendorCredentialProbe)
}

func (e *Extension) handleAdminVendorList(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	vendors, err := e.canvas.AdminModelVendors()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"vendors": billingList(vendors)})
}

func (e *Extension) handleAdminVendorCatalog(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	catalog, err := e.canvas.VendorCatalog()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 目录单独用 catalog 键：它与厂商列表字段相近但语义不同（目录是可选模板，
	// 没有主键与读数），同名返回会让前端把“候选厂商”当成“已接入厂商”。
	respondOK(c, gin.H{"catalog": billingList(catalog)})
}

func (e *Extension) handleAdminVendorCreate(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	var input app.VendorInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "厂商参数格式错误")
		return
	}
	// 新建固定由服务层生成主键：请求体里的任何 ID 都应被丢弃，避免一次“新建”
	// 静默覆盖已有厂商。
	vendor, err := e.canvas.SaveModelVendor(input, "")
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "vendor.create", "vendor", vendor.ID, "新建模型厂商", gin.H{
		"code":         vendor.Code,
		"name":         vendor.Name,
		"kind":         vendor.Kind,
		"enabled":      vendor.Enabled,
		"sortOrder":    vendor.SortOrder,
		"protocols":    len(vendor.Protocols),
		"capabilities": len(vendor.Capabilities),
	})
	respondOK(c, gin.H{"vendor": billingView(vendor)})
}

func (e *Extension) handleAdminVendorUpdate(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少厂商标识")
		return
	}
	var input app.VendorInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "厂商参数格式错误")
		return
	}
	vendor, err := e.canvas.SaveModelVendor(input, id)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "vendor.update", "vendor", id, "更新模型厂商", gin.H{
		"code":         vendor.Code,
		"name":         vendor.Name,
		"kind":         vendor.Kind,
		"enabled":      vendor.Enabled,
		"sortOrder":    vendor.SortOrder,
		"protocols":    len(vendor.Protocols),
		"capabilities": len(vendor.Capabilities),
	})
	respondOK(c, gin.H{"vendor": billingView(vendor)})
}

func (e *Extension) handleAdminVendorDelete(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondFailure(c, http.StatusBadRequest, "缺少厂商标识")
		return
	}
	if err := e.canvas.DeleteModelVendor(id); err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "vendor.delete", "vendor", id, "删除模型厂商", gin.H{"id": id})
	respondOK(c, gin.H{"deleted": true})
}

func (e *Extension) handleAdminVendorCredentialList(c *gin.Context) {
	if e.canvas == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	credentials, err := e.canvas.VendorCredentials(strings.TrimSpace(c.Param("id")))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"credentials": billingList(credentials)})
}

func (e *Extension) handleAdminVendorCredentialCreate(c *gin.Context) {
	actor := canvasActor(adminActor(c))
	if e.canvas == nil || actor == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	vendorID := strings.TrimSpace(c.Param("id"))
	if vendorID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少厂商标识")
		return
	}
	var input app.CredentialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "凭据参数格式错误")
		return
	}
	credential, err := e.canvas.SaveVendorCredential(actor, vendorID, input, "")
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "credential.create", "credential", credential.ID, "新建厂商凭据", gin.H{
		"vendorId":  credential.VendorID,
		"channelId": credential.ChannelID,
		"name":      credential.Name,
		"apiFormat": credential.APIFormat,
		"weight":    credential.Weight,
		"enabled":   credential.Enabled,
		"models":    len(input.Models),
	})
	respondOK(c, gin.H{"credential": billingView(credential)})
}

func (e *Extension) handleAdminVendorCredentialUpdate(c *gin.Context) {
	actor := canvasActor(adminActor(c))
	if e.canvas == nil || actor == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	vendorID := strings.TrimSpace(c.Param("id"))
	credentialID := strings.TrimSpace(c.Param("credentialId"))
	if vendorID == "" || credentialID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少厂商或凭据标识")
		return
	}
	var input app.CredentialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "凭据参数格式错误")
		return
	}
	credential, err := e.canvas.SaveVendorCredential(actor, vendorID, input, credentialID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "credential.update", "credential", credential.ID, "更新厂商凭据", gin.H{
		"vendorId":  credential.VendorID,
		"channelId": credential.ChannelID,
		"name":      credential.Name,
		"apiFormat": credential.APIFormat,
		"weight":    credential.Weight,
		"enabled":   credential.Enabled,
		"models":    len(input.Models),
	})
	respondOK(c, gin.H{"credential": billingView(credential)})
}

func (e *Extension) handleAdminVendorCredentialDelete(c *gin.Context) {
	actor := canvasActor(adminActor(c))
	if e.canvas == nil || actor == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	vendorID := strings.TrimSpace(c.Param("id"))
	credentialID := strings.TrimSpace(c.Param("credentialId"))
	if vendorID == "" || credentialID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少厂商或凭据标识")
		return
	}
	if err := e.canvas.DeleteVendorCredential(actor, vendorID, credentialID); err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "credential.delete", "credential", credentialID, "删除厂商凭据", gin.H{
		"vendorId": vendorID,
	})
	respondOK(c, gin.H{"deleted": true})
}

func (e *Extension) handleAdminVendorCredentialModels(c *gin.Context) {
	actor := canvasActor(adminActor(c))
	if e.canvas == nil || actor == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	vendorID := strings.TrimSpace(c.Param("id"))
	credentialID := strings.TrimSpace(c.Param("credentialId"))
	if vendorID == "" || credentialID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少厂商或凭据标识")
		return
	}
	models, err := e.canvas.VendorCredentialModels(actor, vendorID, credentialID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"models": billingList(models)})
}

func (e *Extension) handleAdminVendorCredentialModelImport(c *gin.Context) {
	actor := canvasActor(adminActor(c))
	if e.canvas == nil || actor == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	vendorID := strings.TrimSpace(c.Param("id"))
	credentialID := strings.TrimSpace(c.Param("credentialId"))
	if vendorID == "" || credentialID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少厂商或凭据标识")
		return
	}
	var input struct {
		Models []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "模型参数格式错误")
		return
	}
	result, err := e.canvas.ImportVendorCredentialModels(c.Request.Context(), actor, vendorID, credentialID, input.Models)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "credential.model.import", "credential", credentialID, "导入厂商凭据模型", gin.H{
		"vendorId":  vendorID,
		"requested": len(input.Models),
		"added":     result.Added,
	})
	respondOK(c, gin.H{"models": billingList(result.Models), "added": result.Added})
}

func (e *Extension) handleAdminVendorCredentialProbe(c *gin.Context) {
	actor := canvasActor(adminActor(c))
	if e.canvas == nil || actor == nil {
		respondFailure(c, http.StatusServiceUnavailable, "管理后台尚未就绪")
		return
	}
	vendorID := strings.TrimSpace(c.Param("id"))
	credentialID := strings.TrimSpace(c.Param("credentialId"))
	if vendorID == "" || credentialID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少厂商或凭据标识")
		return
	}
	models, err := e.canvas.ProbeVendorCredentialModels(c.Request.Context(), actor, vendorID, credentialID)
	if err != nil {
		// 探测失败同样要留痕：这次写入已经把失败原因记进了凭据的最近检查记录，
		// 审计里没有对应条目会让“什么时候开始连不上”无从追起。
		e.recordAudit(c, "credential.probe", "credential", credentialID, "探测厂商凭据模型目录", gin.H{
			"vendorId": vendorID,
			"ok":       false,
		})
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "credential.probe", "credential", credentialID, "探测厂商凭据模型目录", gin.H{
		"vendorId":   vendorID,
		"ok":         true,
		"modelCount": len(models),
	})
	respondOK(c, gin.H{"models": billingList(models)})
}
