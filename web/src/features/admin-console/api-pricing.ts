import { http } from "@/services/api/request";

/**
 * 模型定价与加价倍率接口（/api/admin/billing/markup、/api/admin/billing/model-prices）。
 *
 * 与 api-templates.ts 一样，这一层只做「类型 + 路径」映射：倍率怎么解析、单价按哪个单位
 * 结算、试算结果准不准，全部由服务端一处判定；前端重复一份只会出现"页面算出来 1.2、
 * 实际扣费 1.18"这类对不上的账。
 *
 * 金额口径：单价都是「分」为单位的整数占位，不在这里做元/分换算，也不做 ×100 —— 换算
 * 一旦散落到展示层，四舍五入的差异就会变成真实的少收/多收。
 */

/** 模型能力：与前台模型目录用的是同一套枚举。 */
export type ModelPriceCapability = "TEXT" | "IMAGE" | "VIDEO" | "AUDIO";

/**
 * 计价单位：文本按百万 token，图片按张，视频按秒，其余按次。
 *
 * 文本用「分/百万 token」而不是「分/千 token」，是因为金额列是整数分：官方最便宜的档位
 * 是 0.02 元/百万 token，折算成"分/千 token"是 0.02，整数存不下，只能被迫向上取整到
 * 1 分——那等于把 ¥0.02 按 ¥10 卖。TOKEN_1K 只为兼容旧配置保留。
 */
export type ModelPriceUnit = "TOKEN_1M" | "TOKEN_1K" | "IMAGE" | "SECOND" | "REQUEST";

/**
 * 价格档位：同一模型、同一能力下"这次调用按哪一行价结算"的键。
 *
 * 取值集合由能力决定，服务端会严格校验，配错直接 400：
 * - TEXT 按 token 性质分三档（缓存命中 / 未命中 / 输出），差距可达两个数量级，必须齐备；
 * - IMAGE 按上游 quality 分低 / 中 / 高三档，另允许留空表示"这个模型不区分质量"；
 * - AUDIO 按输出时长分短 / 中 / 长三档，另允许留空表示"这个音频模型不看时长"
 *   （配音与整首歌由上游定长，只有能按秒指定时长的音乐模型才需要配三行）；
 * - VIDEO 只有一个价，档位留空。
 *
 * 图片的空档不是任何一档的别名：它代表"面板没有指定质量"时的价（上游按 auto 计费）。
 * 音频的空档同样是兜底价——旧前端不带时长时上游会按缺省产出 60 秒，那一行要按中档配。
 *
 * MEDIUM 是图片与音频共用的词，含义随能力变化：图片是中质量，音频是中等时长。
 */
export type ModelPricePriceTier = "" | "CACHE" | "INPUT" | "OUTPUT" | "LOW" | "MEDIUM" | "HIGH" | "SHORT" | "LONG";

export type ModelPrice = {
    id: string;
    modelKey: string;
    capability: ModelPriceCapability;
    priceTier: ModelPricePriceTier;
    unit: ModelPriceUnit;
    vendorCode: string;
    /** null = 还没定价（不是 0）：上游价格没回填时不能当成免费。 */
    upstreamUnitPrice: number | null;
    /** null = 没有单条售价，实际售价按倍率解析结果计算。 */
    sellUnitPrice: number | null;
    multiplierBp: number | null;
    currency: string;
    enabled: boolean;
    note: string;
    createdAt: string;
    updatedAt: string;
};

/** 倍率作用域：全局 → 能力 → 厂商 → 模型，逐级收窄。 */
export type MarkupScope = "GLOBAL" | "CAPABILITY" | "VENDOR" | "MODEL";

export type MarkupRule = {
    id: string;
    scope: MarkupScope;
    /** 作用域对应的目标：GLOBAL 用 *，其余填能力/厂商 code/模型标识。 */
    target: string;
    /** 万分比：12000 = ×1.2。 */
    multiplierBp: number;
    /** 服务端给的展示用倍率，避免前端各写一份 bp→倍率 的换算。 */
    multiplier: string;
    note: string;
};

export type MarkupRuleInput = {
    scope: MarkupScope;
    target: string;
    multiplierBp: number;
    note: string;
};

export type PricingResolution = {
    multiplierBp: number;
    /** 命中规则的来源描述，例如「MODEL gpt-4o」或「GLOBAL 默认」。 */
    source: string;
    sellUnitPrice: number | null;
    /** false 表示上游单价为空、算不出售价。 */
    priced: boolean;
};

export type ModelPriceInput = {
    modelKey: string;
    capability: ModelPriceCapability;
    /** TEXT 与 IMAGE 都保留用户选的档位；VIDEO / AUDIO 必须为空。 */
    priceTier: ModelPricePriceTier;
    unit: ModelPriceUnit;
    vendorCode: string;
    /** 空值用 null 提交：0 与「未定价」在计费上是两件事。 */
    upstreamUnitPrice: number | null;
    sellUnitPrice: number | null;
    multiplierBp: number | null;
    currency?: string;
    enabled?: boolean;
    note?: string;
};

export type PricingPreviewInput = {
    modelKey: string;
    vendorCode: string;
    capability: ModelPriceCapability;
    /** 必须带上档位：分档的价格在库里是每档一行，不带档位查不到任何一条。 */
    priceTier: ModelPricePriceTier;
    /** null 表示还没填上游单价，服务端会直接判为未定价。 */
    upstreamUnitPrice: number | null;
};

export function getAdminBillingMarkup() {
    return http.get<{ rules: MarkupRule[]; defaultMultiplierBp: number }>("/admin/billing/markup");
}

/** 倍率是整表提交：规则之间有覆盖关系，逐条改会让中间态被并发读到。 */
export function updateAdminBillingMarkup(rules: MarkupRuleInput[]) {
    return http.put<{ rules: MarkupRule[]; defaultMultiplierBp: number }>("/admin/billing/markup", { rules });
}

export function listAdminModelPrices() {
    return http.get<{ prices: ModelPrice[] }>("/admin/billing/model-prices");
}

export function createAdminModelPrice(input: ModelPriceInput) {
    return http.post<{ price: ModelPrice }>("/admin/billing/model-prices", input);
}

export function updateAdminModelPrice(id: string, input: ModelPriceInput) {
    return http.put<{ price: ModelPrice }>(`/admin/billing/model-prices/${encodeURIComponent(id)}`, input);
}

export function deleteAdminModelPrice(id: string) {
    return http.delete<{ deleted: true }>(`/admin/billing/model-prices/${encodeURIComponent(id)}`);
}

/** 试算不写库：只是把"这条模型最终按哪个倍率、卖多少钱"提前算给运营看。 */
export function previewAdminModelPrice(input: PricingPreviewInput) {
    return http.post<{ resolution: PricingResolution }>("/admin/billing/model-prices/preview", input);
}
