import { resolveModelChannel, type AiConfig } from "@/stores/use-config-store";

/**
 * 移除背景（fal BiRefNet v2）的固定调用口径。
 *
 * 与 AI 超分同一套形态：不是"用户在模型下拉里挑一个模型"，而是画布上的一个动作——
 * 一个端点、一张输入、一个固定价。因此模型标识写死在这里，不跟随用户的全局模型选择；
 * 渠道仍然从目录里取，换账号或换渠道时前端不用改代码。
 */
export const REMOVE_BACKGROUND_MODEL = "fal-ai/birefnet/v2";

/**
 * 上游只接受 image_url，不接受提示词；这段文案只用于满足任务侧的非空校验，
 * 插件不会把它拼进请求体（见 plugin-packages/fal-remove-background/docs/interface.md）。
 */
export const REMOVE_BACKGROUND_PROMPT = "移除背景";

/**
 * 把用户的全局配置收敛成这次抠图调用的配置；平台目录里没有这条模型时返回 null。
 *
 * 返回 null 而不是回落到当前选中的图片模型：抠图是一条独立的付费端点，落到别的模型上
 * 要么报错、要么按别的模型的价格扣钱，两种都不是用户点下按钮时预期的事。
 */
export function resolveRemoveBackgroundConfig(config: AiConfig): AiConfig | null {
    const channel = resolveModelChannel(config, REMOVE_BACKGROUND_MODEL);
    if (!channel.models.includes(REMOVE_BACKGROUND_MODEL)) return null;
    return {
        ...config,
        // 抠图节点不参与工作流执行器：它是平台的系统渠道能力，与用户的 RunningHub 配置无关。
        taskWorkflowProvider: "model",
        model: REMOVE_BACKGROUND_MODEL,
        imageModel: REMOVE_BACKGROUND_MODEL,
        // 渠道模型声明了 size.parameter=none、quality 不支持、只出 1 张，这三个值会被后端
        // 的能力合同再收敛一次；写在这里是为了让报价与提交用同一份输入。
        // transparentBackground 走同一个口径：它描述的是"要不要向上游申请透明底"，而这条
        // 端点永远返回带 alpha 的图，没有这个可调项。
        count: "1",
        size: "auto",
        quality: "auto",
        transparentBackground: "false",
    };
}
