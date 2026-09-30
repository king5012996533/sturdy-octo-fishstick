import { Button, Tag } from "antd";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { PaginationBar } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceLoadingState, WorkspaceState } from "@/components/layout/workspace-state";
import type { CalloutTone } from "@/components/ui/product/callout";
import { formatDateTime } from "@/lib/format-usage";
import { cancelBillingOrder, formatMoneyFen, listMyBillingOrders, payBillingOrder, type BillingOrder, type BillingOrderStatus } from "@/services/api/billing";

import { WalletPanel, WalletSectionHead, errorMessage, useDelayedLoading } from "./wallet-kit";

/**
 * Zone C —— 充值订单。
 *
 * 订单是充值的凭据，也是"钱付了但积分没到"的唯一追查入口，所以它必须和余额同页：
 * 收银台是外部页面，用户跳出去之后回到本站的第一件事就是确认这笔单子到哪一步了。
 *
 * 只有 PENDING 才给操作按钮：已完成的订单再点一次没有任何语义，反而会让人以为能重付。
 */

const orderStatusMeta: Record<BillingOrderStatus, { label: string; color: string }> = {
    PENDING: { label: "待支付", color: "gold" },
    PAID: { label: "已支付", color: "green" },
    CANCELED: { label: "已取消", color: "default" },
    REFUNDED: { label: "已退款", color: "blue" },
    FAILED: { label: "失败", color: "red" },
};

/* 后端以后加新状态时不能让整行 TypeError：认不出来的状态原样展示。 */
function statusView(status: BillingOrderStatus) {
    return (orderStatusMeta as Record<string, { label: string; color: string } | undefined>)[status] ?? { label: status || "未知状态", color: "default" };
}

function OrderRow({ order, busy, onContinuePay, onCancel }: { order: BillingOrder; busy: boolean; onContinuePay: (order: BillingOrder) => void; onCancel: (order: BillingOrder) => void }) {
    const status = statusView(order.status);
    return (
        <div className="flex flex-col gap-2.5 rounded-[var(--r-lg)] border border-[var(--workspace-border)] bg-surface px-3.5 py-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate font-mono text-[var(--fs-caption)] text-foreground">{order.orderNo}</span>
                    <Tag className="m-0" color={status.color}>
                        {status.label}
                    </Tag>
                </div>
                <p className="mt-1 text-[var(--fs-label)] text-foreground/70">
                    {order.planName || order.planCode} · 实付 <span className="font-medium tabular-nums text-foreground">{formatMoneyFen(order.payableFen)}</span>
                    {order.discountFen > 0 ? <span className="text-foreground/50">（已抵扣 {formatMoneyFen(order.discountFen)}）</span> : null}
                </p>
                <p className="mt-0.5 text-[var(--fs-tiny)] text-foreground/45">
                    创建 {formatDateTime(order.createdAt)} · 支付 {formatDateTime(order.paidAt)}
                </p>
            </div>
            {order.status === "PENDING" ? (
                <div className="flex shrink-0 items-center gap-2">
                    <Button type="primary" size="small" loading={busy} onClick={() => onContinuePay(order)}>
                        继续支付
                    </Button>
                    <Button size="small" disabled={busy} onClick={() => onCancel(order)}>
                        取消订单
                    </Button>
                </div>
            ) : null}
        </div>
    );
}

export function CreditOrdersSection({ revision, onNotice }: { revision: number; onNotice: (notice: { tone: CalloutTone; text: string }) => void }) {
    const [orders, setOrders] = useState<BillingOrder[]>([]);
    const [total, setTotal] = useState(0);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(10);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [reloadKey, setReloadKey] = useState(0);
    const [busyId, setBusyId] = useState("");
    const showSkeleton = useDelayedLoading(loading && !orders.length);

    useEffect(() => {
        let cancelled = false;
        setLoading(true);
        setError("");
        listMyBillingOrders({ page, pageSize })
            .then((result) => {
                if (cancelled) return;
                setOrders(result.orders || []);
                setTotal(result.total || 0);
            })
            .catch((cause) => {
                if (cancelled) return;
                setOrders([]);
                setTotal(0);
                setError(errorMessage(cause, "订单加载失败"));
            })
            .finally(() => {
                if (!cancelled) setLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [page, pageSize, reloadKey, revision]);

    const reload = useCallback(() => setReloadKey((value) => value + 1), []);

    /**
     * 发起支付的结果只有两种：有收银台地址就跳转，没有就交给运营确认。
     * 两种都不算失败——下单本身已经成功，此时说"支付失败"会让人重复付款。
     */
    const continuePay = async (order: BillingOrder) => {
        if (busyId) return;
        setBusyId(order.id);
        try {
            const launch = await payBillingOrder(order.id);
            if (launch.payUrl) {
                window.open(launch.payUrl, "_blank", "noopener,noreferrer");
                onNotice({ tone: "info", text: "已打开收银台，支付完成后积分自动入账，可稍后回到本页刷新。" });
            } else {
                onNotice({
                    tone: "warning",
                    text: launch.provider === "MANUAL" ? "订单已创建，本渠道由运营人工确认到账，确认后积分自动入账。" : "暂未获取到收银台地址，可稍后重试。",
                });
            }
        } catch (cause) {
            onNotice({ tone: "error", text: errorMessage(cause, "发起支付失败，请稍后重试。") });
        } finally {
            setBusyId("");
        }
    };

    const cancelOrder = async (order: BillingOrder) => {
        if (busyId) return;
        setBusyId(order.id);
        try {
            await cancelBillingOrder(order.id);
            onNotice({ tone: "success", text: `订单 ${order.orderNo} 已取消。` });
            reload();
        } catch (cause) {
            onNotice({ tone: "error", text: errorMessage(cause, "取消订单失败，请稍后重试。") });
        } finally {
            setBusyId("");
        }
    };

    return (
        <section className="wallet-section" aria-label="充值订单">
            <WalletSectionHead
                eyebrow="Orders"
                title="充值订单"
                note="支付完成后积分自动入账，可在积分流水里逐笔核对。"
                actions={
                    <Button icon={<RefreshCw className="size-3.5" strokeWidth={1.75} />} loading={loading} onClick={reload}>
                        刷新状态
                    </Button>
                }
            />

            {showSkeleton ? (
                <div className="mt-4">
                    <WorkspaceLoadingState label="正在加载订单" detail="读取历史下单与支付状态" rows={2} />
                </div>
            ) : null}

            {!loading && error ? <WorkspaceErrorState compact title="订单加载失败" description={error} onRetry={reload} /> : null}

            {!loading && !error && !orders.length ? (
                <WalletPanel className="mt-4">
                    <h3 className="font-[family-name:var(--font-display)] text-[var(--fs-heading)] font-semibold text-foreground">还没有充值订单</h3>
                    <p className="mt-1 max-w-[42ch] text-[var(--fs-caption)] leading-relaxed text-foreground/58">在上方选择档位并完成支付后，订单会出现在这里。</p>
                </WalletPanel>
            ) : null}

            {orders.length ? (
                <>
                    <div className="mt-4 flex flex-col gap-2">
                        {orders.map((order) => (
                            <OrderRow key={order.id} order={order} busy={busyId === order.id} onContinuePay={(target) => void continuePay(target)} onCancel={(target) => void cancelOrder(target)} />
                        ))}
                    </div>
                    <PaginationBar
                        current={page}
                        pageSize={pageSize}
                        total={total}
                        pageSizeOptions={[10, 20, 50]}
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
