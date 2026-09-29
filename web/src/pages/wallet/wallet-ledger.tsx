import { Button, Select, Skeleton } from "antd";
import { CircleDollarSign, CreditCard, Gift, RotateCcw, SlidersHorizontal, Zap, type LucideIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { PaginationBar, TableSurface } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceState } from "@/components/layout/workspace-state";
import { formatCount, formatDateTime } from "@/lib/format-usage";
import { cn } from "@/lib/utils";
import { getCreditLedger, type CreditLedgerEntry, type CreditLedgerKind } from "@/services/api/credit";

import { WalletPanel, errorMessage, formatCreditDelta, useDelayedLoading } from "./wallet-kit";

/**
 * Zone D —— 流水区。
 *
 * 台账要能被逐行比对，所以桌面用真表格（列头与数据通过 th/scope 关联）、金额列右对齐
 * 等宽；窄屏不做横向滚动，改为把「时间 · 变动后余额」压进类型列的第二行。
 */

const kindMeta: Record<CreditLedgerKind, { label: string; icon: LucideIcon }> = {
    TASK_CHARGE: { label: "任务扣费", icon: Zap },
    TASK_REFUND: { label: "任务退回", icon: RotateCcw },
    TOPUP: { label: "充值到账", icon: CreditCard },
    TOPUP_GIFT: { label: "赠送积分", icon: Gift },
    ADMIN_ADJUST: { label: "后台调整", icon: SlidersHorizontal },
};

/* 后端以后加新 kind 时，这里不能变成空白行：认不出来的类型原样展示，图标退回中性。 */
function kindView(kind: string) {
    return (kindMeta as Record<string, { label: string; icon: LucideIcon } | undefined>)[kind] ?? { label: kind || "积分变动", icon: CircleDollarSign };
}

const kindOptions = [
    { value: "", label: "全部类型" },
    ...(Object.keys(kindMeta) as CreditLedgerKind[]).map((kind) => ({ value: kind, label: kindMeta[kind].label })),
];

const headCellClass = "px-4 py-2.5 text-[var(--fs-label)] font-medium text-foreground/58";
const bodyCellClass = "px-4 py-3 align-top";

/** 业务引用只在有真实引用时展示：SELF 是"这笔变动没有外部单据"，ID 就是它自己，写出来只是噪声。 */
function referenceOf(entry: CreditLedgerEntry) {
    if (!entry.refId || entry.refType === "SELF") return "";
    return entry.refType ? `${entry.refType} · ${entry.refId}` : entry.refId;
}

function LedgerRow({ entry }: { entry: CreditLedgerEntry }) {
    const view = kindView(entry.kind);
    const Icon = view.icon;
    const reference = referenceOf(entry);
    const incoming = entry.amount > 0;
    return (
        <tr className="border-b border-[var(--workspace-border)] transition-colors last:border-b-0 hover:bg-surface-hover">
            <td className={cn("hidden font-mono text-[var(--fs-caption)] tabular-nums text-foreground/58 sm:table-cell", bodyCellClass)}>{formatDateTime(entry.createdAt)}</td>
            <td className={bodyCellClass}>
                <span className="flex items-center gap-2 text-[var(--fs-body)] text-foreground">
                    <Icon aria-hidden strokeWidth={1.75} className="size-4 shrink-0 text-foreground/45" />
                    <span className="min-w-0 truncate">{view.label}</span>
                </span>
                <span className="mt-0.5 block font-mono text-[var(--fs-tiny)] tabular-nums text-foreground/45 sm:hidden">
                    {formatDateTime(entry.createdAt)} · 余额 {formatCount(entry.balanceAfter)}
                </span>
            </td>
            <td className={cn(bodyCellClass, "min-w-0")}>
                <span className="line-clamp-2 block max-w-[38ch] text-[var(--fs-caption)] leading-relaxed text-foreground/70" title={entry.note}>
                    {entry.note || "—"}
                </span>
                {reference ? (
                    <span className="mt-0.5 block max-w-[38ch] truncate font-mono text-[var(--fs-tiny)] text-foreground/45" title={reference}>
                        {reference}
                    </span>
                ) : null}
            </td>
            {/* 入账用语义绿、出账保持墨色：日常扣费占绝大多数，把每一笔都涂红既超预算也失去信息量。 */}
            <td className={cn(bodyCellClass, "text-right font-mono text-[var(--fs-body)] tabular-nums", incoming ? "text-status-success" : "text-foreground")}>{formatCreditDelta(entry.amount)}</td>
            <td className={cn("hidden text-right font-mono text-[var(--fs-caption)] tabular-nums text-foreground/58 sm:table-cell", bodyCellClass)}>{formatCount(entry.balanceAfter)}</td>
        </tr>
    );
}

export function CreditLedgerSection({ revision, onTopUp }: { revision: number; onTopUp: () => void }) {
    const [kind, setKind] = useState<CreditLedgerKind | "">("");
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [entries, setEntries] = useState<CreditLedgerEntry[]>([]);
    const [total, setTotal] = useState(0);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [reloadKey, setReloadKey] = useState(0);
    const showSkeleton = useDelayedLoading(loading);

    useEffect(() => {
        let cancelled = false;
        setLoading(true);
        setError("");
        getCreditLedger({ page, pageSize, kind: kind || undefined })
            .then((result) => {
                if (cancelled) return;
                setEntries(result.entries ?? []);
                setTotal(result.total ?? 0);
            })
            .catch((cause) => {
                if (cancelled) return;
                setEntries([]);
                setTotal(0);
                setError(errorMessage(cause, "积分流水加载失败"));
            })
            .finally(() => {
                if (!cancelled) setLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [page, pageSize, kind, reloadKey, revision]);

    return (
        <section className="mt-8" aria-label="积分流水">
            <div className="flex flex-wrap items-end justify-between gap-3">
                <div className="min-w-0">
                    <h2 className="font-[family-name:var(--font-display)] text-[var(--fs-heading-lg)] font-semibold leading-[1.35] text-foreground">积分流水</h2>
                    <p className="mt-1 text-[var(--fs-caption)] leading-relaxed text-foreground/58">按时间倒序记录每一次扣费、退回与充值。</p>
                </div>
                <Select
                    className="w-36"
                    value={kind}
                    options={kindOptions}
                    aria-label="按类型筛选流水"
                    onChange={(value: CreditLedgerKind | "") => {
                        setKind(value);
                        // 换筛选条件等于换一份结果集，停在第 3 页会看到空列表而不是新结果的第一页。
                        setPage(1);
                    }}
                />
            </div>

            {showSkeleton && !entries.length ? (
                <div className="mt-4 rounded-lg bg-surface p-4">
                    <Skeleton active title={{ width: "30%" }} paragraph={{ rows: 5 }} />
                </div>
            ) : null}

            {!loading && error ? <WorkspaceErrorState compact title="积分流水加载失败" description={error} onRetry={() => setReloadKey((value) => value + 1)} /> : null}

            {/* 空态要能区分"还没有任何记录"和"筛选后没有结果"：后者必须回显条件并给出口。 */}
            {!loading && !error && !entries.length ? (
                kind ? (
                    <WalletPanel className="mt-4">
                        <h3 className="font-[family-name:var(--font-display)] text-[var(--fs-heading)] font-semibold text-foreground">没有符合筛选的流水</h3>
                        <p className="mt-1 text-[var(--fs-caption)] leading-relaxed text-foreground/58">当前筛选：{kindMeta[kind].label}。换一个类型，或清除筛选看全部记录。</p>
                        <div className="mt-4">
                            <Button
                                onClick={() => {
                                    setKind("");
                                    setPage(1);
                                }}
                            >
                                清除筛选
                            </Button>
                        </div>
                    </WalletPanel>
                ) : (
                    <WorkspaceState
                        compact
                        icon="empty"
                        title="还没有积分记录"
                        description="充值到账、任务扣费与失败退回都会在这里留下凭据，方便随时对账。"
                        action={
                            <Button type="primary" onClick={onTopUp}>
                                去充值
                            </Button>
                        }
                    />
                )
            ) : null}

            {entries.length ? (
                <>
                    <TableSurface>
                        <table className="w-full border-collapse text-left">
                            <caption className="sr-only">积分流水，按时间倒序</caption>
                            <thead>
                                <tr className="border-b border-[var(--workspace-border)]">
                                    <th scope="col" className={cn("hidden sm:table-cell", headCellClass)}>
                                        时间
                                    </th>
                                    <th scope="col" className={headCellClass}>
                                        类型
                                    </th>
                                    <th scope="col" className={headCellClass}>
                                        说明
                                    </th>
                                    <th scope="col" className={cn("text-right", headCellClass)}>
                                        变动
                                    </th>
                                    <th scope="col" className={cn("hidden text-right sm:table-cell", headCellClass)}>
                                        变动后余额
                                    </th>
                                </tr>
                            </thead>
                            <tbody>
                                {entries.map((entry) => (
                                    <LedgerRow key={entry.id} entry={entry} />
                                ))}
                            </tbody>
                        </table>
                    </TableSurface>
                    <PaginationBar
                        current={page}
                        pageSize={pageSize}
                        total={total}
                        onChange={(nextPage, nextPageSize) => {
                            setPage(nextPageSize !== pageSize ? 1 : nextPage);
                            setPageSize(nextPageSize);
                        }}
                    />
                </>
            ) : null}
        </section>
    );
}
