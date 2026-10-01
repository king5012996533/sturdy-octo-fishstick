package hosted

import (
	"net/http"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 用户中心的「个人资料」接口。
//
// 与 /finance/account（只读总览）分开：那个接口要合并用量、协议与计费口径，答的是
// "我是谁、我用了多少"；这一组只负责昵称与头像的读写，答的是"我长什么样"。合成一个
// 接口会让每次改昵称都顺带重算一遍 30 天用量。
func (e *Extension) registerAccountProfileRoutes(api *gin.RouterGroup) {
	api.GET("/finance/account/profile", e.handleAccountProfileGet)
	api.PATCH("/finance/account/profile", e.handleAccountProfileUpdate)
}

func (e *Extension) handleAccountProfileGet(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	view, err := e.service.Profile(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAccountProfileUpdate(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	var input struct {
		Name      string `json:"name"`
		AvatarURL string `json:"avatarUrl"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "资料参数格式错误")
		return
	}
	view, err := e.service.UpdateProfile(auth.ProfileUpdate{
		UserID:    user.ID,
		Name:      input.Name,
		AvatarURL: input.AvatarURL,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}
