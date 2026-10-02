package app

import (
	"fmt"
	"strings"
)

// 音频价格档位的时长边界（秒）：短 ≤ 30，中 ≤ 90，更长是长档。
//
// 边界取在这两个数上，是因为前台给出的时长档位是 15/30/60/90/120/180 秒：
// 30 与 90 正好把六档劈成 2/2/2，运营配价时不必记边界，看档位名字就对得上。
const (
	audioShortMaxSeconds  = 30
	audioMediumMaxSeconds = 90
)

// audioPriceTier 把音频输出时长映射成价格档位（见 auth.AudioPriceTiers）。
//
// 时长是连续值，价目表却只能按档存，所以在这里切成短 / 中 / 长三档。分档的理由不是成本
// 差得远——ACE-Step 15 秒与 3 分钟的上游成本只差几分钱——而是一刀切等于鼓励所有人选最长
// 档：同样收 60 分，没人会选 15 秒，于是每条的实际收入被锁死在最短档的水平上。
//
// 取不到时长时返回空档，交给"不区分档位"那一行结算。这对应两种情况，运营要分别照顾：
// 一是这个音频模型本来就没有时长维度（配音、整首歌由上游定长），二是旧前端还没带上时长。
// 后者上游会按缺省值产出 60 秒，所以那一行兜底价应当按中档配，否则这批请求会少收一半。
func audioPriceTier(value any) string {
	seconds, ok := audioSecondsOption(value)
	if !ok {
		return ""
	}
	switch {
	case seconds <= audioShortMaxSeconds:
		return tierShort
	case seconds <= audioMediumMaxSeconds:
		return tierMedium
	default:
		return tierLong
	}
}

// audioSecondsOption 把能力参数里的时长解析成正整数秒。
//
// 面板传上来的可能是字符串（"60"）也可能是数字（60），还有可能是渠道配置里的浮点；
// 解析不出来一律当作"没有这个参数"，由调用方回退到空档。
func audioSecondsOption(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, typed > 0
	case int64:
		return int(typed), typed > 0
	case float64:
		return int(typed), typed > 0
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		var parsed int
		if _, err := fmt.Sscanf(trimmed, "%d", &parsed); err != nil {
			return 0, false
		}
		return parsed, parsed > 0
	default:
		return 0, false
	}
}
