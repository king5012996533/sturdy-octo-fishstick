package hosted

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// 图生视频这类协议要求模型上游亲自拉取参考素材，平台签发的地址必须无需会话即可下载，
// 准入只看签名与有效期。这条路一旦缺失或提前判 401，上游拿不到图，而错误会被归类成
// 「模型不接受当前参数」，真正的故障点会从日志里消失。
func TestPublicResourceDownloadUsesSignatureInsteadOfSession(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")
	extension, _, _, service := newTestExtension(t)
	defer extension.Close()
	router := newTestRouter(extension, service)

	payload := []byte("reference-image-bytes")
	resource, err := service.UploadLocalResourceFile("user-1", "reference.png", int64(len(payload)), "image", 8, 8, 0, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("准备参考素材失败: %v", err)
	}

	signed, err := service.DirectResourceURL("user-1", resource.ID)
	if err != nil {
		t.Fatalf("签发下载地址失败: %v", err)
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("解析下载地址失败: %v", err)
	}
	if !strings.HasPrefix(parsed.Path, "/api/public/resources/") {
		t.Fatalf("下载地址不符合公开资源约定: %s", parsed.Path)
	}

	recorder := perform(router, http.MethodGet, parsed.Path+"?"+parsed.RawQuery, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("带签名的匿名下载应成功，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(recorder.Body.Bytes(), payload) {
		t.Fatalf("下载内容与上传内容不一致: %q", recorder.Body.String())
	}

	tampered := parsed.Query()
	tampered.Set("signature", "forged-signature")
	if recorder := perform(router, http.MethodGet, parsed.Path+"?"+tampered.Encode(), "", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("伪造签名应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	expired := parsed.Query()
	expired.Set("expires", "1")
	if recorder := perform(router, http.MethodGet, parsed.Path+"?"+expired.Encode(), "", nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("过期链接应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
