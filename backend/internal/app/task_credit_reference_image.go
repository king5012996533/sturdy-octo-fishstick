package app

import (
	"fmt"
	"strconv"
	"strings"
)

// 参考图超出免费额度后按张加收。
//
// 只对 MiniMax 原生结算的协议生效（秘塔 H3）：上游前 5 张参考图免费，第 6 张起 ¥0.05/张，
// 我们按张收 15 分（¥0.15），与"跟上游同步再在成本上加价"的整条口径一致。
//
// 这一笔与秒数无关，而 H3 的计价单位是秒，所以它走计费端口上独立的"附加费"，不折算成秒：
// 折算在 15 分/秒的 768P 档上刚好是 1 秒，到 25 分/秒的 2K 档就会变成 25 分/张——同一件事
// 在两个档位上两个价，而且账面上看不出为什么。
const (
	freeReferenceImages        = 5
	extraReferenceImageCredits = 15
)

// referenceImageSurcharge 汇总本次提交超出免费额度的参考图加收，并给出写进流水的理由。
//
// 张数以提交的参考图数组为准（图片、视频任务共用同一个数组）。试算端在素材还没准备好时
// 拿不到数组本体，只能带一个张数摘要，所以这里允许退回 referenceImageCount——那条路只在
// "还没提交"的报价里生效，提交时数组一定在，客户端改不动实扣金额。
func (s *Service) referenceImageSurcharge(input map[string]any) (int64, string) {
	if !billsReferenceVideoSeconds(inputInterfaceType(input)) {
		return 0, ""
	}
	count := referenceImageCountOf(input)
	if count <= freeReferenceImages {
		return 0, ""
	}
	extra := count - freeReferenceImages
	return int64(extra) * extraReferenceImageCredits, fmt.Sprintf("参考图 %d 张，超出免费的 %d 张按 %d 分/张加收", count, extra, extraReferenceImageCredits)
}

// referenceImageCountOf 读本次提交的参考图张数。
//
// 数组优先于摘要：摘要是客户端自报的，只在数组缺席（试算）时用来报价。
func referenceImageCountOf(input map[string]any) int {
	if list, ok := input["referenceImages"].([]any); ok {
		return len(list)
	}
	switch value := input["referenceImageCount"].(type) {
	case float64:
		return clampNonNegativeInt(value)
	case int:
		if value > 0 {
			return value
		}
	case int64:
		return clampNonNegativeInt(float64(value))
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return clampNonNegativeInt(float64(parsed))
		}
	}
	return 0
}

func clampNonNegativeInt(value float64) int {
	if value <= 0 {
		return 0
	}
	return int(value)
}
