// Package avatar 持有用户头像的本地存储。
//
// 头像刻意不走 resources 资源管线：那条管线有自己的引用计数与孤儿回收
// （app.cleanupDetachedResources 每小时扫一遍 24 小时没被任何画布文档引用的资源），
// 而头像的引用关系记在账号库里、不在画布文档里，对回收器永远不可见——放进去只会在
// 一天后被安静地删掉，表现为"用户某天回来头像没了"。
//
// 于是这里自己管一个目录：一个用户一个文件、固定键名、覆盖写。类型不靠扩展名判断，
// 读取时现嗅探，所以换格式不需要关心"要不要顺手删掉旧扩展名的那一份"。
package avatar

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"infinite-canvas/backend/internal/asset"
)

// MaxBytes 是头像文件上限。
//
// 2MB 按"手机直出照片直接传"取：侧栏与广场卡片最大也就渲染到 96px，再大纯属浪费
// 流量和磁盘，但压到几百 KB 会让用户先在本地裁一遍才能传。
const MaxBytes = 2 << 20

// sniffBytes 是嗅探文件类型要读的字节数。
//
// 512 与 http.DetectContentType 的内部上限一致；WebP 的魔数要到第 16 字节才完整，
// 读少了会把 WebP 判成 application/octet-stream。
const sniffBytes = 512

// allowedMIMETypes 是允许的头像格式。
//
// 只收这三种光栅格式。SVG 长得像图片，但它可以内嵌脚本与外部引用，一旦被当成
// <img> 之外的资源使用就是一个注入点；而头像没有任何非用 SVG 不可的理由。
var allowedMIMETypes = map[string]string{
	"image/png":  "PNG",
	"image/jpeg": "JPG",
	"image/webp": "WebP",
}

// InvalidError 表示上传内容本身不合法。
//
// 与存储故障分开：前者是用户改一下文件就能解决的 400，后者是 500，混在一起会让
// "图片太大"这种提示变成"系统处理失败"。
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &InvalidError{Message: fmt.Sprintf(format, args...)}
}

// IsInvalid 判断错误是否由上传内容引起。
func IsInvalid(err error) bool {
	var target *InvalidError
	return errors.As(err, &target)
}

// Store 是头像的本地存储。
type Store struct {
	files *asset.FileStore
}

// NewStore 在 dataDir 下开一个头像目录。
func NewStore(dataDir string) *Store {
	return &Store{files: asset.NewFileStoreAt(filepath.Join(dataDir, "avatars"))}
}

// File 是一次头像读取的结果。
type File struct {
	Body     *os.File
	MIMEType string
	Size     int64
	ModTime  time.Time
}

// Save 校验并写入一张头像，覆盖该用户此前的那张。
//
// 覆盖而不是追加：一个用户一个文件，地址由用户标识决定，不需要版本号——换头像
// 之后旧地址必须立刻失效，否则"我换掉了那张照片"对已经拿到旧地址的人就不成立。
func (s *Store) Save(userID string, header *multipart.FileHeader) (string, error) {
	if s == nil || s.files == nil {
		return "", errors.New("avatar: 存储未初始化")
	}
	key, err := objectKey(userID)
	if err != nil {
		return "", err
	}
	if header == nil || header.Size <= 0 {
		return "", invalid("请选择要上传的图片")
	}
	if header.Size > MaxBytes {
		return "", invalid("头像不能超过 %dMB", MaxBytes>>20)
	}
	file, err := header.Open()
	if err != nil {
		return "", err
	}
	defer file.Close()

	// 先嗅探再落盘：声明的内容类型来自客户端，不能作为准入依据，否则把任意文件
	// 改名成 .png 就能存进来，而这条存储最终是原样吐给浏览器的。
	head := make([]byte, sniffBytes)
	read, readErr := io.ReadFull(file, head)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return "", readErr
	}
	mimeType := normalizeMIMEType(http.DetectContentType(head[:read]))
	if _, allowed := allowedMIMETypes[mimeType]; !allowed {
		return "", invalid("头像仅支持 PNG、JPG 或 WebP 图片")
	}
	body := io.MultiReader(bytes.NewReader(head[:read]), file)
	if err := s.files.Write(key, body); err != nil {
		return "", err
	}
	return mimeType, nil
}

// Open 读取该用户的头像，没有时返回 os.ErrNotExist。
func (s *Store) Open(userID string) (*File, error) {
	if s == nil || s.files == nil {
		return nil, errors.New("avatar: 存储未初始化")
	}
	key, err := objectKey(userID)
	if err != nil {
		return nil, err
	}
	file, err := s.files.Open(key)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	head := make([]byte, sniffBytes)
	read, readErr := file.Read(head)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		file.Close()
		return nil, readErr
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, err
	}
	return &File{
		Body:     file,
		MIMEType: normalizeMIMEType(http.DetectContentType(head[:read])),
		Size:     info.Size(),
		ModTime:  info.ModTime(),
	}, nil
}

// Remove 删除该用户的头像文件，文件本来就不存在时视为成功。
func (s *Store) Remove(userID string) error {
	if s == nil || s.files == nil {
		return errors.New("avatar: 存储未初始化")
	}
	key, err := objectKey(userID)
	if err != nil {
		return err
	}
	return s.files.Delete(key)
}

// objectKey 只接受调用方从会话里取到的用户标识。
//
// 键名不由外部输入拼装（没有文件名、没有扩展名），因此这里的用户标识是键里唯一
// 的可变部分；空值仍然要拦掉，否则会落到 avatars/ 目录本身。
func objectKey(userID string) (string, error) {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" || strings.ContainsAny(trimmed, `/\`) || trimmed == "." || trimmed == ".." {
		return "", errors.New("avatar: 用户标识无效")
	}
	return filepath.ToSlash(filepath.Join(trimmed, "avatar")), nil
}

// normalizeMIMEType 去掉嗅探结果里的字符集参数。
func normalizeMIMEType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
}
