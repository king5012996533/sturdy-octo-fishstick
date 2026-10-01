package avatar

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngHeader 是一张 PNG 的魔数，够 http.DetectContentType 认出格式。
//
// 这里不去构造一张真图：存储层的准入判断就是嗅探文件头，用例要钉住的正是"声明的内容
// 类型不算数、字节说了算"，造一张能解码的图反而把这条规则藏进了巧合里。
var pngHeader = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d}

func fileHeaderFor(t *testing.T, filename string, content []byte) *multipart.FileHeader {
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
	request := httptest.NewRequest(http.MethodPost, "/", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("解析 multipart 失败: %v", err)
	}
	header := request.MultipartForm.File["file"]
	if len(header) != 1 {
		t.Fatalf("multipart 里应有 1 个文件，实际 %d", len(header))
	}
	return header[0]
}

func TestStoreRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	content := append(append([]byte{}, pngHeader...), []byte("fake image body")...)
	header := fileHeaderFor(t, "avatar.png", content)

	// 声明成 text/plain 也照样被认成 PNG：准入看字节，不看客户端怎么说。
	header.Header.Set("Content-Type", "text/plain")
	mimeType, err := store.Save("user-1", header)
	if err != nil {
		t.Fatalf("保存头像失败: %v", err)
	}
	if mimeType != "image/png" {
		t.Fatalf("应按字节判定为 image/png，实际 %q", mimeType)
	}

	file, err := store.Open("user-1")
	if err != nil {
		t.Fatalf("读取头像失败: %v", err)
	}
	defer file.Body.Close()
	if file.MIMEType != "image/png" {
		t.Fatalf("读取时同样应嗅探出 image/png，实际 %q", file.MIMEType)
	}
	if file.Size != int64(len(content)) {
		t.Fatalf("读取到的长度应为 %d，实际 %d", len(content), file.Size)
	}
	got, err := io.ReadAll(file.Body)
	if err != nil {
		t.Fatalf("读取头像内容失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("读回的内容与写入的不一致")
	}
}

func TestStoreReplacesPreviousAvatar(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Save("user-1", fileHeaderFor(t, "a.png", pngHeader)); err != nil {
		t.Fatalf("保存第一张头像失败: %v", err)
	}
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}
	if _, err := store.Save("user-1", fileHeaderFor(t, "b.jpg", jpeg)); err != nil {
		t.Fatalf("覆盖头像失败: %v", err)
	}
	// 换格式不该留下第二份文件：地址由用户标识决定，留一份读不到的就是磁盘垃圾。
	entries, err := os.ReadDir(filepath.Join(store.files.Root(), "user-1"))
	if err != nil {
		t.Fatalf("读取头像目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("一个用户应只留一份头像文件，实际 %d 份", len(entries))
	}
	file, err := store.Open("user-1")
	if err != nil {
		t.Fatalf("读取覆盖后的头像失败: %v", err)
	}
	defer file.Body.Close()
	if file.MIMEType != "image/jpeg" {
		t.Fatalf("覆盖后应是 image/jpeg，实际 %q", file.MIMEType)
	}
}

func TestStoreRejectsUnsupportedContent(t *testing.T) {
	store := NewStore(t.TempDir())
	cases := []struct {
		name    string
		content []byte
	}{
		// 改名成 .png 的文本：这是最常见的一种"看起来像图片"。
		{name: "text.png", content: []byte("<!doctype html><html>not an image</html>")},
		// SVG 长得像图片，但可以内嵌脚本与外部引用，不在允许列表里。
		{name: "vector.png", content: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
	}
	for _, tc := range cases {
		_, err := store.Save("user-1", fileHeaderFor(t, tc.name, tc.content))
		if err == nil {
			t.Fatalf("%s 不应被接受", tc.name)
		}
		if !IsInvalid(err) {
			t.Fatalf("%s 的失败应被判定为内容问题（400），实际 %v", tc.name, err)
		}
	}
	if _, err := store.Open("user-1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("被拒绝的上传不应留下文件，Open 返回 %v", err)
	}
}

func TestStoreRejectsOversizeAvatar(t *testing.T) {
	store := NewStore(t.TempDir())
	big := append(append([]byte{}, pngHeader...), bytes.Repeat([]byte{0}, MaxBytes)...)
	_, err := store.Save("user-1", fileHeaderFor(t, "big.png", big))
	if err == nil || !IsInvalid(err) {
		t.Fatalf("超过上限的头像应被拒，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "2MB") {
		t.Fatalf("提示里应写明上限，实际 %q", err.Error())
	}
}

func TestStoreRemoveIsIdempotent(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Save("user-1", fileHeaderFor(t, "a.png", pngHeader)); err != nil {
		t.Fatalf("保存头像失败: %v", err)
	}
	if err := store.Remove("user-1"); err != nil {
		t.Fatalf("删除头像失败: %v", err)
	}
	if _, err := store.Open("user-1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("删除后不应还能读到，Open 返回 %v", err)
	}
	// 删除本来就没有的东西必须成功：用户可能在两个页面里分别点了一次移除。
	if err := store.Remove("user-1"); err != nil {
		t.Fatalf("重复删除应视为成功，实际 %v", err)
	}
}

// TestStoreRejectsUnsafeUserID 盯住键名拼装：用户标识是键里唯一的变量，
// 它能带出路径分隔符就意味着能写到别的账号、甚至别的目录去。
func TestStoreRejectsUnsafeUserID(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, userID := range []string{"", "  ", "../etc", "a/b", `a\b`, ".."} {
		if _, err := store.Save(userID, fileHeaderFor(t, "a.png", pngHeader)); err == nil {
			t.Fatalf("用户标识 %q 不应被接受", userID)
		}
	}
}
