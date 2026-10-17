package app

// 内联参考图压缩。
//
// 没有公网素材地址时，参考图会以 data URL 直接进 JSON 请求体。相机或画布导出的
// 2048×2048 PNG 单张 5–7MB，三张就能把请求体推到 20MB 以上：上传时间挤占上游
// 生成预算，慢一点的同步生图模型会在响应头回来前被判定超时。PNG 本身无损，
// 对生成模型的输入来说没必要；在不带透明通道的前提下转成高质量 JPEG，体积通常
// 只剩十分之一，画质差异在生成阶段不可见。

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"strings"
)

const (
	inlineImageCompactThresholdBytes = 1 << 20
	inlineImageJPEGQuality           = 88
)

// compactInlineImageData 只压缩"大体积、可解码、完全不透明"的图片。
// 带 alpha 的图片保持原样：JPEG 没有透明通道，把透明背景压成黑底会改变参考图语义。
func compactInlineImageData(data []byte, mimeType string) ([]byte, string) {
	if len(data) <= inlineImageCompactThresholdBytes {
		return data, mimeType
	}
	normalized := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if !strings.HasPrefix(normalized, "image/") || normalized == "image/jpeg" || normalized == "image/gif" {
		return data, mimeType
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || !imageFullyOpaque(decoded) {
		return data, mimeType
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, decoded, &jpeg.Options{Quality: inlineImageJPEGQuality}); err != nil {
		return data, mimeType
	}
	if encoded.Len() >= len(data) {
		return data, mimeType
	}
	return encoded.Bytes(), "image/jpeg"
}

// imageFullyOpaque 判断图片是否每个像素都是不透明的。
// 常见解码结果走像素切片快路径，避免 2048×2048 图片逐点调用 At。
func imageFullyOpaque(img image.Image) bool {
	switch typed := img.(type) {
	case *image.NRGBA:
		for index := 3; index < len(typed.Pix); index += 4 {
			if typed.Pix[index] != 0xff {
				return false
			}
		}
		return true
	case *image.RGBA:
		for index := 3; index < len(typed.Pix); index += 4 {
			if typed.Pix[index] != 0xff {
				return false
			}
		}
		return true
	case *image.Gray, *image.Gray16, *image.YCbCr, *image.CMYK:
		return true
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, alpha := img.At(x, y).RGBA(); alpha != 0xffff {
				return false
			}
		}
	}
	return true
}
