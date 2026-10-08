import { formatCount } from "@/lib/format-usage";

import type { AdminCreditKind, AdminCreditLedgerEntry } from "./api";

/**
 * 积分展示的公共口径。
 *
 * 后台有两个地方要展示同一份账：积分管理页，以及用户管理里为单个用户开的积分抽屉。
 * 文案、配色、符号约定各写一份时，两处会对同一笔流水给出不同的样子，运营第一反应
 * 是"哪边算错了"。这里集中一份，两个面板都从这里取。
 */

export const creditKindOptions = [
    { value: "", label: "全部类型" },
    { value: "TASK_CHARGE", label: "任务扣费" },
    { value: "TASK_REFUND", label: "任务退款" },
    { value: "TASK_SETTLE", label: "文本结算" },
    { value: "TOPUP", label: "充值到账" },
    { value: "TOPUP_GIFT", label: "充值赠送" },
    { value: "ADMIN_ADJUST", label: "人工调整" },
];

export const creditKindViews: Record<AdminCreditKind, { label: string; color: string }> = {
    TASK_CHARGE: { label: "任务扣费", color: "volcano" },
    TASK_REFUND: { label: "任务退款", color: "blue" },
    TASK_SETTLE: { label: "文本结算", color: "orange" },
    TOPUP: { label: "充值到账", color: "green" },
    TOPUP_GIFT: { label: "充值赠送", color: "cyan" },
    ADMIN_ADJUST: { label: "人工调整", color: "gold" },
};

export const creditPositiveInk = "#9ceac4";
export const creditNegativeInk = "#ffb4b4";

/** 带符号展示：方向由正负号承担，颜色只是辅助，打印或截图黑白时也不会看反。 */
export function formatSignedCredits(value: number) {
    const amount = Number.isFinite(value) ? Math.trunc(value) : 0;
    const sign = amount > 0 ? "+" : amount < 0 ? "-" : "";
    return `${sign}${formatCount(Math.abs(amount))}`;
}

export function creditSignedInk(value: number) {
    if (value > 0) return creditPositiveInk;
    if (value < 0) return creditNegativeInk;
    return "var(--admin-ink-faint)";
}

/** 无外部单据的流水只回 SELF，那是本系统自己产生的账，展示出来只会变成噪声。 */
export function creditRefLabel(entry: AdminCreditLedgerEntry) {
    if (!entry.refId || entry.refType === "SELF") return "";
    return `${entry.refType} ${entry.refId}`;
}
