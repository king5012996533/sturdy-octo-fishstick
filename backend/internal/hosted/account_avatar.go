package hosted

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"infinite-canvas/backend/internal/avatar"

	"github.com/gin-gonic/gin"
)

// 用户头像：上传、清除，以及公开读取。
//
// 头像不走 resources 管线（原因见 internal/avatar），因此这里要自己管三件事：
//   - 写：校验 + 落盘 + 把地址写回账号；
//   - 读：按账号当前的头像地址决定这个公开地址还能不能取到图；
//   - 清：先清字段再删文件——字段是准入，删文件失败只是留下一点磁盘垃圾。
//
// 公开读这一条必须挂在 anonymousPathPrefixes 上：头像出现在侧栏、广场卡片和评论里，
// 一个没登录的访客也得能看见，否则整片广场的头像都会变成破图。

// avatarUploadMaxRequestBytes 是解析 multipart 之前就拦住的请求体上限。
//
// 比 MaxBytes 留出一点富余给表单分隔符与文件名；真正的准入判断在 avatar.Store 里，
// 这里只是不让一个 1GB 的请求先把磁盘写满、再返回"文件太大"。
const avatarUploadMaxRequestBytes = avatar.MaxBytes + 64*1024

func (e *Extension) registerAccountAvatarRoutes(api *gin.RouterGroup) {
	api.POST("/finance/account/avatar", e.handleAccountAvatarUpload)
	api.DELETE("/finance/account/avatar", e.handleAccountAvatarClear)
}

// registerPublicAvatarRoute 挂载公开头像读取。
//
// 与签名下载地址不同，这里不带签名也不带有效期：头像本身就是公开信息，且它的地址
// 会长期留在账号资料、广场条目与客户端缓存里，一个会过期的地址等于一片随机出现的
// 破图。准入改成"账号当前是否还在用平台头像"，清除头像后旧地址立即 404。
func (e *Extension) registerPublicAvatarRoute(api *gin.RouterGroup) {
	api.GET("/public/avatars/:userId", e.handlePublicAvatar)
}

func (e *Extension) handleAccountAvatarUpload(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	if e.avatars == nil {
		respondFailure(c, http.StatusInternalServerError, "头像存储尚未装配")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, avatarUploadMaxRequestBytes)
	header, err := c.FormFile("file")
	if err != nil {
		respondFailure(c, http.StatusBadRequest, "请选择要上传的图片")
		return
	}
	if _, err := e.avatars.Save(user.ID, header); err != nil {
		if avatar.IsInvalid(err) {
			respondFailure(c, http.StatusBadRequest, err.Error())
			return
		}
		respondServiceError(c, err)
		return
	}
	avatarURL, err := e.platformAvatarURL(user.ID)
	if err != nil {
		// 文件已经落盘但地址拼不出来（通常是没配 CANVAS_PUBLIC_BASE_URL）：此时
		// 宁可回失败，也不能把一份取不到的地址写进账号资料。
		respondServiceError(c, err)
		return
	}
	view, err := e.service.SetAvatarURL(user.ID, avatarURL)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAccountAvatarClear(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	view, err := e.service.ClearAvatar(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 先清字段再删文件：字段才是那个公开地址的准入条件，清完立刻取不到图。
	// 反过来的话，删文件成功而写库失败，用户会看到一个指向空文件的头像。
	if e.avatars != nil {
		if err := e.avatars.Remove(user.ID); err != nil {
			// 删文件失败只是留下一份再也不会被读到的磁盘垃圾：字段已经清空，
			// 上面那条公开路由不会再放行它。
			log.Printf("avatar: 删除头像文件失败 user=%s error_type=%T", user.ID, err)
		}
	}
	respondOK(c, view)
}

func (e *Extension) handlePublicAvatar(c *gin.Context) {
	userID := strings.TrimSpace(c.Param("userId"))
	if e.avatars == nil || userID == "" {
		respondFailure(c, http.StatusNotFound, "头像不存在")
		return
	}
	stored, err := e.service.StoredAvatarURL(userID)
	if err != nil {
		respondFailure(c, http.StatusNotFound, "头像不存在")
		return
	}
	// 只服务平台自己存的那张图。头像字段允许第三方地址（GitHub 登录带回来的、
	// 早期手工填过的），那些由浏览器直连对方站点；如果这里照着字段去 302，
	// 这个无鉴权路由就成了一条谁都可能被引导过去的开放跳转。
	if !isPlatformAvatarURL(stored, userID) {
		respondFailure(c, http.StatusNotFound, "头像不存在")
		return
	}
	file, err := e.avatars.Open(userID)
	if err != nil {
		respondFailure(c, http.StatusNotFound, "头像不存在")
		return
	}
	defer file.Body.Close()
	if file.MIMEType != "" {
		c.Header("Content-Type", file.MIMEType)
	}
	c.Header("X-Content-Type-Options", "nosniff")
	// 私有信息没有，但也别让中间层把它当成可长期共享的公共资源缓存到换头像之后。
	c.Header("Cache-Control", "public, max-age=86400")
	http.ServeContent(c.Writer, c.Request, userID, file.ModTime, file.Body)
}

// platformAvatarURL 拼出平台托管头像的公开地址。
//
// 版本参数取当前时间：同一个账号的地址在换头像后必须变化，否则浏览器与 CDN 会继续
// 拿缓存里那张旧图，用户会以为自己没换成功。
func (e *Extension) platformAvatarURL(userID string) (string, error) {
	if e.canvas == nil {
		return "", errors.New("hosted: 画布服务未装配，无法生成头像地址")
	}
	base, err := e.canvas.PublicBaseURL()
	if err != nil {
		return "", err
	}
	return base + "/api/public/avatars/" + url.PathEscape(userID) + "?v=" + strconv.FormatInt(time.Now().Unix(), 10), nil
}

// isPlatformAvatarURL 判断账号里的地址是不是平台头像路由签发的那一条。
//
// 按路径而不是按整个字符串比对：域名可能因为换备案、加 CDN 而变化，用整串比对的话，
// 改一次域名就会让所有存量头像同时失效。用户标识这一段必须吻合，否则 A 的地址会被
// 用来读 B 的图。
func isPlatformAvatarURL(raw string, userID string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return parsed.EscapedPath() == "/api/public/avatars/"+url.PathEscape(userID)
}
