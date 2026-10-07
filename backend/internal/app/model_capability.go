package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"infinite-canvas/backend/internal/model"
)

func normalizeCapability(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "text", "image", "video", "audio":
		return normalized
	default:
		return ""
	}
}

func capabilityFromTaskType(taskType string) string {
	value := strings.ToLower(taskType)
	for _, capability := range []string{"video", "image", "audio", "text"} {
		if strings.Contains(value, capability) {
			return capability
		}
	}
	if strings.Contains(value, "storyboard") || strings.Contains(value, "agent") {
		return "text"
	}
	return ""
}

// ModelCapabilityConfig 是模型能力声明，不包含供应商字段名；协议适配器负责把统一参数映射到上游请求。
type ModelCapabilityConfig struct {
	Version int                    `json:"version"`
	Text    *TextCapabilityConfig  `json:"text,omitempty"`
	Image   *ImageCapabilityConfig `json:"image,omitempty"`
	Video   *VideoCapabilityConfig `json:"video,omitempty"`
}

type TextCapabilityConfig struct {
	// Streaming controls whether this model accepts upstream SSE text responses.
	// A nil value is treated as true for backwards compatibility with older configs.
	Streaming  *bool               `json:"streaming,omitempty"`
	References TextReferenceConfig `json:"references"`
}

type TextReferenceConfig struct {
	PromptMaxChars int   `json:"promptMaxChars"`
	MaxImages      int   `json:"maxImages"`
	MaxImageBytes  int64 `json:"maxImageBytes"`
	MaxVideos      int   `json:"maxVideos"`
	MaxVideoBytes  int64 `json:"maxVideoBytes"`
}

type ImageCapabilityConfig struct {
	References            ImageReferenceConfig `json:"references"`
	Size                  ImageSizeConfig      `json:"size"`
	Quality               ImageQualityConfig   `json:"quality"`
	TransparentBackground VideoBooleanConfig   `json:"transparentBackground"`
	ResponseFormat        ParameterSupport     `json:"responseFormat"`
	OutputFormat          ParameterSupport     `json:"outputFormat"`
	MaxOutputs            int                  `json:"maxOutputs"`
}

type ImageReferenceConfig struct {
	PromptMaxChars int   `json:"promptMaxChars"`
	MaxImages      int   `json:"maxImages"`
	MaxImageBytes  int64 `json:"maxImageBytes"`
	MaskSupported  bool  `json:"maskSupported"`
}

type ImageSizeConfig struct {
	Parameter   string            `json:"parameter"`
	Values      []string          `json:"values"`
	Default     string            `json:"default"`
	AllowCustom bool              `json:"allowCustom"`
	Presets     []ImageSizePreset `json:"presets,omitempty"`
}

type ImageSizePreset struct {
	Tier   string `json:"tier"`
	Ratio  string `json:"ratio"`
	Size   string `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type ImageQualityConfig struct {
	Supported bool     `json:"supported"`
	Values    []string `json:"values"`
	Default   string   `json:"default"`
}

type ParameterSupport struct {
	Supported bool `json:"supported"`
}

type VideoCapabilityConfig struct {
	References VideoReferenceConfig `json:"references"`
	Duration   VideoDurationConfig  `json:"duration"`
	// DurationByResolution 给"不同分辨率档位有不同时长上限"的模型留出口子。
	// 单一 duration 表达不了这种形态：要么把 15 秒露给最长只到 12 秒的档位（用户选了
	// 15 秒却拿到 12 秒的成片），要么为了 720p 把 480p 的 15 秒一起砍掉。键取
	// resolutions 里的规范写法（如 "720p"），未登记的分辨率回落到上面的 duration。
	DurationByResolution map[string]VideoDurationConfig `json:"durationByResolution,omitempty"`
	DurationSupported    *bool                          `json:"durationSupported,omitempty"`
	Ratios               []string                       `json:"ratios"`
	DefaultRatio         string                         `json:"defaultRatio"`
	Resolutions          []string                       `json:"resolutions"`
	DefaultResolution    string                         `json:"defaultResolution"`
	GenerateAudio        VideoBooleanConfig             `json:"generateAudio"`
	Watermark            VideoBooleanConfig             `json:"watermark"`
	Operations           []string                       `json:"operations"`
	DefaultOperation     string                         `json:"defaultOperation"`
}

type VideoReferenceConfig struct {
	PromptMaxChars        int     `json:"promptMaxChars"`
	MinImages             int     `json:"minImages"`
	MaxImages             int     `json:"maxImages"`
	MaxImageBytes         int64   `json:"maxImageBytes"`
	MinImageWidth         int     `json:"minImageWidth,omitempty"`
	MaxImageWidth         int     `json:"maxImageWidth,omitempty"`
	MinImageHeight        int     `json:"minImageHeight,omitempty"`
	MaxImageHeight        int     `json:"maxImageHeight,omitempty"`
	MinImageAspect        float64 `json:"minImageAspect,omitempty"`
	MaxImageAspect        float64 `json:"maxImageAspect,omitempty"`
	MinImagePixels        int64   `json:"minImagePixels,omitempty"`
	MaxImagePixels        int64   `json:"maxImagePixels,omitempty"`
	MaxVideos             int     `json:"maxVideos"`
	MaxVideoBytes         int64   `json:"maxVideoBytes"`
	MaxVideoDuration      int     `json:"maxVideoDurationSeconds"`
	MinVideoDuration      int     `json:"minVideoDurationSeconds,omitempty"`
	MaxVideoTotalDuration int     `json:"maxVideoTotalDurationSeconds,omitempty"`
	MinVideoWidth         int     `json:"minVideoWidth,omitempty"`
	MaxVideoWidth         int     `json:"maxVideoWidth,omitempty"`
	MinVideoHeight        int     `json:"minVideoHeight,omitempty"`
	MaxVideoHeight        int     `json:"maxVideoHeight,omitempty"`
	MinVideoAspect        float64 `json:"minVideoAspect,omitempty"`
	MaxVideoAspect        float64 `json:"maxVideoAspect,omitempty"`
	MinVideoPixels        int64   `json:"minVideoPixels,omitempty"`
	MaxVideoPixels        int64   `json:"maxVideoPixels,omitempty"`
	MaxAudios             int     `json:"maxAudios"`
	MaxAudioBytes         int64   `json:"maxAudioBytes"`
	MaxAudioDuration      int     `json:"maxAudioDurationSeconds"`
	MinAudioDuration      float64 `json:"minAudioDurationSeconds,omitempty"`
	MaxAudioTotalDuration int     `json:"maxAudioTotalDurationSeconds,omitempty"`
}

// DefaultVideoPromptMaxChars 是普通视频模型提示词字符数的默认上限。
// 视频提示词由输入框文本、连线内容和技能上下文合成，远长于用户手输内容；
// 默认值过小会把画布工作流正常可用的提示词拦在本地。管理员仍可按模型覆盖。
const DefaultVideoPromptMaxChars = 8000

type VideoDurationConfig struct {
	Selection string `json:"selection"`
	Min       int    `json:"min,omitempty"`
	Max       int    `json:"max,omitempty"`
	Step      int    `json:"step,omitempty"`
	Values    []int  `json:"values,omitempty"`
	Default   int    `json:"default"`
}

type VideoBooleanConfig struct {
	Supported bool `json:"supported"`
	Default   bool `json:"default"`
}

func DefaultModelCapabilityConfig(protocol string) *ModelCapabilityConfig {
	return DefaultModelCapabilityConfigForModel(protocol, "")
}

func videoDurationSupported(value *VideoCapabilityConfig) bool {
	return value == nil || value.DurationSupported == nil || *value.DurationSupported
}

func DefaultImageCapabilityConfig(protocol string, modelName string) *ImageCapabilityConfig {
	image := &ImageCapabilityConfig{
		References:            ImageReferenceConfig{PromptMaxChars: 32000, MaxImages: 16, MaxImageBytes: 30 * 1024 * 1024, MaskSupported: true},
		Size:                  ImageSizeConfig{Parameter: "size", Values: defaultImageSizeValues(), Default: "1:1", AllowCustom: true},
		Quality:               ImageQualityConfig{Supported: true, Values: []string{"auto", "low", "medium", "high"}, Default: "auto"},
		TransparentBackground: VideoBooleanConfig{Supported: true, Default: false},
		ResponseFormat:        ParameterSupport{Supported: true},
		OutputFormat:          ParameterSupport{Supported: true},
		MaxOutputs:            15,
	}
	// Replicate 是"一模型一 schema"的托管平台，能力合同必须逐模型对齐上游，不能套通用默认值。
	if model.ChannelInterfaceType(protocol) == model.ChannelInterfaceReplicatePredictionImage {
		applyReplicateImageCapability(image, modelName)
		return image
	}
	switch model.ChannelInterfaceType(protocol) {
	case model.ChannelInterfaceGrokImage:
		image.References.MaxImages = 1
		image.References.MaskSupported = false
		// grok2api / xAI Imagine：size→aspect_ratio，quality→resolution(1k/2k)。
		image.Size = ImageSizeConfig{Parameter: "aspect_ratio", Values: []string{"1:1", "3:4", "4:3", "9:16", "16:9", "2:3", "3:2"}, Default: "1:1", AllowCustom: false}
		image.Quality = ImageQualityConfig{Supported: true, Values: []string{"1k", "2k"}, Default: "2k"}
		image.TransparentBackground = VideoBooleanConfig{Supported: false, Default: false}
		image.ResponseFormat = ParameterSupport{Supported: true}
		image.OutputFormat = ParameterSupport{Supported: false}
		image.MaxOutputs = 1
	case model.ChannelInterfaceVolcengineArkImage, model.ChannelInterfaceVolcengineArkAgentPlanImage:
		image.References.MaskSupported = false
		image.Quality.Supported = false
		image.TransparentBackground.Supported = false
		image.ResponseFormat.Supported = false
		image.OutputFormat.Supported = false
	case model.ChannelInterfaceVolcengineJiMengImage:
		image.References.MaxImages = 14
		image.References.MaskSupported = false
		image.Quality.Supported = false
		image.TransparentBackground.Supported = false
		image.ResponseFormat.Supported = false
		image.OutputFormat.Supported = false
	case model.ChannelInterfaceGeminiImage:
		image.References.MaskSupported = false
		// Gemini Images uses imageConfig.aspectRatio, not the OpenAI-style pixel size field.
		image.Size = ImageSizeConfig{Parameter: "aspect_ratio", Values: []string{"auto", "1:1", "2:3", "3:2", "3:4", "4:3", "9:16", "16:9", "21:9"}, Default: "1:1", AllowCustom: false}
		image.TransparentBackground.Supported = false
		image.ResponseFormat.Supported = false
		image.OutputFormat.Supported = false
		image.MaxOutputs = 4
	}
	if model.ChannelInterfaceType(protocol) != model.ChannelInterfaceGrokImage && strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelName)), "grok-imagine-image") {
		image.References.MaxImages = 0
		image.References.MaskSupported = false
		image.Size = ImageSizeConfig{Parameter: "aspect_ratio", Values: []string{"1:1", "3:4", "4:3", "9:16", "16:9", "2:3", "3:2"}, Default: "1:1", AllowCustom: false}
		image.Quality = ImageQualityConfig{Supported: true, Values: []string{"1k", "2k"}, Default: "2k"}
		image.TransparentBackground = VideoBooleanConfig{Supported: false, Default: false}
		image.ResponseFormat = ParameterSupport{Supported: true}
		image.OutputFormat = ParameterSupport{Supported: false}
		image.MaxOutputs = 1
	}
	return image
}

// replicateImageRatioTiers 是 Replicate 图片模型唯一可用的分辨率档位写法。
//
// 上游不接受像素尺寸，只接受档位字符串（imagen 的 1K/2K、seedream 的 1K/2K/4K）；
// 没有分辨率参数的模型用单档 1k 表达"比例可配、分辨率由模型决定"，前端据此渲染
// 比例网格与档位切换，不会再出现"除生成数量外没有可选项"的空面板。
var replicateImageRatioTiers = []string{"1k"}

// Replicate 图片模型的比例枚举直接取自上游 schema；每个模型的取值并不相同。
var (
	replicateFluxRatios      = []string{"1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4", "4:5", "5:4", "21:9"}
	replicateGPTImageRatios  = []string{"1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4"}
	replicateKontextRatios   = []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "4:5", "5:4", "21:9", "2:1", "1:2"}
	replicateKleinRatios     = []string{"1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4", "5:4", "4:5", "21:9"}
	replicateNanoBananaRatio = []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"}
	replicateImagenRatios    = []string{"1:1", "9:16", "16:9", "3:4", "4:3"}
	replicateSeedream4Ratio  = []string{"1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "21:9"}
	replicateSeedream3Ratio  = []string{"1:1", "3:4", "4:3", "16:9", "9:16", "2:3", "3:2", "21:9"}
	replicateMinimaxRatios   = []string{"1:1", "16:9", "4:3", "3:2", "2:3", "3:4", "9:16", "21:9"}
	replicatePhotonRatios    = []string{"1:1", "3:4", "4:3", "9:16", "16:9", "21:9"}
	replicateIdeogramRatios  = []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "4:5", "5:4", "2:1", "1:2", "3:1", "1:3"}
	replicateBriaRatios      = []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9"}
)

// applyReplicateImageCapability 按上游真实输入 schema 推导图片能力合同。
//
// 每个模型的宽高比枚举、输出数量、参考图数量都不同，通用默认值会把不支持的参数
// 下发给上游（flux 没有 size、imagen 没有 num_outputs），也会让前端渲染不出比例选项。
// 输出数量与参考图数量必须与插件真实映射的字段一致：多报会让用户拿到比承诺更少的图，
// 因此未登记的模型一律回落到"单张、无参考图"的保守形态。
func applyReplicateImageCapability(image *ImageCapabilityConfig, modelName string) {
	if image == nil {
		return
	}
	owner, base := splitCatalogModelName(strings.ToLower(strings.TrimSpace(modelName)))

	// Replicate 图片模型统一只接受比例字符串，且不支持透明底、b64 响应与质量枚举。
	image.Size.Parameter = "aspect_ratio"
	image.Size.AllowCustom = false
	image.References.MaskSupported = false
	image.TransparentBackground = VideoBooleanConfig{Supported: false, Default: false}
	image.ResponseFormat = ParameterSupport{Supported: false}
	image.OutputFormat = ParameterSupport{Supported: false}

	apply := func(ratios []string, tiers []string, maxOutputs int, maxImages int, defaultTier string) {
		image.Size.Values = append([]string(nil), ratios...)
		image.Size.Default = ratios[0]
		image.Quality = ImageQualityConfig{Supported: true, Values: append([]string(nil), tiers...), Default: defaultTier}
		image.MaxOutputs = maxOutputs
		image.References.MaxImages = maxImages
	}

	switch {
	case owner == "openai" && strings.HasPrefix(base, "gpt-image-2.5"):
		// gpt-image-2.5（sunburst/flare）：比例只走 aspect_ratio，档位直通上游 quality，
		// 参考图走 input_images 编辑，单次最多 10 张输出（number_of_images），没有分辨率与
		// 透明底参数。上游 quality 五档齐备，价目也按五档各占一行，low 与 max 相差 40 倍。
		apply(replicateGPTImageRatios, []string{"low", "medium", "high", "xhigh", "max"}, 10, 4, "low")
	case owner == "openai" && strings.HasPrefix(base, "gpt-image"):
		// gpt-image-2.0：上游 quality 只到 high，没有 xhigh/max。这两档不能跟着前缀一起放开，
		// 否则会把上游不认的参数下发出去（面板露出一个选了就报错的档位）。
		apply(replicateGPTImageRatios, []string{"low", "medium", "high"}, 10, 4, "low")
	case strings.HasPrefix(base, "flux-2-"):
		// flux-2 klein：参考图数组 + match_input_image，单张输出。
		apply(replicateKleinRatios, replicateImageRatioTiers, 1, 4, "1k")
	case strings.HasPrefix(base, "flux-kontext-"):
		// kontext：单张源图编辑，支持 2:1/1:2 等极端画幅。
		apply(replicateKontextRatios, replicateImageRatioTiers, 1, 1, "1k")
	case base == "flux-schnell":
		// flux-schnell：num_outputs 最多 4 张，纯文生图、不接受参考图。
		apply(replicateFluxRatios, replicateImageRatioTiers, 4, 0, "1k")
	case base == "flux-dev":
		apply(replicateFluxRatios, replicateImageRatioTiers, 4, 1, "1k")
	case base == "flux-1.1-pro":
		apply([]string{"1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4", "4:5", "5:4"}, replicateImageRatioTiers, 1, 1, "1k")
	case strings.HasPrefix(base, "flux-") || base == "flux-fast" || strings.HasPrefix(base, "hunyuan-image-"):
		// 同 schema 的 flux 系变体（prunaai/flux-fast、腾讯混元图）：比例一致但没有输出数量参数。
		apply(replicateFluxRatios, replicateImageRatioTiers, 1, 0, "1k")
	case owner == "google" && strings.HasPrefix(base, "nano-banana"):
		apply(replicateNanoBananaRatio, replicateImageRatioTiers, 1, 4, "1k")
	case owner == "google" && (base == "imagen-4" || base == "imagen-4-ultra"):
		apply(replicateImagenRatios, []string{"1k", "2k"}, 1, 0, "1k")
	case owner == "google" && strings.HasPrefix(base, "imagen-"):
		// imagen-4-fast / imagen-3 系列没有分辨率参数，只能选比例。
		apply(replicateImagenRatios, replicateImageRatioTiers, 1, 0, "1k")
	case base == "seedream-4":
		apply(replicateSeedream4Ratio, []string{"1k", "2k", "4k"}, 10, 10, "2k")
	case base == "seedream-3":
		apply(replicateSeedream3Ratio, replicateImageRatioTiers, 1, 0, "1k")
	case owner == "minimax" && strings.HasPrefix(base, "image-01"):
		apply(replicateMinimaxRatios, replicateImageRatioTiers, 9, 1, "1k")
	case owner == "luma" && strings.HasPrefix(base, "photon"):
		apply(replicatePhotonRatios, replicateImageRatioTiers, 1, 1, "1k")
	case owner == "ideogram-ai":
		apply(replicateIdeogramRatios, replicateImageRatioTiers, 1, 1, "1k")
	case owner == "bria":
		apply(replicateBriaRatios, replicateImageRatioTiers, 1, 0, "1k")
	default:
		apply(replicateFluxRatios, replicateImageRatioTiers, 1, 0, "1k")
	}
}

// Replicate 视频模型的比例枚举同样直接取自上游 schema。
var (
	replicateVeoRatios      = []string{"16:9", "9:16"}
	replicateKlingRatios    = []string{"16:9", "9:16", "1:1"}
	replicateSeedanceRatios = []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}
	replicateWanRatios      = []string{"16:9", "9:16"}
	replicateWanTiers       = []string{"480p", "720p", "1080p"}
)

// applyReplicateVideoCapability 按上游真实输入 schema 推导视频能力合同。
//
// 每个模型的时长档位、分辨率枚举与首尾帧字段都不同：kling v2.1 只有图生视频（首帧必填）、
// veo 只有 4/6/8 秒、hailuo 没有比例参数、wan 用像素尺寸而不是比例。通用默认值
// （1-15 秒、480p-2160p、9 张参考图）会让前端渲染出上游不认的选项，用户点下去才报错；
// 因此这里只开放插件真实映射过的字段，未登记的模型回落到最小可用形态。
func applyReplicateVideoCapability(video *VideoCapabilityConfig, modelName string) {
	if video == nil {
		return
	}
	owner, base := splitCatalogModelName(strings.ToLower(strings.TrimSpace(modelName)))

	// Replicate 视频模型没有音频/水印开关，也不吃参考视频与参考音频。
	video.GenerateAudio = VideoBooleanConfig{Supported: false, Default: false}
	video.Watermark = VideoBooleanConfig{Supported: false, Default: false}
	video.References.MaxVideos, video.References.MaxVideoBytes, video.References.MaxVideoDuration = 0, 0, 0
	video.References.MaxAudios, video.References.MaxAudioBytes, video.References.MaxAudioDuration = 0, 0, 0
	video.References.MaxImageBytes = 30 * 1024 * 1024
	video.References.MaxImages = 1
	video.Operations = []string{"text_to_video", "image_to_video"}
	video.DefaultOperation = "text_to_video"
	video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{5}, Default: 5}
	// 用空切片而不是 nil：nil 序列化成 null，前端按数组消费会直接抛错。
	video.Ratios, video.DefaultRatio = []string{}, ""
	video.Resolutions, video.DefaultResolution = []string{}, ""

	// 默认时长取上游 schema 的 default，不取枚举末位：末位通常是最贵、最慢的一档。
	setDuration := func(fallback int, values ...int) {
		video.Duration = VideoDurationConfig{Selection: "enum", Values: values, Default: fallback}
	}
	setDurationRange := func(minimum, maximum, step, fallback int) {
		video.Duration = VideoDurationConfig{Selection: "range", Min: minimum, Max: maximum, Step: step, Default: fallback}
	}
	setRatios := func(values []string, fallback string) {
		video.Ratios, video.DefaultRatio = values, fallback
	}
	setResolutions := func(values []string, fallback string) {
		video.Resolutions, video.DefaultResolution = values, fallback
	}

	switch {
	case owner == "google" && strings.HasPrefix(base, "veo-3"):
		// veo-3 / veo-3-fast：唯一支持生成音频的一族。
		setDuration(8, 4, 6, 8)
		setRatios(replicateVeoRatios, "16:9")
		setResolutions([]string{"720p", "1080p"}, "1080p")
		video.GenerateAudio = VideoBooleanConfig{Supported: true, Default: true}
	case owner == "google" && strings.HasPrefix(base, "veo-2"):
		setDuration(5, 5, 6, 7, 8)
		setRatios(replicateVeoRatios, "16:9")
	case owner == "kwaivgi" && strings.HasPrefix(base, "kling-v2.5-turbo-pro"):
		// kling-v2.1 已被上游停用，2.5 turbo pro 是在售替代：文生与图生都支持，首尾帧字段齐全。
		setDuration(5, 5, 10)
		setRatios(replicateKlingRatios, "16:9")
		video.References.MaxImages = 2
	case owner == "kwaivgi" && strings.HasPrefix(base, "kling-v2.6"):
		setDuration(5, 5, 10)
		setRatios(replicateKlingRatios, "16:9")
		// 2.6 只有 start_image，没有 end_image：多给一张尾帧会被上游直接拒绝。
		video.References.MaxImages = 1
	case owner == "kwaivgi" && strings.HasPrefix(base, "kling-v2.1-master"):
		setDuration(5, 5, 10)
		setRatios(replicateKlingRatios, "16:9")
	case owner == "kwaivgi" && strings.HasPrefix(base, "kling"):
		// kling v2.1 / v1.6：start_image 是必填输入，只能图生视频。
		// 尾帧（end_image）只在该模型的 pro 档开放，而档位不由平台控制，因此这里只收一张首帧：
		// 多收一张尾帧会让上游直接以 "end_image requires mode 'pro'" 失败。
		setDuration(5, 5, 10)
		video.Operations = []string{"image_to_video"}
		video.DefaultOperation = "image_to_video"
		video.References.MinImages = 1
		video.References.MaxImages = 1
	case owner == "bytedance" && strings.Contains(base, "seedance"):
		// seedance 用比例 + 分辨率两个参数，且首尾帧都走独立字段。
		setRatios(replicateSeedanceRatios, "16:9")
		video.References.MaxImages = 2
		if strings.Contains(base, "lite") {
			// lite 还接受一组风格参考图，最多 4 张。
			setDurationRange(4, 12, 1, 5)
			setResolutions([]string{"480p", "720p", "1080p"}, "720p")
			video.Operations = []string{"text_to_video", "image_to_video", "reference_to_video"}
			video.References.MaxImages = 4
		} else {
			setDurationRange(2, 12, 1, 5)
			setResolutions([]string{"480p", "720p", "1080p"}, "1080p")
		}
	case owner == "minimax" && strings.HasPrefix(base, "hailuo"):
		// hailuo 没有比例参数：画幅由首帧图决定，纯文本生成走模型默认。
		setDuration(6, 6, 10)
		setResolutions([]string{"512p", "768p", "1080p"}, "1080p")
		video.References.MaxImages = 2
	case owner == "wan-video" && strings.Contains(base, "wan"):
		setDuration(5, 5, 10)
		setRatios(replicateWanRatios, "16:9")
		setResolutions(replicateWanTiers, "720p")
		// wan-2.5-t2v 是纯文生视频，插件不会把参考图下发到上游。
		video.References.MaxImages = 0
	case owner == "pixverse":
		setDuration(5, 5, 8)
		setRatios(replicateKlingRatios, "16:9")
		setResolutions([]string{"360p", "540p", "720p", "1080p"}, "540p")
		video.References.MaxImages = 2
	default:
		// 未登记族：不开放比例、分辨率与参考图，避免把上游不认的参数写进请求。
		video.References.MaxImages = 0
		video.Operations = []string{"text_to_video"}
		video.DefaultOperation = "text_to_video"
	}
}

// splitCatalogModelName 把 owner/name 形式的模型标识拆成两段；没有斜杠时 owner 为空。
func splitCatalogModelName(value string) (string, string) {
	if index := strings.Index(value, "/"); index >= 0 {
		return value[:index], value[index+1:]
	}
	return "", value
}

func defaultImageSizeValues() []string {
	return []string{
		"auto", "1:1", "3:2", "2:3", "4:3", "3:4", "16:9", "21:9", "9:16",
		"1024x1024", "1360x1024", "1024x1360", "1536x1024", "1024x1536", "1024x1280", "1280x1024", "2048x878", "1824x1024", "1024x1824",
		"2048x2048", "2304x1728", "1728x2304", "2496x1664", "1664x2496", "1792x2240", "2240x1792", "3136x1344", "2752x1536", "1536x2752",
		"2880x2880", "3264x2448", "2448x3264", "3504x2336", "2336x3504", "2560x3200", "3200x2560", "3808x1632", "3840x2160", "2160x3840",
	}
}

// legacyImageSizeValues 用于修复旧数据中仅保存了 "*" 的图片尺寸能力。
// 这组值是前后台共同展示的基础预设，不能让历史通配符配置继续污染用户生成参数。
func legacyImageSizeValues() []string {
	return []string{
		"1:1", "3:2", "2:3", "4:3", "3:4", "16:9", "21:9", "9:16",
		"1024x1024", "1536x1024", "1024x1536",
	}
}

func DefaultModelCapabilityConfigForModel(protocol string, modelName string) *ModelCapabilityConfig {
	// 文本模型是否支持视觉输入不能从协议或模型名可靠推断，默认关闭，由管理员按真实上游能力开启。
	streaming := true
	text := &TextCapabilityConfig{Streaming: &streaming, References: TextReferenceConfig{PromptMaxChars: 32000}}
	// Replicate 是一模型一 schema 的平台，视频能力合同同样必须逐模型对齐上游。
	replicateVideo := model.ChannelInterfaceType(protocol) == model.ChannelInterfaceReplicatePredictionVideo
	video := &VideoCapabilityConfig{
		References:        VideoReferenceConfig{PromptMaxChars: DefaultVideoPromptMaxChars, MinImages: 0, MaxImages: 9, MaxImageBytes: 30 * 1024 * 1024, MaxVideos: 0, MaxVideoBytes: 0, MaxVideoDuration: 0, MaxAudios: 0, MaxAudioBytes: 0, MaxAudioDuration: 0},
		Duration:          VideoDurationConfig{Selection: "range", Min: 1, Max: 15, Step: 1, Default: 6},
		Ratios:            []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"},
		DefaultRatio:      "16:9",
		Resolutions:       []string{"480p", "720p", "1080p", "1440p", "2160p"},
		DefaultResolution: "720p",
		GenerateAudio:     VideoBooleanConfig{Supported: false, Default: false},
		Watermark:         VideoBooleanConfig{Supported: false, Default: false},
		Operations:        []string{"text_to_video", "image_to_video"},
		DefaultOperation:  "text_to_video",
	}
	if replicateVideo {
		applyReplicateVideoCapability(video, modelName)
		return &ModelCapabilityConfig{Version: 1, Text: text, Image: DefaultImageCapabilityConfig(protocol, modelName), Video: video}
	}
	switch model.ChannelInterfaceType(protocol) {
	case model.ChannelInterfaceVolcengineJiMengVideo:
		video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{5, 10}, Default: 5}
		video.Resolutions = []string{"720p"}
	case model.ChannelInterfaceGeminiVeo:
		video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{4, 6, 8}, Default: 6}
		video.Resolutions = []string{"720p", "1080p"}
	case model.ChannelInterfaceVolcengineArkVideo, model.ChannelInterfaceVolcengineArkAgentPlanVideo:
		video.Operations = append(video.Operations, "reference_to_video")
		video.References.MaxVideos, video.References.MaxAudios = 3, 3
		video.References.MaxVideoBytes, video.References.MaxAudioBytes = 200*1024*1024, 15*1024*1024
		video.References.MaxVideoDuration, video.References.MaxAudioDuration = 15, 15
		video.References.MinVideoDuration, video.References.MinAudioDuration = 2, 2
		video.GenerateAudio = VideoBooleanConfig{Supported: true, Default: true}
		video.Watermark = VideoBooleanConfig{Supported: true, Default: false}
		video.Resolutions = []string{"480p", "720p", "1080p"}
	case model.ChannelInterfaceNewAPIChannel1, model.ChannelInterfaceNewAPIChannel2:
		video.References.MaxVideos, video.References.MaxAudios = 3, 3
		video.References.MaxVideoBytes, video.References.MaxAudioBytes = 200*1024*1024, 15*1024*1024
		video.References.MaxVideoDuration, video.References.MaxAudioDuration = 15, 15
		video.GenerateAudio = VideoBooleanConfig{Supported: true, Default: true}
		if model.ChannelInterfaceType(protocol) == model.ChannelInterfaceNewAPIChannel1 {
			video.Resolutions = []string{"480p", "720p", "1080p"}
		}
	case model.ChannelInterfaceNewAPIVideo, model.ChannelInterfaceXAIVideo:
		video.GenerateAudio = VideoBooleanConfig{Supported: false, Default: false}
	case model.ChannelInterfaceNovitaVideo:
		video.References.MaxImages, video.References.MaxImageBytes = 1, 10*1024*1024
		video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{5, 10}, Default: 5}
		video.Ratios = []string{"16:9", "9:16", "1:1"}
		video.Resolutions = []string{"1080p"}
		video.DefaultResolution = "1080p"
	case model.ChannelInterfaceZonghengVideo:
		// 规格来自上游能力中枢（公开接口 /api/models）：时长 6–15 秒连续可选，清晰度只有
		// 480p / 720p，参考图最多 7 张，音视频参考为 0。不要照抄平台默认的 9 张参考图与
		// 1440p/2160p 档位——那些组合上游会直接拒收，用户只会拿到一次失败任务。
		video.Duration = VideoDurationConfig{Selection: "range", Min: 6, Max: 15, Step: 1, Default: 6}
		video.Ratios = []string{"16:9", "9:16", "1:1"}
		video.Resolutions = []string{"480p", "720p"}
		video.DefaultResolution = "720p"
		video.References.MaxImages = 7
	case model.ChannelInterfaceMiniMaxVideo:
		video.Operations = append(video.Operations, "reference_to_video")
		video.References.MaxImages = 9
		video.References.MaxImageBytes = 30 * 1024 * 1024
		video.References.MaxVideos = 3
		video.References.MaxVideoBytes = 50 * 1024 * 1024
		video.References.MaxVideoDuration = 15
		video.References.MaxAudios = 3
		video.References.MaxAudioBytes = 15 * 1024 * 1024
		video.References.MaxAudioDuration = 15
		video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, Default: 5}
		video.Ratios = []string{"adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16"}
		video.DefaultRatio = "16:9"
		video.Resolutions = []string{"768P", "2K"}
		video.DefaultResolution = "768P"
		video.Watermark = VideoBooleanConfig{Supported: true, Default: false}
	case model.ChannelInterfaceAgnesVideo:
		video = applyModelSpecificVideoCapability(video, protocol, modelName)
	}
	if isSeedance2Family(protocol, modelName) {
		video.References = overlayOfficialSeedance2References(video.References, isSeedance25Model(modelName))
		if model.IsVolcengineArkVideoProtocol(model.ChannelInterfaceType(protocol)) {
			video.References.MinAudioDuration = 2
		}
		video.Operations = appendUniqueString(video.Operations, "reference_to_video")
		if isSeedance25Model(modelName) {
			video.Operations = appendUniqueString(video.Operations, "audio_to_video")
			if video.Duration.Selection == "range" && video.Duration.Max < 30 {
				video.Duration.Max = 30
			}
		}
	}
	return &ModelCapabilityConfig{Version: 1, Text: text, Image: DefaultImageCapabilityConfig(protocol, modelName), Video: video}
}

func DecodeModelCapabilityConfig(raw string) (*ModelCapabilityConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var value ModelCapabilityConfig
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	return &value, nil
}

// normalizedChannelModelCapability 从持久化记录恢复渠道模型的权威能力合同。
// 目录读取可以选择隔离损坏记录；任务创建等写路径必须把错误向上返回并失败关闭。
func normalizedChannelModelCapability(channelModel *model.ChannelModel) (*ModelCapabilityConfig, error) {
	if channelModel == nil {
		return nil, errors.New("渠道模型为空")
	}
	capability := normalizeCapability(channelModel.Capability)
	if capability == "audio" {
		return nil, nil
	}
	if capability != "text" && capability != "image" && capability != "video" {
		return nil, fmt.Errorf("不支持的渠道模型能力：%s", channelModel.Capability)
	}
	config, err := DecodeModelCapabilityConfig(channelModel.CapabilityConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("解析渠道模型能力配置失败：%w", err)
	}
	normalized, err := NormalizeModelCapabilityConfigForModel(capability, string(channelModel.Protocol), firstNonEmpty(channelModel.ProviderModelKey, channelModel.ModelKey), config)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func NormalizeModelCapabilityConfig(capability string, protocol string, input *ModelCapabilityConfig) (*ModelCapabilityConfig, error) {
	return NormalizeModelCapabilityConfigForModel(capability, protocol, "", input)
}

func NormalizeModelCapabilityConfigForModel(capability string, protocol string, modelName string, input *ModelCapabilityConfig) (*ModelCapabilityConfig, error) {
	if capability != "text" && capability != "image" && capability != "video" {
		return nil, nil
	}
	if capability == "text" {
		if input == nil || input.Text == nil {
			return nil, BadAuthRequest("请配置文本模型能力参数")
		}
		text := *input.Text
		if text.Streaming == nil {
			streaming := true
			text.Streaming = &streaming
		}
		value := &ModelCapabilityConfig{Version: 1, Text: &text}
		if err := validateTextCapabilityConfig(value.Text); err != nil {
			return nil, err
		}
		return value, nil
	}
	if capability == "image" {
		if input == nil || input.Image == nil {
			return nil, BadAuthRequest("请配置图片模型能力参数")
		}
		value := &ModelCapabilityConfig{Version: 1, Image: input.Image}
		if err := validateImageCapabilityConfig(value.Image); err != nil {
			return nil, err
		}
		return value, nil
	}
	if input == nil || input.Video == nil {
		return nil, BadAuthRequest("请配置视频模型能力参数")
	}
	value := &ModelCapabilityConfig{Version: 1, Video: applyModelSpecificVideoCapability(input.Video, protocol, modelName)}
	if err := validateVideoCapabilityConfig(value.Video); err != nil {
		return nil, err
	}
	return value, nil
}

func applyModelSpecificVideoCapability(profile *VideoCapabilityConfig, protocol string, modelName string) *VideoCapabilityConfig {
	if profile == nil {
		return profile
	}
	normalizedProtocol := strings.TrimSpace(protocol)
	normalizedModel := strings.ToLower(strings.TrimSpace(modelName))
	// aigenvideo-seedance 声明式插件的请求模板直接把 images[] 铺给上游（插件自己声明最多 10 张
	// 参考图），但通用视频合同只声明了文生视频/图生视频。画布挂 3 张以上参考图时操作会被推断成
	// reference_to_video，合同里没有这个操作，整组模型就在模型下拉里被判成不兼容而无法选中。
	// 这里按插件真实能力补齐操作；视频/音频参考压回 0，因为该协议的模板只映射 images[]，
	// 放开音视频参考只会让用户的上传被静默丢弃。
	if strings.HasPrefix(normalizedProtocol, "aigenvideo-seedance") && strings.Contains(normalizedModel, "seedance-2") {
		value := *profile
		value.References = profile.References
		value.References.MinImages = 0
		value.References.MaxImages = 10
		value.References.MaxVideos = 0
		value.References.MaxVideoBytes = 0
		value.References.MaxVideoDuration = 0
		value.References.MinVideoDuration = 0
		value.References.MaxAudios = 0
		value.References.MaxAudioBytes = 0
		value.References.MaxAudioDuration = 0
		value.References.MinAudioDuration = 0
		value.References.MaxAudioTotalDuration = 0
		value.Operations = appendUniqueString(value.Operations, "reference_to_video")
		return &value
	}
	if model.ChannelInterfaceType(normalizedProtocol) != model.ChannelInterfaceAgnesVideo {
		return profile
	}
	if normalizedModel != "agnes-video-2.5" && normalizedModel != "agnes-video-2.5-flash" {
		return profile
	}
	value := *profile
	value.References = profile.References
	flash := normalizedModel == "agnes-video-2.5-flash"
	value.References.MaxImages = 9
	value.References.MaxVideos = 3
	value.References.MaxAudios = 3
	value.References.MaxVideoBytes = 200 * 1024 * 1024
	value.References.MaxVideoDuration = 15
	value.References.MaxAudioBytes = 15 * 1024 * 1024
	value.References.MaxAudioDuration = 15
	if flash {
		value.References.MaxImages = 5
		value.References.MaxVideos = 0
		value.References.MaxVideoBytes = 0
		value.References.MaxVideoDuration = 0
	}
	value.Duration = VideoDurationConfig{Selection: "range", Min: 4, Max: 12, Step: 1, Default: 5}
	value.Ratios = []string{"21:9", "16:9", "4:3", "1:1", "3:4", "9:16"}
	value.DefaultRatio = "16:9"
	value.Resolutions = []string{"720P", "960P", "2K"}
	if flash {
		value.Resolutions = []string{"720P"}
	}
	value.DefaultResolution = "720P"
	value.GenerateAudio = VideoBooleanConfig{Supported: false, Default: false}
	value.Watermark = VideoBooleanConfig{Supported: false, Default: false}
	value.Operations = []string{"text_to_video", "image_to_video", "reference_to_video", "audio_to_video"}
	value.DefaultOperation = "text_to_video"
	return &value
}

func appendUniqueString(values []string, value string) []string {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}

// CapabilitySpecFromModelCapabilityConfig 将渠道模型的真实供应能力投影为路由能力规格。
// 渠道模型能力参数是唯一事实来源，前台模型供应线路直接引用该规格。
func CapabilitySpecFromModelCapabilityConfig(config *ModelCapabilityConfig, capability string) (CapabilitySpec, error) {
	spec := CapabilitySpec{Version: 1, Capability: capability, Inputs: map[string]InputConstraint{}, Options: map[string]OptionConstraint{}}
	// 音频模型当前没有可编辑的渠道能力 JSON，使用空能力规格表示“无额外路由约束”。
	if capability == "audio" {
		return spec, nil
	}
	if config == nil {
		switch capability {
		case "text":
			return spec, BadAuthRequest("渠道文本模型尚未配置能力参数")
		case "image":
			return spec, BadAuthRequest("渠道图片模型尚未配置能力参数")
		case "video":
			return spec, BadAuthRequest("渠道视频模型尚未配置能力参数")
		default:
			return spec, BadAuthRequest("渠道模型尚未配置能力参数")
		}
	}
	switch capability {
	case "text":
		if config.Text == nil {
			return spec, BadAuthRequest("渠道文本模型尚未配置能力参数")
		}
		addInputConstraint(spec.Inputs, "image", 0, config.Text.References.MaxImages)
		addInputConstraint(spec.Inputs, "video", 0, config.Text.References.MaxVideos)
	case "image":
		if config.Image == nil {
			return spec, BadAuthRequest("渠道图片模型尚未配置能力参数")
		}
		image := config.Image
		addInputConstraint(spec.Inputs, "image", 0, image.References.MaxImages)
		if image.References.MaskSupported {
			addInputConstraint(spec.Inputs, "mask", 0, 1)
		}
		if image.Size.Parameter != "none" {
			spec.Options["size"] = imageSizeOptionConstraint(image.Size)
			spec.ImageSize = capabilityImageSizeFromConfig(image.Size)
		}
		if image.Quality.Supported {
			spec.Options["quality"] = anyValues(image.Quality.Values)
		}
		if image.TransparentBackground.Supported {
			spec.Options["transparentBackground"] = boolValues(true)
		} else {
			spec.Options["transparentBackground"] = boolValues(false)
		}
		spec.Options["count"] = numericRange(1, float64(image.MaxOutputs), 1)
	case "video":
		if config.Video == nil {
			return spec, BadAuthRequest("渠道视频模型尚未配置能力参数")
		}
		video := config.Video
		spec.Operations = append([]string(nil), video.Operations...)
		addInputConstraint(spec.Inputs, "image", video.References.MinImages, video.References.MaxImages)
		addInputConstraint(spec.Inputs, "video", 0, video.References.MaxVideos)
		addInputConstraint(spec.Inputs, "audio", 0, video.References.MaxAudios)
		if video.Duration.Selection == "enum" {
			values := make([]any, 0, len(video.Duration.Values))
			for _, value := range video.Duration.Values {
				values = append(values, value)
			}
			spec.Options["videoSeconds"] = OptionConstraint{Values: values}
		} else {
			spec.Options["videoSeconds"] = numericRange(float64(video.Duration.Min), float64(video.Duration.Max), float64(video.Duration.Step))
		}
		spec.Options["size"] = anyValues(video.Ratios)
		if len(video.Resolutions) > 0 {
			spec.Options["vquality"] = anyValues(video.Resolutions)
		}
		if video.GenerateAudio.Supported {
			spec.Options["videoGenerateAudio"] = boolValues(true)
		} else {
			spec.Options["videoGenerateAudio"] = boolValues(false)
		}
		if video.Watermark.Supported {
			spec.Options["videoWatermark"] = boolValues(true)
		} else {
			spec.Options["videoWatermark"] = boolValues(false)
		}
	default:
		return spec, BadAuthRequest("未知模型能力类型")
	}
	return spec, nil
}

// imageSizeOptionConstraint 保留可见的标准尺寸/比例，同时用 * 表示允许自定义。
// * 不能替代标准值，否则管理端只能看到一个没有业务含义的通配符。
func imageSizeOptionConstraint(size ImageSizeConfig) OptionConstraint {
	values := make([]string, 0, len(size.Values)+1)
	seen := make(map[string]struct{}, len(size.Values)+1)
	for _, value := range size.Values {
		value = strings.TrimSpace(value)
		if value == "" || value == "*" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	if size.AllowCustom {
		if len(values) == 0 {
			for _, value := range legacyImageSizeValues() {
				seen[value] = struct{}{}
				values = append(values, value)
			}
		}
		values = append(values, "*")
	}
	return anyValues(values)
}

func capabilityImageSizeFromConfig(size ImageSizeConfig) *CapabilityImageSize {
	if size.Parameter == "" || size.Parameter == "none" {
		return nil
	}
	result := &CapabilityImageSize{Parameter: size.Parameter, AllowCustom: size.AllowCustom}
	for _, preset := range size.Presets {
		tier := strings.ToLower(strings.TrimSpace(preset.Tier))
		ratio := strings.TrimSpace(preset.Ratio)
		value := strings.TrimSpace(preset.Size)
		if tier == "" || ratio == "" || value == "" {
			continue
		}
		result.Presets = append(result.Presets, CapabilityImageSizePreset{
			Size: value, Tier: tier, Ratio: ratio, Width: preset.Width, Height: preset.Height,
		})
	}
	return result
}

func addInputConstraint(inputs map[string]InputConstraint, name string, min int, max int) {
	if min <= 0 && max <= 0 {
		return
	}
	inputs[name] = InputConstraint{Min: min, Max: max}
}

func validateTextCapabilityConfig(value *TextCapabilityConfig) error {
	if value.References.PromptMaxChars < 1 || value.References.PromptMaxChars > 1000000 {
		return BadAuthRequest("提示词最大字符数必须在 1-1000000 之间")
	}
	for name, number := range map[string]int{"最大图片引用数": value.References.MaxImages, "最大视频引用数": value.References.MaxVideos} {
		if number < 0 || number > 100 {
			return BadAuthRequest(name + "必须在 0-100 之间")
		}
	}
	if value.References.MaxImageBytes < 0 || value.References.MaxVideoBytes < 0 {
		return BadAuthRequest("引用素材大小限制不能小于 0")
	}
	return nil
}

func validateImageCapabilityConfig(value *ImageCapabilityConfig) error {
	if value.References.PromptMaxChars < 1 || value.References.PromptMaxChars > 1000000 {
		return BadAuthRequest("提示词最大字符数必须在 1-1000000 之间")
	}
	if value.References.MaxImages < 0 || value.References.MaxImages > 100 || value.References.MaxImageBytes < 0 {
		return BadAuthRequest("图片引用限制无效")
	}
	if value.MaxOutputs < 1 || value.MaxOutputs > 100 {
		return BadAuthRequest("单次图片数量必须在 1-100 之间")
	}
	switch value.Size.Parameter {
	case "none":
		value.Size.Values = []string{}
		value.Size.Presets = nil
		value.Size.Default = "auto"
		value.Size.AllowCustom = false
	case "size", "aspect_ratio":
		if strings.TrimSpace(value.Size.Default) == "" {
			return BadAuthRequest("请配置默认图片尺寸或比例")
		}
		if !value.Size.AllowCustom && !containsCapabilityString(value.Size.Values, value.Size.Default) {
			return BadAuthRequest("默认图片尺寸必须属于支持值")
		}
	default:
		return BadAuthRequest("尺寸参数仅支持不发送、size 或 aspect_ratio")
	}
	seenPresets := make(map[string]bool)
	for _, preset := range value.Size.Presets {
		if preset.Tier != "1k" && preset.Tier != "2k" && preset.Tier != "4k" {
			return BadAuthRequest("图片分辨率档位仅支持 1K、2K、4K")
		}
		parts := strings.Split(preset.Ratio, ":")
		if len(parts) != 2 {
			return BadAuthRequest("图片预设比例格式无效")
		}
		w, ew := strconv.Atoi(parts[0])
		h, eh := strconv.Atoi(parts[1])
		if ew != nil || eh != nil || w <= 0 || h <= 0 || w > 100000 || h > 100000 || max(w, h) > min(w, h)*3 {
			return BadAuthRequest("图片预设比例无效")
		}
		if preset.Width <= 0 || preset.Height <= 0 || max(preset.Width, preset.Height) > 3840 || max(preset.Width, preset.Height) > min(preset.Width, preset.Height)*3 || preset.Width*preset.Height < 655360 || preset.Width*preset.Height > 8294400 || preset.Size != fmt.Sprintf("%dx%d", preset.Width, preset.Height) {
			return BadAuthRequest("图片预设像素尺寸无效")
		}
		// 容许像素取整误差，但不能将横屏尺寸标记成竖屏或其他比例。
		difference := preset.Width*h - preset.Height*w
		if difference < 0 {
			difference = -difference
		}
		if difference*1000 > preset.Height*w*25 {
			return BadAuthRequest("图片预设像素尺寸与宽高比不一致")
		}
		a, b := w, h
		for b != 0 {
			a, b = b, a%b
		}
		key := fmt.Sprintf("%s:%d:%d", preset.Tier, w/a, h/a)
		if seenPresets[key] {
			return BadAuthRequest("图片尺寸预设重复")
		}
		seenPresets[key] = true
		requestValue := preset.Size
		if value.Size.Parameter == "aspect_ratio" {
			requestValue = preset.Ratio
		}
		if !containsCapabilityString(value.Size.Values, requestValue) {
			return BadAuthRequest("图片预设必须包含在尺寸支持值中")
		}
	}
	if value.Quality.Supported {
		if len(value.Quality.Values) == 0 || strings.TrimSpace(value.Quality.Default) == "" || !containsCapabilityString(value.Quality.Values, value.Quality.Default) {
			return BadAuthRequest("请配置图片质量支持值和默认值")
		}
	} else {
		value.Quality.Values = []string{}
		value.Quality.Default = "auto"
	}
	if err := validateImagePresetSelection(value, value.Quality.Default, value.Size.Default); err != nil {
		return BadAuthRequest("默认图片分辨率与宽高比不在已配置的组合中")
	}
	if !value.TransparentBackground.Supported {
		value.TransparentBackground.Default = false
	}
	return nil
}

// videoDurationKey 把用户选的分辨率收敛成 durationByResolution 的键。
//
// 解析顺序与真实请求一致：先按合同里的 resolutions 认（"2k"、"4k" 这类别名由
// videoResolutionNameRequest 归一），认不出来（auto / 空 / 未声明）就用默认分辨率——
// 前端在这种情况下的选项也来自默认档位。
func videoDurationKey(profile *VideoCapabilityConfig, requested string) string {
	if profile == nil {
		return ""
	}
	if name := videoResolutionNameRequest(profile, requested); name != "" {
		return strings.ToLower(strings.TrimSpace(name))
	}
	return strings.ToLower(strings.TrimSpace(profile.DefaultResolution))
}

// videoDurationForResolution 取某个分辨率档位下的时长合同，没登记就回落到顶层 duration。
func videoDurationForResolution(profile *VideoCapabilityConfig, resolution string) VideoDurationConfig {
	if profile == nil {
		return VideoDurationConfig{}
	}
	if len(profile.DurationByResolution) == 0 {
		return profile.Duration
	}
	key := strings.ToLower(strings.TrimSpace(resolution))
	if key == "" {
		return profile.Duration
	}
	if override, ok := profile.DurationByResolution[key]; ok {
		return override
	}
	return profile.Duration
}

func validateVideoCapabilityConfig(value *VideoCapabilityConfig) error {
	if value.References.PromptMaxChars < 1 || value.References.PromptMaxChars > 1000000 {
		return BadAuthRequest("提示词最大字符数必须在 1-1000000 之间")
	}
	for name, number := range map[string]int{"最少图片引用数": value.References.MinImages, "最大图片引用数": value.References.MaxImages, "最大视频引用数": value.References.MaxVideos, "最大音频引用数": value.References.MaxAudios} {
		if number < 0 || number > 100 {
			return BadAuthRequest(name + "必须在 0-100 之间")
		}
	}
	if value.References.MinImages > value.References.MaxImages {
		return BadAuthRequest("最少图片引用数不能超过最大图片引用数")
	}
	if value.References.MaxImageBytes < 0 || value.References.MaxVideoBytes < 0 || value.References.MaxAudioBytes < 0 || value.References.MaxVideoDuration < 0 || value.References.MaxAudioDuration < 0 || value.References.MinVideoDuration < 0 || value.References.MinAudioDuration < 0 || value.References.MaxAudioTotalDuration < 0 || value.References.MaxVideoTotalDuration < 0 {
		return BadAuthRequest("引用素材限制不能小于 0")
	}
	if value.References.MinImageWidth < 0 || value.References.MaxImageWidth < 0 || value.References.MinImageHeight < 0 || value.References.MaxImageHeight < 0 || value.References.MinVideoWidth < 0 || value.References.MaxVideoWidth < 0 || value.References.MinVideoHeight < 0 || value.References.MaxVideoHeight < 0 {
		return BadAuthRequest("引用素材尺寸限制不能小于 0")
	}
	if value.References.MinImageAspect < 0 || value.References.MaxImageAspect < 0 || value.References.MinVideoAspect < 0 || value.References.MaxVideoAspect < 0 || value.References.MinImagePixels < 0 || value.References.MaxImagePixels < 0 || value.References.MinVideoPixels < 0 || value.References.MaxVideoPixels < 0 {
		return BadAuthRequest("引用素材宽高比或像素限制不能小于 0")
	}
	if (value.References.MaxVideoDuration > 0 && value.References.MinVideoDuration > value.References.MaxVideoDuration) || (value.References.MaxAudioDuration > 0 && value.References.MinAudioDuration > float64(value.References.MaxAudioDuration)) {
		return BadAuthRequest("引用素材最小时长不能超过最大时长")
	}
	if err := validateVideoDuration(value.Duration); err != nil {
		return err
	}
	for resolution, duration := range value.DurationByResolution {
		if !containsCapabilityString(value.Resolutions, resolution) {
			return BadAuthRequest("按分辨率设置的时长档位 " + resolution + " 不在支持的分辨率里")
		}
		if err := validateVideoDuration(duration); err != nil {
			return err
		}
	}
	if len(value.Ratios) == 0 {
		if strings.TrimSpace(value.DefaultRatio) != "" {
			return BadAuthRequest("未配置画面比例时不能设置默认比例")
		}
	} else if strings.TrimSpace(value.DefaultRatio) == "" || !containsCapabilityString(value.Ratios, value.DefaultRatio) {
		return BadAuthRequest("默认画面比例必须属于支持值")
	}
	if len(value.Resolutions) == 0 {
		if strings.TrimSpace(value.DefaultResolution) != "" {
			return BadAuthRequest("未配置输出分辨率时不能设置默认分辨率")
		}
	} else if strings.TrimSpace(value.DefaultResolution) == "" || !containsCapabilityString(value.Resolutions, value.DefaultResolution) {
		return BadAuthRequest("默认输出分辨率必须属于支持值")
	}
	if len(value.Operations) == 0 || strings.TrimSpace(value.DefaultOperation) == "" || !containsCapabilityString(value.Operations, value.DefaultOperation) {
		return BadAuthRequest("请至少配置一个生成模式，并选择默认模式")
	}
	return nil
}

func validateVideoDuration(value VideoDurationConfig) error {
	switch value.Selection {
	case "range":
		if value.Min < 1 || value.Max < value.Min || value.Max > 3600 || value.Step < 1 || value.Default < value.Min || value.Default > value.Max || (value.Default-value.Min)%value.Step != 0 {
			return BadAuthRequest("视频时长范围或默认值无效")
		}
	case "enum":
		if len(value.Values) == 0 || len(value.Values) > 100 {
			return BadAuthRequest("视频固定时长至少需要一个选项")
		}
		values := append([]int(nil), value.Values...)
		sort.Ints(values)
		for index, item := range values {
			if (item < 1 && item != -1) || item > 3600 || (index > 0 && values[index-1] == item) {
				return BadAuthRequest("视频固定时长选项无效或重复")
			}
		}
		if !containsInt(values, value.Default) {
			return BadAuthRequest("视频默认时长必须属于固定时长选项")
		}
	default:
		return BadAuthRequest("视频时长选择方式仅支持范围或固定值")
	}
	return nil
}

func (s *Service) ValidateTaskCapability(input map[string]any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return BadAuthRequest("任务输入格式无效")
	}
	var taskInput canvasGenerationInput
	if err := json.Unmarshal(encoded, &taskInput); err != nil || (taskInput.Mode != "image" && taskInput.Mode != "video" && taskInput.Mode != "audio") {
		return nil
	}
	if isWorkflowProviderInterface(taskInput.Config.InterfaceType) {
		if err := validateWorkflowProviderPromptLength(taskInput); err != nil {
			return err
		}
		return validateWorkflowProviderConfig(taskInput.Mode, taskInput.Config)
	}
	// 普通音频模型沿用主线的能力校验路径；当前专用能力表只覆盖图片和视频。
	if taskInput.Mode == "audio" {
		return nil
	}
	channelID := strings.TrimSpace(taskInput.Config.ChannelID)
	if channelID == "" {
		channelID = systemChannelIDFromBaseURL(taskInput.Config.BaseURL)
	}
	if channelID == "" {
		if taskInput.Mode == "image" {
			profile := DefaultImageCapabilityConfig(taskInput.Config.InterfaceType, taskInput.Config.Model)
			if taskInput.Config.CapabilityConfig != nil && taskInput.Config.CapabilityConfig.Image != nil {
				profile = taskInput.Config.CapabilityConfig.Image
			}
			return validateImageTask(profile, taskInput)
		}
		profile := taskInput.Config.CapabilityConfig
		if profile == nil || profile.Video == nil {
			if taskInput.Config.InterfaceType != string(model.ChannelInterfaceAgnesVideo) {
				return nil
			}
			profile = DefaultModelCapabilityConfigForModel(taskInput.Config.InterfaceType, taskInput.Config.Model)
		}
		normalized, normalizeErr := NormalizeModelCapabilityConfigForModel("video", taskInput.Config.InterfaceType, taskInput.Config.Model, profile)
		if normalizeErr != nil || normalized == nil || normalized.Video == nil {
			return BadAuthRequest("当前视频模型能力参数无效")
		}
		return validateVideoTask(normalized.Video, taskInput)
	}
	item, err := s.repo.ChannelModelByKey(channelID, providerChannelModelKey(taskInput.Config))
	if err != nil {
		return BadAuthRequest("当前系统渠道模型未配置或已停用")
	}
	profile, err := DecodeModelCapabilityConfig(item.CapabilityConfigJSON)
	if taskInput.Mode == "image" {
		if err != nil {
			return BadAuthRequest("当前图片模型能力参数无效")
		}
		imageProfile := DefaultImageCapabilityConfig(string(item.Protocol), firstNonEmpty(item.ProviderModelKey, item.ModelKey))
		if profile != nil && profile.Image != nil {
			imageProfile = profile.Image
		}
		return validateImageTask(applyModelSpecificImageCapability(imageProfile, string(item.Protocol), firstNonEmpty(item.ProviderModelKey, item.ModelKey), taskInput.Config.APIFormat), taskInput)
	}
	if err != nil || profile == nil || profile.Video == nil {
		return BadAuthRequest("当前视频模型尚未配置能力参数")
	}
	normalized, normalizeErr := NormalizeModelCapabilityConfigForModel("video", string(item.Protocol), firstNonEmpty(item.ProviderModelKey, item.ModelKey), profile)
	if normalizeErr != nil || normalized == nil || normalized.Video == nil {
		return BadAuthRequest("当前视频模型能力参数无效")
	}
	applyFixedVideoResolution(&taskInput, normalized.Video)
	if config, ok := input["config"].(map[string]any); ok {
		config["vquality"] = taskInput.Config.VQuality
	}
	return validateVideoTask(normalized.Video, taskInput)
}

// applyModelSpecificImageCapability is retained as a narrow normalization hook
// for provider-specific image validation. The stored capability profile is
// already normalized when the channel model is saved, so no second override is
// needed here.
func applyModelSpecificImageCapability(profile *ImageCapabilityConfig, _ string, _ string, _ string) *ImageCapabilityConfig {
	return profile
}

// applyFixedVideoResolution 让单档位 SKU 的预扣、恢复和上游请求保持同一分辨率。
func applyFixedVideoResolution(input *canvasGenerationInput, profile *VideoCapabilityConfig) {
	if input == nil || profile == nil || len(profile.Resolutions) != 1 {
		return
	}
	if resolution := videoResolutionNameRequest(profile, profile.Resolutions[0]); resolution != "" {
		input.Config.VQuality = resolution
	}
}

func validateReferenceDuration(kind string, index int, durationMs int64, minimum, maximum float64) error {
	if minimum <= 0 && maximum <= 0 {
		return nil
	}
	if durationMs <= 0 {
		return BadAuthRequest(fmt.Sprintf("第 %d 段参考%s的时长无法读取，请重新导入素材后再提交", index+1, kind))
	}
	if durationMs < durationLimitMs(minimum) || (maximum > 0 && durationMs > durationLimitMs(maximum)) {
		return BadAuthRequest(fmt.Sprintf("第 %d 段参考%s时长为 %.2f 秒，需要 %s 秒；请裁剪或更换这段素材后再提交", index+1, kind, float64(durationMs)/1000, referenceBound(minimum, maximum)))
	}
	return nil
}

func validateVideoTask(profile *VideoCapabilityConfig, input canvasGenerationInput) error {
	if profile == nil {
		return BadAuthRequest("当前视频模型能力参数无效")
	}
	if err := validateModelPromptLength("视频", input.Prompt, profile.References.PromptMaxChars); err != nil {
		return err
	}
	if err := validateVideoReferenceMedia(profile, input); err != nil {
		return err
	}
	return validateVideoTaskParameters(profile, input)
}

func validateVideoTaskParameters(profile *VideoCapabilityConfig, input canvasGenerationInput) error {
	if profile == nil {
		return BadAuthRequest("当前视频模型能力参数无效")
	}
	if err := validateModelPromptLength("视频", input.Prompt, profile.References.PromptMaxChars); err != nil {
		return err
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(input.Config.VideoSeconds))
	durationKey := videoDurationKey(profile, input.Config.VQuality)
	if err != nil || !videoDurationAllowed(videoDurationForResolution(profile, durationKey), seconds) {
		// 档位自己有上限时报得更具体：用户看到"720p 最长 12 秒"才知道该换 480p，
		// 只说"时长不在支持范围内"会让他把 10 / 12 / 15 全试一遍。
		if _, limited := profile.DurationByResolution[durationKey]; limited && durationKey != "" {
			return BadAuthRequest("当前时长在该分辨率档位下不可用：" + durationKey + " 档只支持 " + videoDurationRangeLabel(videoDurationForResolution(profile, durationKey)))
		}
		return BadAuthRequest("视频时长不在当前模型支持范围内")
	}
	if input.Config.Size != "" && !videoRatioAllowed(profile.Ratios, input.Config.Size) {
		return BadAuthRequest("画面比例不在当前模型支持范围内")
	}
	if len(profile.Resolutions) > 0 && !isAutomaticVideoResolution(input.Config.VQuality) && videoResolutionNameRequest(profile, input.Config.VQuality) == "" {
		return BadAuthRequest("输出分辨率不在当前模型支持范围内")
	}
	operation := metadataString(input.Metadata, "videoEditOperation")
	if operation == "" {
		if len(input.ReferenceImages) > 0 {
			operation = "image_to_video"
		} else {
			operation = profile.DefaultOperation
		}
	}
	if !containsCapabilityString(profile.Operations, operation) {
		return BadAuthRequest("当前视频模型不支持该生成模式")
	}
	return nil
}

func validateImageTask(profile *ImageCapabilityConfig, input canvasGenerationInput) error {
	if profile == nil {
		return nil
	}
	modelName := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(input.Config.Model)), "models/")
	if input.Config.InterfaceType == string(model.ChannelInterfaceGrokImage) && modelName == "grok-imagine-image-quality" {
		const maxPromptBytes = 8000
		promptBytes := len(withSystemPrompt(input.Config, input.Prompt))
		if promptBytes > maxPromptBytes {
			return BadAuthRequest(fmt.Sprintf("Grok 图片完整提示词为 %d UTF-8 字节，超过上游 %d 字节限制。系统不会自动删改；请精简当前输入、连线文本、角色卡、画风或模板内容后重试", promptBytes, maxPromptBytes))
		}
	}
	if len(input.ReferenceImages) > profile.References.MaxImages {
		return BadAuthRequest(fmt.Sprintf("当前图片模型最多支持 %d 张参考图", profile.References.MaxImages))
	}
	for _, media := range input.ReferenceImages {
		if profile.References.MaxImageBytes > 0 && media.Bytes > profile.References.MaxImageBytes {
			return BadAuthRequest("参考图片文件超过当前模型大小限制")
		}
	}
	if input.Mask != nil && !profile.References.MaskSupported {
		return BadAuthRequest("当前图片模型不支持蒙版编辑")
	}
	if profile.Size.Parameter != "none" && !profile.Size.AllowCustom && strings.TrimSpace(input.Config.Size) != "" && !containsCapabilityString(profile.Size.Values, input.Config.Size) {
		return BadAuthRequest("图片尺寸不在当前模型支持范围内")
	}
	if profile.Size.Parameter == "size" && profile.Size.AllowCustom && strings.HasPrefix(modelName, "gpt-image-2") && !containsCapabilityString(profile.Size.Values, input.Config.Size) {
		if err := validateGPTImage2CustomSize(input.Config.Size); err != nil {
			return BadAuthRequest(err.Error())
		}
	}
	quality := strings.TrimSpace(input.Config.Quality)
	if profile.Quality.Supported && quality != "" && !strings.EqualFold(quality, "auto") && !strings.EqualFold(quality, "any") && !containsCapabilityString(profile.Quality.Values, quality) {
		return BadAuthRequest("图片质量不在当前模型支持范围内")
	}
	if err := validateImagePresetSelection(profile, qualityForImageValidation(quality, profile.Quality.Default), firstNonEmpty(input.Config.Size, profile.Size.Default)); err != nil {
		return err
	}
	count, err := strconv.Atoi(strings.TrimSpace(input.Config.Count))
	if err == nil && count > profile.MaxOutputs {
		return BadAuthRequest(fmt.Sprintf("当前图片模型单次最多生成 %d 张", profile.MaxOutputs))
	}
	return nil
}

func qualityForImageValidation(quality string, fallback string) string {
	if strings.EqualFold(quality, "auto") || strings.EqualFold(quality, "any") {
		return fallback
	}
	return firstNonEmpty(quality, fallback)
}

func validateImagePresetSelection(profile *ImageCapabilityConfig, quality, ratio string) error {
	if profile.Size.Parameter != "aspect_ratio" || profile.Size.AllowCustom || len(profile.Size.Presets) == 0 || ratio == "auto" {
		return nil
	}
	tier := imageResolutionTier(quality)
	if tier == "" {
		return nil
	}
	for _, preset := range profile.Size.Presets {
		if preset.Tier == tier && preset.Ratio == ratio {
			return nil
		}
	}
	return BadAuthRequest("当前分辨率不支持所选图片宽高比")
}

func imageResolutionTier(quality string) string {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "1k", "low":
		return "1k"
	case "2k", "medium":
		return "2k"
	case "4k", "high":
		return "4k"
	default:
		return ""
	}
}

func validateWorkflowProviderPromptLength(input canvasGenerationInput) error {
	profile := input.Config.CapabilityConfig
	if input.Mode != "video" || profile == nil || profile.Video == nil {
		return nil
	}
	return validateModelPromptLength("视频", input.Prompt, profile.Video.References.PromptMaxChars)
}

func validateModelPromptLength(label string, prompt string, maxChars int) error {
	if maxChars <= 0 {
		return nil
	}
	actualChars := utf8.RuneCountInString(prompt)
	if actualChars <= maxChars {
		return nil
	}
	return BadAuthRequest(fmt.Sprintf("当前%s模型提示词最多 %d 个字符，完整提示词为 %d 个字符。系统不会自动截断，请精简当前输入、连线内容或技能上下文后重试", label, maxChars, actualChars))
}

func validateGPTImage2CustomSize(value string) error {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "×", "x")))
	if value == "" || value == "auto" {
		return nil
	}
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return errors.New("自定义图片尺寸请使用宽x高，例如 3840x1920")
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return errors.New("图片尺寸必须是正整数")
	}
	if width%16 != 0 || height%16 != 0 {
		return errors.New("图片尺寸宽高必须是 16 的倍数")
	}
	if max(width, height) > 3840 {
		return errors.New("图片尺寸最长边不能超过 3840px")
	}
	if max(width, height) > min(width, height)*3 {
		return errors.New("图片宽高比不能超过 3:1")
	}
	pixels := int64(width) * int64(height)
	if pixels < 655360 || pixels > 8294400 {
		return errors.New("图片总像素需在 655360 到 8294400 之间")
	}
	return nil
}

// videoDurationRangeLabel 把一档时长渲染成用户能读的范围，用于按分辨率档位报错。
func videoDurationRangeLabel(value VideoDurationConfig) string {
	if value.Selection == "enum" {
		parts := make([]string, 0, len(value.Values))
		for _, item := range value.Values {
			if item == -1 {
				parts = append(parts, "自动")
				continue
			}
			parts = append(parts, strconv.Itoa(item))
		}
		return strings.Join(parts, " / ") + " 秒"
	}
	if value.Min == value.Max {
		return strconv.Itoa(value.Min) + " 秒"
	}
	return strconv.Itoa(value.Min) + "-" + strconv.Itoa(value.Max) + " 秒"
}

func videoDurationAllowed(value VideoDurationConfig, seconds int) bool {
	if seconds == -1 {
		return containsInt(value.Values, -1)
	}
	if value.Selection == "enum" {
		return containsInt(value.Values, seconds)
	}
	return seconds >= value.Min && seconds <= value.Max && value.Step > 0 && (seconds-value.Min)%value.Step == 0
}

func videoRatioAllowed(options []string, value string) bool {
	value = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(value, "×", "x")))
	if containsCapabilityString(options, value) {
		return true
	}
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return false
	}
	width, widthErr := strconv.ParseFloat(parts[0], 64)
	height, heightErr := strconv.ParseFloat(parts[1], 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return false
	}
	actual := width / height
	for _, option := range options {
		candidate := ratioValue(option)
		if candidate > 0 && absFloat(candidate-actual)/candidate < 0.01 {
			return true
		}
	}
	return false
}

func ratioValue(value string) float64 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0
	}
	width, widthErr := strconv.ParseFloat(parts[0], 64)
	height, heightErr := strconv.ParseFloat(parts[1], 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return 0
	}
	return width / height
}

func normalizeResolution(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "p")
	if value == "2k" {
		return "1440p"
	}
	if value == "4k" {
		return "2160p"
	}
	return value + "p"
}

func containsCapabilityString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
