package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

const adminUserKey = "canvas.adminUser"

// RegisterAdminRoutes 注册管理端接口。
//
// 这一层只做会话解析、管理员角色与参数绑定：渠道与模型的业务规则、密钥加密、
// 审计和一致性检查都留在 app 层，管理端与后台任务共用同一套实现。
// 整组路由挂在 requireAdminMiddleware 下，新增路由忘记鉴权就会变成接口级漏洞，
// 因此守卫放在 group 上而不是每个 handler 里。
func RegisterAdminRoutes(r *gin.RouterGroup, svc *app.Service) {
	admin := r.Group("/admin")
	admin.Use(requireAdminMiddleware(svc))

	// 模型协议目录：管理端只提供平台真正能执行、且保存时会通过的协议，
	// 避免"下拉里能选、保存时报协议无效"。
	admin.GET("/protocols", func(c *gin.Context) {
		items, err := svc.AdminProtocolCatalog(adminUser(c), c.Query("capability"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"providers": items})
	})

	admin.GET("/channels", func(c *gin.Context) {
		page, err := svc.AdminSystemChannelPage(adminUser(c), app.AdminListQuery{
			Keyword: c.Query("keyword"),
			Status:  c.Query("status"),
			Page:    adminIntQuery(c, "page"),
			Limit:   adminIntQuery(c, "pageSize"),
		})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, page)
	})

	admin.GET("/channels/:id", func(c *gin.Context) {
		channel, err := svc.AdminSystemChannel(adminUser(c), c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, channel)
	})

	admin.POST("/channels", func(c *gin.Context) {
		var input app.ChannelRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, errors.New("渠道参数格式错误"))
			return
		}
		channel, err := svc.CreateSystemChannel(adminUser(c), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, channel)
	})

	admin.PUT("/channels/:id", func(c *gin.Context) {
		var input app.ChannelRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, errors.New("渠道参数格式错误"))
			return
		}
		channel, err := svc.UpdateSystemChannel(adminUser(c), c.Param("id"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, channel)
	})

	admin.DELETE("/channels/:id", func(c *gin.Context) {
		if err := svc.DeleteSystemChannel(adminUser(c), c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": true})
	})

	admin.POST("/channels/:id/duplicate", func(c *gin.Context) {
		channel, err := svc.DuplicateSystemChannel(adminUser(c), c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, channel)
	})

	admin.GET("/channels/:id/models", func(c *gin.Context) {
		models, err := svc.AdminChannelModels(adminUser(c), c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"models": models})
	})

	// 上游目录只读预览：管理员挑完再走 import，避免一次拉取就把未定价的 SKU 写进库。
	admin.GET("/channels/:id/models/upstream", func(c *gin.Context) {
		models, err := svc.PreviewAdminChannelModels(c.Request.Context(), adminUser(c), c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"models": models})
	})

	admin.POST("/channels/:id/models/import", func(c *gin.Context) {
		var input app.AdminChannelModelImportRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, errors.New("模型导入参数格式错误"))
			return
		}
		result, err := svc.ImportAdminChannelModels(c.Request.Context(), adminUser(c), c.Param("id"), input.Models)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})

	admin.POST("/channels/:id/models", func(c *gin.Context) {
		req, bound := bindChannelModelRequest(c)
		if !bound {
			return
		}
		item, err := svc.SaveAdminChannelModel(adminUser(c), c.Param("id"), "", req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, item)
	})

	admin.PUT("/channels/:id/models/:modelId", func(c *gin.Context) {
		req, bound := bindChannelModelRequest(c)
		if !bound {
			return
		}
		item, err := svc.SaveAdminChannelModel(adminUser(c), c.Param("id"), c.Param("modelId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, item)
	})

	admin.DELETE("/channels/:id/models/:modelId", func(c *gin.Context) {
		if err := svc.DeleteAdminChannelModel(adminUser(c), c.Param("id"), c.Param("modelId")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": true})
	})

	admin.POST("/channels/:id/models/batch-delete", func(c *gin.Context) {
		var input struct {
			IDs []string `json:"ids"`
		}
		if err := c.ShouldBindJSON(&input); err != nil || len(input.IDs) == 0 {
			fail(c, http.StatusBadRequest, errors.New("请选择要删除的模型"))
			return
		}
		deleted, err := svc.DeleteAdminChannelModels(adminUser(c), c.Param("id"), input.IDs)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": deleted})
	})

	// 连通性测试走服务端：管理端只提交待测模型配置，渠道密钥不出库。
	admin.POST("/channels/:id/models/test", func(c *gin.Context) {
		req, bound := bindChannelModelRequest(c)
		if !bound {
			return
		}
		result, err := svc.TestAdminChannelModel(c.Request.Context(), adminUser(c), c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})

	admin.GET("/channels/:id/order", func(c *gin.Context) {
		order, err := svc.AdminChannelOrder(adminUser(c), c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"items": order})
	})

	admin.PUT("/channels/:id/order", func(c *gin.Context) {
		var input app.ChannelOrderRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, errors.New("排序参数格式错误"))
			return
		}
		if err := svc.SaveAdminChannelOrder(adminUser(c), c.Param("id"), input); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"saved": true})
	})

	// 渠道本身的排序：channelId 为空表示系统渠道列表。
	admin.GET("/order", func(c *gin.Context) {
		order, err := svc.AdminChannelOrder(adminUser(c), "")
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"items": order})
	})

	admin.PUT("/order", func(c *gin.Context) {
		var input app.ChannelOrderRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, errors.New("排序参数格式错误"))
			return
		}
		if err := svc.SaveAdminChannelOrder(adminUser(c), "", input); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"saved": true})
	})

	// 站点品牌、Logo、主题与备案信息。
	registerAdminSettingsRoutes(admin, svc)

	admin.GET("/features", func(c *gin.Context) {
		features, err := svc.AdminFeatureAvailability(adminUser(c))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, features)
	})

	// 功能开放直接决定前台形态：customChannels 关闭 + frontendModels 关闭
	// 才是"平台托管模型"，/api/ai/system 转发端点只在这个形态下工作。
	admin.PUT("/features", func(c *gin.Context) {
		var input app.FeatureAvailability
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, errors.New("功能开放参数格式错误"))
			return
		}
		features, err := svc.UpdateFeatureAvailability(adminUser(c), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, features)
	})

	admin.GET("/runtime-policy", func(c *gin.Context) {
		policy, err := svc.AdminRuntimePolicySetting(adminUser(c))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, policy)
	})

	admin.PUT("/runtime-policy", func(c *gin.Context) {
		var input app.RuntimePolicySetting
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, errors.New("运行时策略参数格式错误"))
			return
		}
		policy, err := svc.UpdateRuntimePolicySetting(adminUser(c), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, policy)
	})

	admin.DELETE("/runtime-policy", func(c *gin.Context) {
		policy, err := svc.ResetRuntimePolicySetting(adminUser(c))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, policy)
	})
}

// requireAdminMiddleware 把「管理员」收敛成一处：未登录 401，非管理员 403。
func requireAdminMiddleware(svc *app.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		if err := svc.RequireAdmin(user); err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		c.Set(adminUserKey, user)
		c.Next()
	}
}

func adminUser(c *gin.Context) *model.User {
	value, exists := c.Get(adminUserKey)
	if !exists {
		return nil
	}
	user, _ := value.(*model.User)
	return user
}

func bindChannelModelRequest(c *gin.Context) (app.ChannelModelRequest, bool) {
	var input app.ChannelModelRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, errors.New("模型参数格式错误"))
		return input, false
	}
	return input, true
}

func adminIntQuery(c *gin.Context, name string) int {
	value, err := strconv.Atoi(strings.TrimSpace(c.Query(name)))
	if err != nil {
		return 0
	}
	return value
}
