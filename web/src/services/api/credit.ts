import { getBillingPlans, type BillingPlan } from "@/services/api/billing";
import { ApiError, http } from "@/services/api/request";
import type { CreateTaskInput } from "@/services/api/task-center";

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

export async function getCreditLedger(options: { page?: number; pageSize?: number; kind?: CreditLedgerKind; refId?: string } = {}) {
    return http.get<CreditLedgerPage>("/finance/ledger", {
        params: { page: options.page, pageSize: options.pageSize, kind: options.kind, refId: options.refId },
    });
}

/**
 * 一次任务在生成前的试算结果。
 *
 * credits 是本次要扣的总额，unit/quantity 是算式里的单位与用量——回传这两项是为了让界面
 * 能写出"15 秒 × 30 积分/秒"这种用户自己就能复核的算式，而不是一个孤零零的数字。
 * priced 为 false 表示模型还没定价，此时界面必须说明原因，不能显示成 0。
 */
export type TaskChargeQuote = {
    credits: number;
    unit: string;
    quantity: number;
    sellUnitPrice: number | null;
    multiplierBp: number;
    multiplierSource: string;
    priced: boolean;
};

export type TaskChargeQuoteResult = {
    quote: TaskChargeQuote;
    wallet: {
        balance: number;
        /** 余额是否够扣这次的钱；由后端判断，前端不自己比大小。 */
        sufficient: boolean;
    };
};

/**
 * 试算一次提交要扣多少积分。
 *
 * 入参与提交任务完全同形：价格必须由后端按同一份模型目录解析。让前端自己算，
 * 或把算好的价带回来，都会在渠道、档位或倍率上与实扣分叉。
 */
export async function quoteTaskCharge(input: CreateTaskInput) {
    return http.post<TaskChargeQuoteResult>("/finance/tasks/quote", input);
}

/**
 * 读出某个任务的真实扣费流水。
 *
 * 界面上的"本次消耗"只能来自这里：报价是"将要扣多少"，流水才是"实际扣了多少、
 * 有没有退"。两者都展示，用户才能看懂一次失败的任务为什么没收钱。
 */
export async function getTaskChargeEntries(taskId: string) {
    const page = await getCreditLedger({ refId: taskId, kind: undefined, pageSize: 20 });
    return page.entries;
}

/** summarizeTaskCharge 把流水折成"实际扣了多少、退了多少"。 */
export function summarizeTaskCharge(entries: CreditLedgerEntry[]) {
    let charged = 0;
    let refunded = 0;
    for (const entry of entries) {
        if (entry.kind === "TASK_CHARGE" && entry.amount < 0) charged += -entry.amount;
        if (entry.kind === "TASK_REFUND" && entry.amount > 0) refunded += entry.amount;
    }
    return { charged, refunded, net: charged - refunded };
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
