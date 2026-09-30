package app

import (
	"context"
	"encoding/base64"
	"testing"

	"infinite-canvas/backend/internal/protocol"
)

// 真实 Seedance/Ark mp4 产物头：ftyp isom ... mp4。
func mp4Fixture() []byte {
	return []byte("\x00\x00\x00 ftypisom\x00\x00\x02\x00isomiso2avc1mp41")
}

func TestNormalizedMediaMimeTypeSniffsTransportPlaceholder(t *testing.T) {
	mp4 := mp4Fixture()
	for _, declared := range []string{"", "application/octet-stream", "binary/octet-stream", "Binary/Octet-Stream; charset=binary"} {
		if got := normalizedMediaMimeType(declared, mp4); got != "video/mp4" {
			t.Fatalf("declared %q: got %q, want video/mp4", declared, got)
		}
	}
}

func TestNormalizedMediaMimeTypeKeepsDeclaredMediaType(t *testing.T) {
	if got := normalizedMediaMimeType("video/webm", mp4Fixture()); got != "video/webm" {
		t.Fatalf("具体类型被改写：%q", got)
	}
	if got := normalizedMediaMimeType("binary/octet-stream", []byte{0x00, 0x01, 0x02, 0x03}); got != "application/octet-stream" {
		t.Fatalf("无法嗅探时必须退化为诚实的未知类型，得到 %q", got)
	}
}

func TestProtocolMediaBytesOnceNormalizesPlaceholderDataURL(t *testing.T) {
	dataURL := "data:binary/octet-stream;base64," + base64.StdEncoding.EncodeToString(mp4Fixture())
	data, mimeType, err := protocolMediaBytesOnce(context.Background(), providerConfig{}, protocol.MediaReference{DataURL: dataURL})
	if err != nil {
		t.Fatalf("解析内联结果失败：%v", err)
	}
	if mimeType != "video/mp4" {
		t.Fatalf("内联结果 MIME 未收敛：%q", mimeType)
	}
	if len(data) != len(mp4Fixture()) {
		t.Fatalf("内联结果字节数不符：%d", len(data))
	}
}
