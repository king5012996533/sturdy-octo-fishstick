package handler

import (
	"errors"
	"net/http"

	"infinite-canvas/backend/internal/app"

	"github.com/gin-gonic/gin"
)

// 站点设置（品牌、Logo、主题与备案信息）。
//
// 这一组路由只在托管实例注册（见 bootstrap/runtime.go 里 hosted 分支的
// RegisterAdminRoutes）：桌面产物不该出现"改站点品牌"的入口，那属于平台运营，
// 而不是本机工作区设置。业务规则与校验留在 app 层，这里只做绑定与投影。
func registerAdminSettingsRoutes(admin *gin.RouterGroup, svc *app.Service) {
	admin.GET("/settings/appearance", func(c *gin.Context) {
		appearance, err := svc.AdminAppearance(adminUser(c))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, appearance)
	})

	// 保存外观：Logo 与背景视频的引用来自上传接口返回的资源 ID，这里不接受裸 URL，
	// 避免把站外地址写进首屏。
	admin.PATCH("/settings/appearance", func(c *gin.Context) {
		var input app.AppearanceSetting
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, errors.New("外观设置参数格式错误"))
			return
		}
		appearance, err := svc.UpdateAppearance(adminUser(c), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, appearance)
	})

	admin.DELETE("/settings/appearance", func(c *gin.Context) {
		appearance, err := svc.ResetAppearance(adminUser(c))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, appearance)
	})

	// 上传槽位：logo / dark-logo / poster / video。
	admin.POST("/settings/appearance/assets/:slot", func(c *gin.Context) {
		header, err := c.FormFile("file")
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("缺少上传文件"))
			return
		}
		resource, err := svc.UploadAppearanceAsset(adminUser(c), c.Param("slot"), header)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"resource": resource})
	})
}
