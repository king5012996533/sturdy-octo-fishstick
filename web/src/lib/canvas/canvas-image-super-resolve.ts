import { MAX_UPSCALE_LONG_EDGE } from "@/lib/canvas/canvas-image-data";
import { resolveModelChannel, type AiConfig } from "@/stores/use-config-store";

/**
 * AI 超分（fal Recraft Crisp）的固定调用口径。
 *
 * 超分不是"用户在模型下拉里挑一个模型"，而是画布上的一个动作：一个端点、一张输入、
 * 一个固定价。因此模型标识写死在这里，不跟随用户的全局模型选择；渠道仍然从目录里取——
 * 这条模型由平台登记在 fal 渠道上，换账号或换渠道时前端不用改代码。
 */
export const SUPER_RESOLVE_MODEL = "fal-ai/recraft/upscale/crisp";

/**
 * 上游只接受 image_url，不接受提示词；这段文案只用于满足任务侧的非空校验，
 * 插件不会把它拼进请求体（见 plugin-packages/fal-upscale/docs/interface.md）。
 */
export const SUPER_RESOLVE_PROMPT = "AI 超分";

/** 上游 Crisp 写死的放大倍率，没有参数可调。 */
export const SUPER_RESOLVE_FACTOR = 4;

/**
 * 一次超分实际交付给用户的尺寸。
 *
 * 上游一律放大 4 倍，但结果是无损 WebP：1024 的原图回来 4096² 就有十几兆，再大就是按 4 倍
 * 膨胀，既吃磁盘又拖垮画布渲染。所以落库前按画布能接受的长边（与本地放大同一个上界）收口，
 * 而这层收口只在放大结果越界时才生效——对 1024 及以下的原图，交付的就是完整的 4 倍。
 *
 * 倍率必须按收口后的结果算：界面上写死"4×"、实际给人一张尺寸没变的图，是另一种欺骗。
 */
export function resolveSuperResolveOutputSize(width: number, height: number) {
    const longEdge = Math.max(1, width, height);
    const scale = Math.min(1, MAX_UPSCALE_LONG_EDGE / (longEdge * SUPER_RESOLVE_FACTOR));
    return {
        width: Math.max(1, Math.round(width * SUPER_RESOLVE_FACTOR * scale)),
        height: Math.max(1, Math.round(height * SUPER_RESOLVE_FACTOR * scale)),
        factor: SUPER_RESOLVE_FACTOR * scale,
        /** 放大结果超过了画布上界、被缩回来过。界面要据此说明，不能一律宣称 4 倍。 */
        capped: scale < 1,
    };
}

/**
 * 把用户的全局配置收敛成这次超分调用的配置；平台目录里没有这条模型时返回 null。
 *
 * 返回 null 而不是回落到当前选中的图片模型：超分是一条独立的付费端点，落到别的模型上
 * 要么报错、要么按别的模型的价格扣钱，两种都不是用户点下按钮时预期的事。
 */
export function resolveSuperResolveConfig(config: AiConfig): AiConfig | null {
    const channel = resolveModelChannel(config, SUPER_RESOLVE_MODEL);
    if (!channel.models.includes(SUPER_RESOLVE_MODEL)) return null;
    return {
        ...config,
        // 超分节点不参与工作流执行器：它是平台的系统渠道能力，与用户的 RunningHub 配置无关。
        taskWorkflowProvider: "model",
        model: SUPER_RESOLVE_MODEL,
        imageModel: SUPER_RESOLVE_MODEL,
        // 渠道模型声明了 size.parameter=none、quality 不支持、只出 1 张，
        // 这三个值会被后端的能力合同再收敛一次；写在这里是为了让报价与提交用同一份输入。
        count: "1",
        size: "auto",
        quality: "auto",
        transparentBackground: "false",
    };
}
