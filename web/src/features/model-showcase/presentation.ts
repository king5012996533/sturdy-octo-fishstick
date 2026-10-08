import { creditUnitRateLabel } from "@/lib/credit-price-label";

import type { ShowcaseModel, ShowcasePrice, ShowcaseSpec } from "./api";

/** 能力分组：未知能力归到"其他"，不会从列表里消失。 */
export type ShowcaseCapability = "all" | "image" | "video" | "audio" | "text" | "other";

const CAPABILITY_LABELS: Record<Exclude<ShowcaseCapability, "all">, string> = {
    image: "图片",
    video: "视频",
    audio: "音频",
    text: "文本",
    other: "其他",
};

export function capabilityKey(capability: string): Exclude<ShowcaseCapability, "all"> {
    const value = (capability || "").trim().toLowerCase();
    return value === "image" || value === "video" || value === "audio" || value === "text" ? value : "other";
}

export function capabilityLabel(capability: string): string {
    return CAPABILITY_LABELS[capabilityKey(capability)];
}

export function priceLabel(price: ShowcasePrice): string {
    if (!price.priced || price.sellUnitPrice === null) return "暂不可用";
    return creditUnitRateLabel(price.unit, price.sellUnitPrice);
}

/** 档位名走运营配置，可能已经是中文；只在档位名缺失时兜底成"默认档"。 */
export function tierLabel(priceTier: string): string {
    const tier = (priceTier || "").trim();
    return tier || "默认档";
}

export function unitLabel(unit: string): string {
    switch (unit) {
        case "IMAGE":
            return "张";
        case "SECOND":
            return "秒";
        case "TOKEN_1M":
            return "百万 token";
        case "TOKEN_1K":
            return "千 token";
        default:
            return "次";
    }
}

export type SpecRow = { label: string; value: string };

/**
 * 参数表只翻译，不补值：某一行没有数据就整行不渲染。
 *
 * 渲染一排"—"看起来信息更全，实际是在告诉用户"这个模型什么都不会"。用户按缺值行去选参数，
 * 故障点会落到生成失败上，而广场页面早就给过一个错误暗示。
 */
export function specRows(spec: ShowcaseSpec, capability: string): SpecRow[] {
    const rows: SpecRow[] = [];
    const key = capabilityKey(capability);

    if (spec.ratios.length) rows.push({ label: "画面比例", value: spec.ratios.join(" / ") });
    if (spec.resolutions.length) rows.push({ label: "分辨率", value: spec.resolutions.join(" / ") });
    if (spec.qualityTiers.length) rows.push({ label: "画质档位", value: spec.qualityTiers.join(" / ") });
    // 档位之间有差异时逐档渲染：只写顶层那一行会把"720p 最高 12 秒"吞掉，
    // 用户照着顶层的 10 / 12 / 15 选 720p，故障点会落到生成失败上。
    if (spec.resolutionDurations?.length) {
        for (const tier of spec.resolutionDurations) {
            rows.push({ label: `可选时长（${tier.resolution}）`, value: `${tier.durations.join(" / ")} 秒` });
        }
    } else if (spec.durations.length) {
        rows.push({ label: "可选时长", value: `${spec.durations.join(" / ")} 秒` });
    }
    if (spec.range && spec.range.max > 0) {
        const step = spec.range.step > 0 ? `，${spec.range.step} 秒步进` : "";
        rows.push({ label: "时长范围", value: `${spec.range.min}–${spec.range.max} 秒${step}` });
    }
    if (spec.generateAudio) rows.push({ label: "声音", value: "支持生成音频" });
    if (spec.maxOutputs > 0) rows.push({ label: "单次输出", value: key === "image" ? `最多 ${spec.maxOutputs} 张` : `最多 ${spec.maxOutputs} 个` });
    if (spec.maxReferenceImages > 0) rows.push({ label: "参考图", value: `最多 ${spec.maxReferenceImages} 张` });
    if (spec.maxReferenceVideos > 0) rows.push({ label: "参考视频", value: `最多 ${spec.maxReferenceVideos} 段` });
    return rows;
}

/**
 * 列表卡的副标题：运营定位语优先，其次摘要；两者都没写就返回空串，卡片整行不渲染。
 *
 * 不用统一兜底句：几十张卡都写着同一句话，页面立刻显出模板味，而且那句话没告诉用户
 * 任何事。宁可让卡片短一行，也不批量生产废话。
 */
