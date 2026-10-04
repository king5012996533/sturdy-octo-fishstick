package app

import (
	"fmt"
	"strconv"
	"strings"
)

// 图片尺寸档的像素边界：1K ≤ 200 万像素，2K ≤ 500 万像素，更大是 4K。
//
// 边界取这两个整数，是因为前台的可选尺寸在这两处有很宽的空档：1K 组最大 1824×1024
// （187 万像素），2K 组从 2048×2048（419 万像素）起；4K 组最小 2336×3504（818 万像素）。
// 边界落在空档里，运营换尺寸档位时不必重算边界。
//
// 分档的理由不是成本差得远——上游 1K 与 2K 是同一档价（15,000 tokens），只有 4K 贵一档
// （20,000 tokens）——而是一刀切等于鼓励所有人选最大尺寸：同样收 75 分，没人会挑 1K。
// 与音频按分钟分档同源，见 audio_price_tier.go。
const (
	imageSize1KMaxPixels = 2_000_000
	imageSize2KMaxPixels = 5_000_000
)

// imageSizePriceTier 把请求尺寸映射成价格档位（见 auth.ImageSizePriceTiers）。
//
// 只认 `宽x高` 这种上游能直接执行的形式：比例串（1:1）、auto、空值都返回空档，交给
// "不区分档位"那一行结算。这些请求的最终面积由模型自己决定，我们按面积分档的前提不成立，
// 猜一个档位只会把成本压在猜错的那一侧。
func imageSizePriceTier(value any) string {
	width, height, ok := imageSizePixels(value)
	if !ok {
		return ""
	}
	switch pixels := width * height; {
	case pixels <= imageSize1KMaxPixels:
		return tierSize1K
	case pixels <= imageSize2KMaxPixels:
		return tierSize2K
	default:
		return tierSize4K
	}
}

// imageSizePixels 解析 `宽x高`，大小写与空格容错，其余一律视为没给尺寸。
func imageSizePixels(value any) (int64, int64, bool) {
	text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	if text == "" || text == "auto" {
		return 0, 0, false
	}
	parts := strings.SplitN(text, "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, widthErr := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	height, heightErr := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}
