import { getBillingPlans, type BillingPlan } from "@/services/api/billing";
import { ApiError, http } from "@/services/api/request";

/**
 * 用户端积分接口（余额、流水、充值货架）。
 *
 * 积分口径：余额与流水金额一律是整数，这个整数就是积分本身，页面不做元/分换算，
 * 只做千分位格式化。把积分当小数再乘除，会在对账时丢精度。
 */

export type CreditWallet = {
    userId: string;
    /** 剩余积分；账户不存在时后端给 0，不是错误。 */
    balance: number;
    /** 累计获得 / 累计消耗，供"这个号一共用了多少"这类读数使用。 */
    lifetimeIn: number;
    lifetimeOut: number;
    /** RFC3339；账户从未变动时可能为空串。 */
    updatedAt: string;
};

export type CreditLedgerKind = "TASK_CHARGE" | "TASK_REFUND" | "TOPUP" | "TOPUP_GIFT" | "ADMIN_ADJUST";

export type CreditLedgerEntry = {
    id: string;
    kind: CreditLedgerKind;
    /** 带符号：正数入账、负数出账。方向由符号承担，不依赖配色。 */
    amount: number;
    balanceAfter: number;
    /** 业务引用类型（TASK / ORDER），无业务引用时为 SELF。 */
    refType: string;
    refId: string;
    note: string;
    createdAt: string;
};

export type CreditLedgerPage = {
    entries: CreditLedgerEntry[];
    total: number;
    page: number;
    pageSize: number;
};

/** 充值档位 = 套餐 + 两个积分字段；只有带积分的套餐才上充值货架。 */
export type CreditTopUpPlan = BillingPlan & {
    /** 购买后到账的积分（不含赠送）。 */
    credits: number;
    /** 平台额外赠送的积分，与到账积分分开入账、分开展示。 */
    giftCredits: number;
};

export async function getCreditWallet() {
    const payload = await http.get<{ wallet: CreditWallet }>("/finance/wallet");
    return payload.wallet;
}

export async function getCreditLedger(options: { page?: number; pageSize?: number; kind?: CreditLedgerKind } = {}) {
    return http.get<CreditLedgerPage>("/finance/ledger", {
        params: { page: options.page, pageSize: options.pageSize, kind: options.kind },
    });
}

function creditField(value: unknown) {
    return typeof value === "number" && Number.isFinite(value) ? Math.trunc(value) : 0;
}

/**
 * 充值货架 = 在售的「纯积分包」，按后端给的顺序排。
 *
 * 平台只卖积分：下单只加余额、不开订阅，所以这里必须要求 periodDays 为 0。
 * 带周期的套餐（periodDays > 0）是订阅商品，一旦出现在这个货架上，用户会买到一件
 * 界面上看不见的东西——订阅态在积分中心里没有任何展示位，等于静默扣一笔账。
 *
 * 积分字段仍过一遍 creditField：服务端版本落后时字段会是 undefined，直接参与
 * `credits + giftCredits > 0` 会得到 NaN 而被静默过滤掉——那正是我们要的结果
 * （劣化为"暂无可购买的积分包"），但保留这次归一化能让意图显式，而不是靠 NaN 的巧合。
 */
export async function getCreditTopUpPlans(): Promise<CreditTopUpPlan[]> {
    const plans = await getBillingPlans();
    return plans
        .map((plan) => ({ ...plan, credits: creditField(plan.credits), giftCredits: creditField(plan.giftCredits) }))
        .filter((plan) => plan.periodDays === 0 && plan.credits + plan.giftCredits > 0)
        .sort((left, right) => left.sortOrder - right.sortOrder || left.priceFen - right.priceFen);
}

/** 余额不足是 402 + reason=insufficient_credits：据此把用户引到充值，而不是"联系客服"。 */
export function isInsufficientCredits(error: unknown) {
    if (!(error instanceof ApiError)) return false;
    return error.reason === "insufficient_credits" || error.code === 40201 || error.status === 402;
}
