package handler

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

var errSystemProxyResponseTooLarge = errors.New("系统渠道流式响应超过配置上限")

// maxSystemProxyErrorCaptureBytes 限制诊断缓冲：流式转发的目的是转发而不是留证，
// 保留前 64KB 足以定位上游错误，又不会把整个响应体堆进内存。
const maxSystemProxyErrorCaptureBytes = 64 << 10

func isSystemProxyEventStream(resp *http.Response) bool {
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mediaType, "text/event-stream")
}

func copySystemProxyResponseHeaders(c *gin.Context, resp *http.Response) {
	for _, key := range []string{"Content-Type", "Cache-Control", "Content-Disposition"} {
		if value := resp.Header.Get(key); value != "" {
			c.Header(key, value)
		}
	}
	c.Header("X-Content-Type-Options", "nosniff")
}

// streamSystemProxyResponse 边转发边脱敏。secrets 是平台渠道的凭证：
// 上游有时会在流式错误或回显里带上密钥，转发层必须负责抹掉，而不是指望上游克制。
func streamSystemProxyResponse(c *gin.Context, resp *http.Response, responseLimit int64, secrets ...string) ([]byte, error) {
	copySystemProxyResponseHeaders(c, resp)
	// 同时通知镜像内和可能存在的外层 Nginx，不得重新缓冲模型事件流。
	c.Header("Cache-Control", "no-cache, no-store, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Status(resp.StatusCode)
	c.Writer.WriteHeaderNow()
	c.Writer.Flush()

	redactor := newRelayStreamRedactor(secrets...)
	var captured bytes.Buffer
	capture := func(chunk []byte) {
		if captured.Len() >= maxSystemProxyErrorCaptureBytes {
			return
		}
		remaining := maxSystemProxyErrorCaptureBytes - captured.Len()
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		_, _ = captured.Write(chunk)
	}
	buffer := make([]byte, 32<<10)
	var written int64
	for {
		read, readErr := resp.Body.Read(buffer)
		if read > 0 {
			if written+int64(read) > responseLimit {
				return captured.Bytes(), errSystemProxyResponseTooLarge
			}
			written += int64(read)
			chunk := redactor.Push(buffer[:read], false)
			if len(chunk) > 0 {
				capture(chunk)
				if _, writeErr := c.Writer.Write(chunk); writeErr != nil {
					return captured.Bytes(), writeErr
				}
				c.Writer.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return captured.Bytes(), readErr
			}
			if tail := redactor.Push(nil, true); len(tail) > 0 {
				capture(tail)
				if _, writeErr := c.Writer.Write(tail); writeErr != nil {
					return captured.Bytes(), writeErr
				}
				c.Writer.Flush()
			}
			return captured.Bytes(), nil
		}
	}
}
