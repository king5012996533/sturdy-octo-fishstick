package app

import (
	"fmt"
	"strings"
)

// 按「输入 + 输出」总秒数计费的视频协议，参考视频必须是平台素材库里的资源。
//
// 扣费只认服务端资源表的时长（见 task_credit_reference_video.go）：外链、内联 data URL 和
// asset:// 素材都拿不到可核对的秒数，只能按 0 秒计，而上游照样按视频真实时长结算。客户端
// 自报的 durationMs 又能随手改小，于是"贴一条 15 秒的外链视频"就能把 20 秒的结算压成只收
// 5 秒，差额全部由平台垫。这里的边界与计费口径一一对应：计费认不出的素材，一律不收。
//
// 只约束系统渠道（channelId 非空）。本地与用户自带 Key 的自定义渠道不扣平台积分，也不走
// 这条计费口径；Seedance 这类按条结算的协议更不受影响——它允许 asset:// 素材引用。
func requirePlatformReferenceVideos(config providerConfig, videos []providerMedia) error {
	if strings.TrimSpace(config.ChannelID) == "" || !billsReferenceVideoSeconds(config.InterfaceType) {
		return nil
	}
	for index, media := range videos {
		if !strings.HasPrefix(strings.TrimSpace(media.StorageKey), "resource:") {
			return BadAuthRequest(fmt.Sprintf("第 %d 个参考视频需要先上传到素材库再提交：当前模型按视频时长计费，只支持平台素材", index+1))
		}
	}
	return nil
}
