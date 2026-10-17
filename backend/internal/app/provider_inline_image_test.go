package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"testing"
)

func encodeInlineTestPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("编码测试 PNG 失败: %v", err)
	}
	return buffer.Bytes()
}

// photoLikeOpaqueImage 生成接近照片的平滑渐变加细微噪点：PNG 压不动，JPEG 能明显缩小。
func photoLikeOpaqueImage(width, height int) image.Image {
	random := rand.New(rand.NewSource(7))
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			noise := random.Intn(9) - 4
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(clampByte(120 + 60*math.Sin(float64(x)/40) + float64(noise))),
				G: uint8(clampByte(110 + 50*math.Cos(float64(y)/35) + float64(noise))),
				B: uint8(clampByte(90 + 40*math.Sin(float64(x+y)/55) + float64(noise))),
				A: 0xff,
			})
		}
	}
	return img
}

func clampByte(value float64) int {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return int(value)
}

func TestCompactInlineImageDataShrinksLargeOpaquePNG(t *testing.T) {
	data := encodeInlineTestPNG(t, photoLikeOpaqueImage(1400, 1400))
	if len(data) <= inlineImageCompactThresholdBytes {
		t.Fatalf("测试图片只有 %d 字节，没有达到压缩阈值", len(data))
	}
	compacted, mimeType := compactInlineImageData(data, "image/png")
	if mimeType != "image/jpeg" {
		t.Fatalf("mimeType = %q，期望 image/jpeg", mimeType)
	}
	if len(compacted) >= len(data)/2 {
		t.Fatalf("压缩后 %d 字节，原图 %d 字节，缩小不明显", len(compacted), len(data))
	}
	if _, _, err := image.Decode(bytes.NewReader(compacted)); err != nil {
		t.Fatalf("压缩结果无法解码: %v", err)
	}
}

func TestCompactInlineImageDataKeepsTransparentPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1200, 1200))
	random := rand.New(rand.NewSource(11))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 1200; x++ {
			alpha := uint8(0xff)
			if x > 500 && x < 700 && y > 500 && y < 700 {
				alpha = 0
			}
			img.SetRGBA(x, y, color.RGBA{R: uint8(random.Intn(256)), G: uint8(random.Intn(256)), B: uint8(random.Intn(256)), A: alpha})
		}
	}
	data := encodeInlineTestPNG(t, img)
	if len(data) <= inlineImageCompactThresholdBytes {
		t.Fatalf("测试图片只有 %d 字节，没有达到压缩阈值", len(data))
	}
	compacted, mimeType := compactInlineImageData(data, "image/png")
	if mimeType != "image/png" || len(compacted) != len(data) {
		t.Fatalf("带透明通道的图片被改写了: mimeType=%q bytes=%d/%d", mimeType, len(compacted), len(data))
	}
}

func TestCompactInlineImageDataLeavesSmallImageAlone(t *testing.T) {
	data := encodeInlineTestPNG(t, photoLikeOpaqueImage(64, 64))
	compacted, mimeType := compactInlineImageData(data, "image/png")
	if mimeType != "image/png" || len(compacted) != len(data) {
		t.Fatalf("小图不应被压缩: mimeType=%q bytes=%d/%d", mimeType, len(compacted), len(data))
	}
}
