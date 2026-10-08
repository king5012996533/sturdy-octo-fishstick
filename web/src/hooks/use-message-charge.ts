import { useEffect, useMemo, useRef } from "react";
import { useQuery } from "@tanstack/react-query";

import { getTaskChargeEntriesForTasks, summarizeTaskCharge, type CreditLedgerEntry } from "@/services/api/credit";

/**
 * 一条消息（一个或多个任务）的真实扣费读数。
 *
 * 数字只认流水：预扣在提交时就落了账，退回由服务端按"上游到底收没收到请求"判定，
 * 界面自己记一份账迟早会跟用户钱包对不上。entries 为空表示"查过，没有扣费记录"，
 * 不是"扣了 0 积分"——这两种情况在界面上必须长得不一样。
 */
export type MessageCharge = {
    entries: CreditLedgerEntry[];
    /** 累计预扣、累计退回；net = charged - refunded 才是这次真正花掉的钱。 */
    charged: number;
    refunded: number;
    net: number;
    loading: boolean;
    error: string;
};

// 失败任务的退回流水可能比任务终态晚一瞬落地，短窗口内多查几次。窗口结束就停，
// 不做无限轮询：成功任务"扣了没退"是它的正常终局，盯着看没有意义。
const SETTLE_WINDOW_MS = 20_000;
// 文本的按 token 补扣也是在任务收尾之后才落账，但它比退款更加"立刻"——结算就写在
// 成功收尾那一刻，窗口只用来兜住"任务状态先到、流水后到"的毫秒级错位。
const TEXT_SETTLE_WINDOW_MS = 8_000;
const SETTLE_INTERVAL_MS = 2_500;

export function useMessageCharge(taskIds: string[], options: { enabled: boolean; expectRefund?: boolean; expectSettle?: boolean }): MessageCharge {
    const ids = useMemo(() => Array.from(new Set(taskIds.map((taskId) => taskId.trim()).filter(Boolean))), [taskIds]);
    const idsKey = ids.join(",");
    const settleUntil = useRef(0);
    useEffect(() => {
        const window = Math.max(options.expectRefund ? SETTLE_WINDOW_MS : 0, options.expectSettle ? TEXT_SETTLE_WINDOW_MS : 0);
        settleUntil.current = window > 0 ? Date.now() + window : 0;
    }, [idsKey, options.expectRefund, options.expectSettle]);
    const query = useQuery({
        queryKey: ["message-charge", idsKey],
        queryFn: () => getTaskChargeEntriesForTasks(ids),
        enabled: options.enabled && ids.length > 0,
        // 会变的只有两类晚到的流水：失败退回，以及文本任务收尾时的按用量补扣。
        // 两者之外的数（预扣）在提交时就定死了，所以缓存可以长一点。
        staleTime: 30_000,
        refetchInterval: (current) => {
            if (Date.now() >= settleUntil.current) return false;
            const entries = current.state.data ?? [];
            // 短对话的补扣可能根本不存在（起步价已经盖住用量），此时靠窗口超时收尾。
            if (options.expectRefund && entries.some((entry) => entry.kind === "TASK_REFUND")) return false;
            if (options.expectSettle && entries.some((entry) => entry.kind === "TASK_SETTLE")) return false;
            return SETTLE_INTERVAL_MS;
        },
    });
    return {
        entries: query.data ?? [],
        ...summarizeTaskCharge(query.data ?? []),
        loading: query.isPending,
        error: query.error instanceof Error ? query.error.message : "",
    };
}
