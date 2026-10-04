package app

import (
	"strings"
	"testing"
)

// 系统渠道 + 按秒计费的协议：外链/内联参考视频必须被拒，平台素材放行，别的协议不误伤。
func TestSystemChannelMiniMaxRejectsNonPlatformReferenceVideos(t *testing.T) {
	config := providerConfig{ChannelID: "CHANNEL_000008", InterfaceType: "minimax-video"}
	platform := providerMedia{StorageKey: "resource:clip-a", DurationMs: 5040}

	if err := requirePlatformReferenceVideos(config, []providerMedia{platform}); err != nil {
		t.Fatalf("平台素材被拒: %v", err)
	}
	for name, media := range map[string]providerMedia{
		"外链":   {URL: "https://cdn.example.com/clip.mp4", DurationMs: 15000},
		"内联":   {DataURL: "data:video/mp4;base64,AAAA", DurationMs: 15000},
		"素材ID": {URL: "asset://voice-1"},
		"空引用":  {},
	} {
		err := requirePlatformReferenceVideos(config, []providerMedia{media})
		if err == nil || !strings.Contains(err.Error(), "素材库") {
			t.Fatalf("%s 参考视频未被拦截: %v", name, err)
		}
	}

	// 第二条可疑时也要报第二段，避免用户只改一条又被打回。
	err := requirePlatformReferenceVideos(config, []providerMedia{platform, {URL: "https://cdn.example.com/b.mp4", DurationMs: 1000}})
	if err == nil || !strings.Contains(err.Error(), "第 2 个") {
		t.Fatalf("第二条外链未被拦截: %v", err)
	}
}

// 边界只落在"系统渠道 + 按秒计费协议"这一个组合上。
func TestPlatformReferenceVideoGuardScope(t *testing.T) {
	external := providerMedia{URL: "https://cdn.example.com/clip.mp4", DurationMs: 15000}
	cases := []struct {
		name   string
		config providerConfig
	}{
		{"本地/自定义渠道", providerConfig{InterfaceType: "minimax-video"}},
		{"Seedance 按条结算", providerConfig{ChannelID: "CHANNEL_000007", InterfaceType: "newapi-channel-2"}},
		{"方舟素材库引用", providerConfig{ChannelID: "CHANNEL_000004", InterfaceType: "volcengine-ark-video"}},
	}
	for _, item := range cases {
		if err := requirePlatformReferenceVideos(item.config, []providerMedia{external}); err != nil {
			t.Fatalf("%s 被误伤: %v", item.name, err)
		}
	}
}
