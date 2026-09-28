// 平台渠道常常只配了模型名、没配 logo，创作端的模型选择器就会退化成通用 CPU 占位图。
// 这里按模型 id / 名称里的厂商标识兜底到真实品牌图标（@lobehub/icons 的 id）。
// 兜底只影响展示：管理员在后台给模型配置 icon 后，一律以配置为准。
const BRAND_ICON_RULES: Array<[RegExp, string]> = [
    // 先匹配具体模型，再回落到厂商通配，避免 "google/*" 之类的规则抢先吃掉更精确的模型。
    [/nano-?banana/, "NanoBanana"],
    [/seedance|seedream|doubao|jimeng/, "ByteDance"],
    [/flux|black-forest-labs|\bbfl\b/, "Flux"],
    [/hunyuan/, "Hunyuan"],
    [/ideogram/, "Ideogram"],
    [/pixverse/, "PixVerse"],
    [/kling|kwaivgi/, "Kling"],
    [/hailuo/, "Hailuo"],
    [/wan-?video|\bwan-?\d/, "Alibaba"],
    [/photon/, "Luma"],
    [/bria/, "BriaAI"],
    [/pruna/, "PrunaAI"],
    [/recraft/, "Recraft"],
    [/runway/, "Runway"],
    [/midjourney/, "Midjourney"],
    [/stability|\bsdxl\b/, "Stability"],
    [/gpt|dall-?e|sora|openai/, "OpenAI"],
    [/deepseek/, "DeepSeek"],
    [/tencent/, "Tencent"],
    [/luma/, "Luma"],
    [/minimax/, "Minimax"],
    [/bytedance/, "ByteDance"],
    [/google|gemini|imagen|veo/, "Google"],
    [/alibaba|qwen/, "Alibaba"],
    // 下面这组匹配的是渠道名（如"Replicate · 主账号"），品牌列表按渠道分行时给出行标。
    [/replicate/, "Replicate"],
    [/silicon-?flow/, "SiliconCloud"],
    [/\bfal\b/, "Fal"],
    [/openrouter/, "OpenRouter"],
    [/volcengine|volces/, "Volcengine"],
    [/dashscope|bailian/, "Bailian"],
];

/** 返回 @lobehub/icons 的图标 id；识别不出来时返回空串，由调用方决定占位样式。 */
export function modelBrandIconId(model?: string): string {
    const value = (model || "").trim().toLowerCase();
    if (!value) return "";
    return BRAND_ICON_RULES.find(([pattern]) => pattern.test(value))?.[1] || "";
}
