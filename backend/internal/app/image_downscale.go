package app

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"math"
)

// 这一组常量决定参考图落库后的形态。
//
// 上游的参考图是原始产出图，单张 3MB 上下（一张 2000px 的 PNG）。原样存下来的话，
// 全池一百多张就是三四百兆，而"使用这个创意"时前端还要把它下载下来再传进用户自己的
// 资源库——用户那边的流量直接翻倍。生成侧最终喂给模型的也就是 1080p 上下，1280px
// 的 JPEG 完全不损失可用信息。
const (
	inspirationReferenceMaxEdge = 1280
	inspirationReferenceQuality = 88
)

// downscaleToJPEG 把图片按长边上限缩放并编码成 JPEG。
//
// 自己写而不引 golang.org/x/image：这个仓库目前的依赖只有 crypto/net/sync/sys/text，
// 为了一个降采样把 x/image 拉进构建，代价比这段代码大。
//
// 只用 box（面积平均）滤波，不做更花的插值：这里永远是把大图缩小，缩小场景下面积平均
// 不会产生振铃，而双三次在这件事上并不会更好看。
func downscaleToJPEG(source image.Image, maxEdge int, quality int) ([]byte, error) {
	scaled := downscaleImage(source, maxEdge)
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, scaled, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// downscaleImage 把长边缩到 maxEdge 以内；本来就够小就原样返回（不做放大）。
func downscaleImage(source image.Image, maxEdge int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || maxEdge <= 0 {
		return source
	}
	longest := width
	if height > longest {
		longest = height
	}
	if longest <= maxEdge {
		return source
	}
	scale := float64(maxEdge) / float64(longest)
	targetWidth := max(1, int(math.Round(float64(width)*scale)))
	targetHeight := max(1, int(math.Round(float64(height)*scale)))

	pixels := toNRGBA(source)
	target := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		// 每个目标像素覆盖源图上的一段区间，取区间内所有像素的平均值。区间至少一个
		// 像素宽，避免缩得比目标还小时出现空区间（那会除零）。
		top := y * height / targetHeight
		bottom := (y + 1) * height / targetHeight
		if bottom <= top {
			bottom = top + 1
		}
		for x := 0; x < targetWidth; x++ {
			left := x * width / targetWidth
			right := (x + 1) * width / targetWidth
			if right <= left {
				right = left + 1
			}
			var red, green, blue, alpha, count uint64
			for sy := top; sy < bottom; sy++ {
				row := sy * pixels.Stride
				for sx := left; sx < right; sx++ {
					offset := row + sx*4
					red += uint64(pixels.Pix[offset])
					green += uint64(pixels.Pix[offset+1])
					blue += uint64(pixels.Pix[offset+2])
					alpha += uint64(pixels.Pix[offset+3])
					count++
				}
			}
			offset := y*target.Stride + x*4
			target.Pix[offset] = uint8(red / count)
			target.Pix[offset+1] = uint8(green / count)
			target.Pix[offset+2] = uint8(blue / count)
			target.Pix[offset+3] = uint8(alpha / count)
		}
	}
	return target
}

// toNRGBA 归一成 NRGBA，让上面的取值可以直接下标访问 Pix。
//
// 直接对 image.Image 调 At() 是接口调用加颜色转换，一张 2000×2000 的图要走上千万次；
// 先整体转一次，后面就是纯切片运算。转换本身走 stdlib 的绘制快路径。
func toNRGBA(source image.Image) *image.NRGBA {
	if nrgba, ok := source.(*image.NRGBA); ok {
		return nrgba
	}
	bounds := source.Bounds()
	target := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(target, target.Bounds(), source, bounds.Min, draw.Src)
	return target
}
