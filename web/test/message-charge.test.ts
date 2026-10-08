import { describe, expect, test } from "bun:test";

import { summarizeTaskCharge, type CreditLedgerEntry } from "../src/services/api/credit";

/**
 * 消息下方"本次消耗"的算法。
 *
 * 这里是唯一允许把流水折成一个数字的地方：折错了（比如把退回算成又一次消耗）会在界面上
 * 显示一个用户一算就对不上的数，而余额本身是对的，所以这类偏差只会以客诉的形式暴露。
 */
function entry(overrides: Partial<CreditLedgerEntry>): CreditLedgerEntry {
    return {
        id: "entry-1",
        kind: "TASK_CHARGE",
        amount: -150,
        balanceAfter: 850,
        refType: "TASK",
        refId: "task-1",
        note: "",
        createdAt: "2026-09-30T10:00:00Z",
        ...overrides,
    };
}

describe("summarizeTaskCharge", () => {
    test("预扣加全额退回：净消耗为 0，但退回额要单独留痕", () => {
        const summary = summarizeTaskCharge([
            entry({ id: "charge", kind: "TASK_CHARGE", amount: -150 }),
            entry({ id: "refund", kind: "TASK_REFUND", amount: 150 }),
        ]);
        expect(summary).toEqual({ charged: 150, refunded: 150, net: 0 });
    });

    test("只有预扣时净消耗就是预扣额", () => {
        const summary = summarizeTaskCharge([entry({ id: "charge", amount: -900 })]);
        expect(summary).toEqual({ charged: 900, refunded: 0, net: 900 });
    });

    test("充值、后台调整这类同账号流水不算进本次生成", () => {
        const summary = summarizeTaskCharge([
            entry({ id: "topup", kind: "TOPUP", amount: 10_000 }),
            entry({ id: "adjust", kind: "ADMIN_ADJUST", amount: -50 }),
            entry({ id: "charge", kind: "TASK_CHARGE", amount: -45 }),
        ]);
        expect(summary).toEqual({ charged: 45, refunded: 0, net: 45 });
    });

    test("部分退回：净消耗是差额", () => {
        const summary = summarizeTaskCharge([
            entry({ id: "charge", kind: "TASK_CHARGE", amount: -900 }),
            entry({ id: "refund", kind: "TASK_REFUND", amount: 300 }),
        ]);
        expect(summary).toEqual({ charged: 900, refunded: 300, net: 600 });
    });

    // 文本按 token 结算，一条消息因此会有两条出账流水：提交时的起步价，以及收尾时的补扣。
    // 只认预扣那一笔，界面会显示"本次消耗 1 积分"而钱包实际少了十几分——用户对不上账，
    // 而这恰恰是这次改动要解决的资损场景。
    test("文本按 token 的补扣与预扣同向，一起算进本次消耗", () => {
        const summary = summarizeTaskCharge([
            entry({ id: "charge", kind: "TASK_CHARGE", amount: -1 }),
            entry({ id: "settle", kind: "TASK_SETTLE", amount: -16, note: "文本按 token 结算" }),
        ]);
        expect(summary).toEqual({ charged: 17, refunded: 0, net: 17 });
    });

    test("补扣流水为 0 或反向时不该被当成退款", () => {
        const summary = summarizeTaskCharge([
            entry({ id: "charge", kind: "TASK_CHARGE", amount: -1 }),
            entry({ id: "settle", kind: "TASK_SETTLE", amount: 5 }),
        ]);
        expect(summary).toEqual({ charged: 1, refunded: 0, net: 1 });
    });
});
