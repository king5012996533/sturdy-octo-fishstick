import { ApiError, http } from "@/services/api/request";

/**
 * 模型广场的公开只读接口。
 *
 * 三个字段组各管一段：slug/displayName/icon 是身份，tagline/summary/highlights 是文案，
 * spec/prices 是能力与价格。spec 与 prices 全部由后端从已发布的能力合同与价目行翻译而来，
 * 前端不再做任何换算——页面上的价必须与账单上的价逐字一致。
 */

export type ShowcasePrice = {
    priceTier: string;
    unit: string;
    sellUnitPrice: number | null;
    priced: boolean;
};

export type ShowcaseDurationRange = {
    min: number;
    max: number;
    step: number;
    value: number;
};

export type ShowcaseSpec = {
    ratios: string[];
    qualityTiers: string[];
    resolutions: string[];
    durations: number[];
    range?: ShowcaseDurationRange;
    generateAudio: boolean;
    maxOutputs: number;
    maxReferenceImages: number;
    maxReferenceVideos: number;
};

export type ShowcaseModel = {
    slug: string;
    displayName: string;
    icon: string;
    capability: string;
    protocol: string;
    tagline: string;
    summary: string;
    highlights: string[];
    sourceUrl: string;
    spec: ShowcaseSpec;
    prices: ShowcasePrice[];
};

/** 列表页：一次取回全部在售模型，筛选与搜索在本地完成。 */
export async function listShowcaseModels(signal?: AbortSignal): Promise<ShowcaseModel[]> {
    const payload = await http.get<{ models: ShowcaseModel[] }>("/public/models", { signal });
    return payload?.models ?? [];
}

/**
 * 详情页：模型不存在时返回 null，由页面渲染"模型不存在"，而不是抛一个用户读不懂的 404 文案。
 *
 * slug 原样拼进路径而不做 encodeURIComponent：上游模型标识自带斜杠（openai/gpt-image-2.5-sunburst），
 * 把它编码成 %2F 会在不同代理上表现不一致，而后端路由本来就是通配匹配斜杠的。
 */
export async function getShowcaseModel(slug: string, signal?: AbortSignal): Promise<ShowcaseModel | null> {
    const trimmed = slug.replace(/^\/+/, "").trim();
    if (!trimmed) return null;
    try {
        const payload = await http.get<{ model: ShowcaseModel }>(`/public/models/${trimmed}`, { signal });
        return payload?.model ?? null;
    } catch (error) {
        if (error instanceof ApiError && error.status === 404) return null;
        throw error;
    }
}
