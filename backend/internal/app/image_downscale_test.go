package app

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func solidImage(width, height int, fill color.RGBA) image.Image {
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.Set(x, y, fill)
		}
	}
	return canvas
}

// 长边缩到上限以内，另一条边按同比例缩：比例错了参考图会被拉变形，
// 而参考图变形会直接毁掉生成结果。
func TestDownscaleImageKeepsAspectRatio(t *testing.T) {
	scaled := downscaleImage(testGradientImage(2400, 1350), 1280)
	if got := scaled.Bounds().Dx(); got != 1280 {
		t.Fatalf("长边应缩到 1280，实际 %d", got)
	}
	if got := scaled.Bounds().Dy(); got != 720 {
		t.Fatalf("短边应按比例得到 720，实际 %d", got)
	}
}

// 竖图的"长边"是高度：只看宽度会把竖图缩成比目标还大。
func TestDownscaleImageHandlesPortrait(t *testing.T) {
	scaled := downscaleImage(testGradientImage(900, 2000), 1280)
	if got := scaled.Bounds().Dy(); got != 1280 {
		t.Fatalf("竖图应缩高度，实际 %d", got)
	}
	if got := scaled.Bounds().Dx(); got != 576 {
		t.Fatalf("竖图宽度应按比例得到 576，实际 %d", got)
	}
}

// 本来就够小的图不做放大：放大只会让文件更大、画面更糊。
func TestDownscaleImageLeavesSmallImages(t *testing.T) {
	source := testGradientImage(640, 480)
	scaled := downscaleImage(source, 1280)
	if scaled.Bounds() != source.Bounds() {
		t.Fatalf("小图不该被改动，实际 %v", scaled.Bounds())
	}
}

// 2×2 的棋盘恰好一半白一半黑，按面积平均应当混成中灰；如果实现退化成"取左上角
// 那个像素"（最近邻），结果会是 255。
func TestDownscaleImageAveragesWithinRange(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			shade := uint8(0)
			if (x+y)%2 == 0 {
				shade = 255
			}
			source.Set(x, y, color.RGBA{R: shade, A: 255})
		}
	}
	scaled := downscaleImage(source, 2)
	average := color.NRGBAModel.Convert(scaled.At(0, 0)).(color.NRGBA)
	if average.R < 100 || average.R > 155 {
		t.Fatalf("棋盘应平均成中灰，实际 R=%d", average.R)
	}
}

// 编码出来的必须是能被浏览器直接显示的 JPEG，且体积明显小于源图。
func TestDownscaleToJPEGProducesSmallerImage(t *testing.T) {
	source := testGradientImage(2000, 1200)
	encoded, err := downscaleToJPEG(source, 1280, 88)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("产物应是合法 JPEG：%v", err)
	}
	if decoded.Bounds().Dx() != 1280 {
		t.Fatalf("产物长边应为 1280，实际 %d", decoded.Bounds().Dx())
	}
}

// 纯色图重编码后应当非常小：这条同时钉住质量参数没有被写成 100。
func TestDownscaleToJPEGQualityIsReasonable(t *testing.T) {
	encoded, err := downscaleToJPEG(solidImage(1600, 900, color.RGBA{R: 20, G: 30, B: 40, A: 255}), 1280, inspirationReferenceQuality)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 120<<10 {
		t.Fatalf("纯色图不该编出 %d 字节，质量参数可能被调成了无损", len(encoded))
	}
}
