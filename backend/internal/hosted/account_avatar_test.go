package hosted

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 头像的 HTTP 契约：上传 / 移除走会话，读取完全公开。
//
// 公开那一条是这一组里唯一一个不需要会话、又能返回字节的接口，因此用例除了跑通主链路，
// 还要钉住两件事：它不会变成开放跳转，以及它随账号字段立刻失效。

// avatarUploadPNG 是一张 PNG 的魔数 + 少量字节；存储层只嗅探文件头。
var avatarUploadPNG = append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("kinotv-avatar")...)

func performAvatarUpload(t *testing.T, router *gin.Engine, cookie *http.Cookie, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 失败: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/finance/account/avatar", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeProfile(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Data struct {
			AvatarURL string `json:"avatarUrl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析资料响应失败: %v %s", err, recorder.Body.String())
	}
	return payload.Data.AvatarURL
}

// TestHostedAccountAvatarRoundTrip 覆盖「上传 → 公开可读 → 移除后立刻 404」。
func TestHostedAccountAvatarRoundTrip(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, userID := registerAccount(t, router, authDB, "avatar@example.com")
	recorder := performAvatarUpload(t, router, cookie, "me.png", avatarUploadPNG)
	if recorder.Code != http.StatusOK {
		t.Fatalf("上传头像失败：%d %s", recorder.Code, recorder.Body.String())
	}
	avatarURL := decodeProfile(t, recorder)
	if !strings.HasPrefix(avatarURL, "https://example.com/api/public/avatars/"+userID) {
		t.Fatalf("头像地址应由服务端签发，实际 %q", avatarURL)
	}

	// 公开读：不带任何 Cookie。头像要出现在侧栏与广场卡片上，访客也得看得见。
	recorder = perform(router, http.MethodGet, "/api/public/avatars/"+userID, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("公开读取头像失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("头像应以嗅探出的类型返回，实际 %q", got)
	}
	if !bytes.Equal(recorder.Body.Bytes(), avatarUploadPNG) {
		t.Fatal("读回的头像内容与上传的不一致")
	}

	// 公开地址里带版本号，换头像之后浏览器不会继续拿缓存里那张旧图。
	if !strings.Contains(avatarURL, "?v=") {
		t.Fatalf("头像地址应带版本参数，实际 %q", avatarURL)
	}
	if recorder := perform(router, http.MethodGet, "/api/finance/account/profile", "", cookie); decodeProfile(t, recorder) != avatarURL {
		t.Fatalf("资料接口应回同一个地址，实际 %s", recorder.Body.String())
	}

	// 移除：字段先清空，那条公开地址随即失效。
	recorder = perform(router, http.MethodDelete, "/api/finance/account/avatar", "", cookie)
	if recorder.Code != http.StatusOK || decodeProfile(t, recorder) != "" {
		t.Fatalf("移除头像失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodGet, "/api/public/avatars/"+userID, "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("移除后公开地址应 404，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

func TestHostedAccountAvatarRequiresSession(t *testing.T) {
	extension, router, _, _ := newAdminRouter(t)
	defer extension.Close()

	if recorder := performAvatarUpload(t, router, nil, "me.png", avatarUploadPNG); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录上传应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodDelete, "/api/finance/account/avatar", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录移除应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// TestHostedAccountAvatarRejectsNonImage 盯住准入看字节而不是看文件名。
func TestHostedAccountAvatarRejectsNonImage(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, userID := registerAccount(t, router, authDB, "avatar-bad@example.com")
	recorder := performAvatarUpload(t, router, cookie, "photo.png", []byte("<html>not an image</html>"))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("非图片应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	// 被拒绝的上传不能在公开路径上留下任何东西。
	if recorder = perform(router, http.MethodGet, "/api/public/avatars/"+userID, "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("被拒绝的上传不应可读，实际 %d", recorder.Code)
	}
}

// TestHostedPublicAvatarRefusesExternalAddress 盯住这个公开路由不会变成开放跳转：
// 头像字段允许第三方地址，但那些由浏览器直连对方站点，不经过这里。
func TestHostedPublicAvatarRefusesExternalAddress(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, userID := registerAccount(t, router, authDB, "avatar-external@example.com")
	recorder := perform(router, http.MethodPatch, "/api/finance/account/profile",
		`{"name":"外链头像","avatarUrl":"https://evil.example.net/track.gif"}`, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("写入外链头像失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodGet, "/api/public/avatars/"+userID, "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("外链头像不该由本站代理，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
