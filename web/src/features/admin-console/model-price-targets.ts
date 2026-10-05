import { listAdminChannelModels, listAdminChannels } from "./api";
import type { ModelPriceCapability } from "./api-pricing";

/**
 * 可定价的模型目录。
 *
 * 价目表的一行认的是渠道模型标识（`CHANNEL_x::model_key`），而运营脑子里记的是
 * "Replicate 下的 MiniMax Music 2.5 配乐"。这份目录把两者对上：调价因此从"背标识"
 * 变成"选模型"。缺了它，定价表单就只是一格占位符写着 gpt-4o 的文本框——本平台的模型
 * 标识没有一个长得像 gpt-4o。
 */
export type ModelPriceTarget = {
    /** 价目表主键，与后端拼的同一串。 */
    fullKey: string;
    channelId: string;
    /** 渠道对外显示的名字；有公开别名时用别名，运营在后台看到的也是它。 */
    channelName: string;
    modelKey: string;
    displayName: string;
    capability: ModelPriceCapability;
    /** 渠道与模型都启用才算可选：停用的模型定出来的价永远不会被计费读到。 */
    enabled: boolean;
};

/** 价目行的主键格式：`渠道 ID::平台模型标识`。后端 taskChargeModelKey 拼的是同一串。 */
export function channelModelFullKey(channelId: string, modelKey: string): string {
    return `${channelId.trim()}::${modelKey.trim()}`;
}

/**
 * 渠道模型的能力是小写（image/video/audio/text），价目表用大写枚举。
 *
 * 认不出来的一律返回 null 由调用方跳过：让运营选到一个定价时必定被服务端拒掉的模型，
 * 比根本不显示它更糟——他会以为自己配好了。
 */
export function toPriceCapability(capability: string): ModelPriceCapability | null {
    const normalized = capability.trim().toUpperCase();
    if (normalized === "TEXT" || normalized === "IMAGE" || normalized === "VIDEO" || normalized === "AUDIO") {
        return normalized;
    }
    return null;
}

/** 渠道 + 模型名：运营在这张表里认的就是这两个东西。 */
export function modelPriceTargetName(target: ModelPriceTarget): string {
    const name = target.displayName.trim() || target.modelKey;
    return `${target.channelName} · ${name}`;
}

/**
 * 一行式文案：通知条、提示语这类地方用。
 *
 * 下拉里不用它——拼成一行会被输入框的宽度截成「Replicate · 主账…」，所以选项拆成两行
 * （名字在上、完整标识在下）。
 */
export function modelPriceTargetLabel(target: ModelPriceTarget): string {
    return `${modelPriceTargetName(target)} · ${target.modelKey}`;
}

/** 搜索用的一整串：渠道名、展示名、模型标识都要能命中，否则运营只能按标识搜。 */
export function modelPriceTargetSearchText(target: ModelPriceTarget): string {
    return `${target.channelName} ${target.displayName} ${target.modelKey} ${target.fullKey}`.toLowerCase();
}

/**
 * 拉全部渠道，再逐个取模型。
 *
 * 渠道是个位数，一次全量比给每个模型单开一个"候选列表"接口简单。模型量再涨一个数量级
 * 时应当改成服务端一次返回，而不是继续在这里并发 N 个请求。
 */
export async function loadModelPriceTargets(): Promise<ModelPriceTarget[]> {
    const page = await listAdminChannels({ page: 1, pageSize: 200 });
    const channels = page.channels ?? [];
    const groups = await Promise.all(
        channels.map(async (channel) => {
            const payload = await listAdminChannelModels(channel.id);
            return (payload.models ?? []).flatMap((model): ModelPriceTarget[] => {
                const capability = toPriceCapability(model.capability);
                if (!capability) return [];
                return [
                    {
                        fullKey: channelModelFullKey(channel.id, model.modelKey),
                        channelId: channel.id,
                        channelName: channel.publicAlias?.trim() || channel.name,
                        modelKey: model.modelKey,
                        displayName: model.displayName ?? "",
                        capability,
                        enabled: Boolean(channel.enabled) && Boolean(model.enabled),
                    },
                ];
            });
        }),
    );
    return groups
        .flat()
        .sort((left, right) => left.channelName.localeCompare(right.channelName, "zh-Hans-CN") || left.modelKey.localeCompare(right.modelKey));
}
