package hosted

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// registerPublicResourceRoutes 暴露「带签名与有效期」的资源下载地址。
//
// 图生视频这类协议要求上游亲自拉取参考素材，平台因此必须给出一个不依赖会话的
// 公网地址。准入完全由 directResourceURL 签发的 HMAC（资源 ID + 到期时间）决定，
// 校验在 app.OpenPublicResourceRange 内完成；这里的中间件放行名单只负责不要在这条
// 路径上提前判 401，不放宽任何鉴权。缺了这条路由时，平台签发的地址会 401，而错误
// 会被归类成「模型不接受当前参数」，排查线索全部丢失。
func (e *Extension) registerPublicResourceRoutes(api *gin.RouterGroup) {
	download := func(c *gin.Context) {
		stream, err := e.canvas.OpenPublicResourceRange(
			c.Param("id"),
			c.Query("expires"),
			c.Query("signature"),
			c.GetHeader("Range"),
		)
		if err != nil {
			respondServiceError(c, err)
			return
		}
		defer stream.Body.Close()

		resource := stream.Resource
		mimeType := resource.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		c.Header("Accept-Ranges", "bytes")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Cache-Control", "private, max-age=300")
		if stream.ContentRange != "" {
			c.Header("Content-Range", stream.ContentRange)
		}
		if seeker, available := stream.Body.(io.ReadSeeker); available {
			c.Header("Content-Type", mimeType)
			http.ServeContent(c.Writer, c.Request, resource.ID, resource.UpdatedAt, seeker)
			return
		}
		c.DataFromReader(stream.StatusCode, stream.ContentLength, mimeType, stream.Body, nil)
	}
	api.GET("/public/resources/:id/file", download)
	api.GET("/public/resources/:id/file/:filename", download)
}
