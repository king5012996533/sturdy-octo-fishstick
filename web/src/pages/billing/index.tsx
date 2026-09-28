import { Button, Input, Tag } from "antd";
import { RefreshCw, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { PageHeader, PaginationBar, WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceLoadingState, WorkspaceState } from "@/components/layout/workspace-state";
import { Callout, type CalloutTone } from "@/components/ui/product/callout";
import { formatBytes, formatCount, formatDateTime } from "@/lib/format-usage";
import { cn } from "@/lib/utils";
import {
    cancelBillingOrder,
    createBillingOrder,
    formatMoneyFen,
    getBillingEntitlements,
    getBillingPlans,
    listMyBillingOrders,
    payBillingOrder,
    quoteBillingOrder,
    type BillingCouponQuote,
    type BillingEntitlements,
    type BillingOrder,
    type BillingOrderStatus,
    type BillingPaymentLaunch,
    type BillingPlan,
} from "@/services/api/billing";
import { useUserStore } from "@/stores/use-user-store";

/**
 * 用户端订阅 / 充值页：选套餐 → 试算优惠 → 下单 → 发起支付 → 在订单列表里跟踪状态。
 *
 * 这一页只做编排与展示，金额、券可用性、订单状态全部由服务端裁决：
 * 前端算价只用于"让用户下单前看到大概"，真正的收款口径以 createBillingOrder 的返回为准。
 */

/* 订单状态是后端枚举，中文映射只发生在展示层，避免状态语义在前后端各解释一次。 */
const orderStatusMeta: Record<BillingOrderStatus, { label: string; color: string }> = {
    PENDING: { label: "待支付", color: "gold" },
    PAID: { label: "已支付", color: "green" },
    CANCELED: { label: "已取消", color: "default" },
    REFUNDED: { label: "已退款", color: "blue" },
    FAILED: { label: "失败", color: "red" },
};

/**
 * 配额读数：后端用 0 表示"不限"，若原样渲染成 0，用户会读成"额度已用尽"，
 * 与真实语义正好相反，因此 0 必须单独翻译成"不限"，正数才展示具体数值。
 */
function formatQuota(value: number, unit: string) {
    if (!Number.isFinite(value) || value <= 0) return "不限";
    return `${formatCount(value)} ${unit}`;
}

/* 存储额度按"不限 / 人类可读容量"展示，单位换算复用既有工具，避免页面自己四舍五入。 */
function formatStorageQuota(megabytes: number) {
    if (!Number.isFinite(megabytes) || megabytes <= 0) return "不限";
    return formatBytes(megabytes * 1024 * 1024);
}

/* 服务端返回的中文文案要原样透出，只有拿不到文案时才回退到本地兜底。 */
function errorMessage(error: unknown, fallback: string) {
    return error instanceof Error && error.message ? error.message : fallback;
}

export function BillingPage() {
    const accountName = useUserStore((state) => state.user?.displayName || state.user?.username || "");

    const [entitlements, setEntitlements] = useState<BillingEntitlements | null>(null);
    const [plans, setPlans] = useState<BillingPlan[]>([]);
    const [catalogLoading, setCatalogLoading] = useState(true);
    const [catalogError, setCatalogError] = useState("");
    const [catalogReloadKey, setCatalogReloadKey] = useState(0);

    const [selectedCode, setSelectedCode] = useState("");
    const [couponCode, setCouponCode] = useState("");
    const [quote, setQuote] = useState<BillingCouponQuote | null>(null);
    const [quoteLoading, setQuoteLoading] = useState(false);
    const [checkoutBusy, setCheckoutBusy] = useState(false);
    const [notice, setNotice] = useState<{ tone: CalloutTone; text: string } | null>(null);
    const checkoutRef = useRef<HTMLElement | null>(null);

    const [orders, setOrders] = useState<BillingOrder[]>([]);
    const [ordersTotal, setOrdersTotal] = useState(0);
    const [ordersPage, setOrdersPage] = useState(1);
    const [ordersPageSize, setOrdersPageSize] = useState(10);
    const [ordersLoading, setOrdersLoading] = useState(true);
    const [ordersError, setOrdersError] = useState("");
    const [ordersReloadKey, setOrdersReloadKey] = useState(0);
    const [orderBusyId, setOrderBusyId] = useState("");

    useEffect(() => {
        let cancelled = false;
        setCatalogLoading(true);
        setCatalogError("");
        // 订阅状态与货架必须一起到位：只加载其中一个都会让首屏自相矛盾（有套餐却没当前订阅）。
        Promise.all([getBillingEntitlements(), getBillingPlans()])
            .then(([entitlement, planList]) => {
                if (cancelled) return;
                setEntitlements(entitlement);
                setPlans(planList);
            })
            .catch((error) => {
                if (cancelled) return;
                setEntitlements(null);
                setPlans([]);
                setCatalogError(errorMessage(error, "套餐信息加载失败"));
            })
            .finally(() => {
                if (!cancelled) setCatalogLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [catalogReloadKey]);

    useEffect(() => {
        let cancelled = false;
        setOrdersLoading(true);
        setOrdersError("");
        listMyBillingOrders({ page: ordersPage, pageSize: ordersPageSize })
            .then((result) => {
                if (cancelled) return;
                setOrders(result.orders || []);
                setOrdersTotal(result.total || 0);
            })
            .catch((error) => {
                if (cancelled) return;
                setOrders([]);
                setOrdersTotal(0);
                setOrdersError(errorMessage(error, "订单加载失败"));
            })
            .finally(() => {
                if (!cancelled) setOrdersLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [ordersPage, ordersPageSize, ordersReloadKey]);

    const selectedPlan = useMemo(() => plans.find((plan) => plan.code === selectedCode) || null, [plans, selectedCode]);

    const reloadOrders = useCallback(() => setOrdersReloadKey((value) => value + 1), []);

    /**
     * 统一处理"发起支付"的结果：有收银台地址就跳转，没有（MANUAL 等线下渠道）就交给运营确认。
     * 下单本身已经成功，所以无论哪种分支都不算失败，只提示下一步该等什么。
     */
    const applyPaymentLaunch = useCallback((launch: BillingPaymentLaunch) => {
        if (launch.payUrl) {
            window.open(launch.payUrl, "_blank", "noopener,noreferrer");
            setNotice({ tone: "info", text: "已打开收银台，支付完成后回来查看订单状态。" });
            return;
        }
        setNotice({
            tone: "warning",
            text: launch.provider === "MANUAL" ? "订单已创建，本渠道由运营人工确认到账，确认后套餐自动生效。" : "订单已创建，但暂未获取到收银台地址，可在下方订单列表里稍后继续支付。",
        });
    }, []);

    const selectPlan = (plan: BillingPlan) => {
        setSelectedCode(plan.code);
        // 换套餐等于换一份报价：上一张券的试算结果不再适用，必须清掉，否则会误导用户以为优惠还在。
        setCouponCode("");
        setQuote(null);
        setNotice(null);
        // 结算面板要到本次渲染后才挂载，等一帧再滚，否则 ref 还是空的。
        window.requestAnimationFrame(() => checkoutRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }));
    };

    const closeCheckout = () => {
        setSelectedCode("");
        setQuote(null);
        setNotice(null);
    };

    const handleQuote = async () => {
        if (!selectedPlan || quoteLoading) return;
        setQuoteLoading(true);
        setNotice(null);
        const code = couponCode.trim();
        try {
            const result = await quoteBillingOrder({ planCode: selectedPlan.code, couponCode: code || undefined });
            setQuote(result);
            // couponError 就地渲染在结算面板里，这里只给成功路径一句确认，避免同一件事提示两遍。
            if (!result.couponError) setNotice({ tone: "success", text: code ? `优惠券 ${code} 可使用。` : "已按套餐原价试算。" });
        } catch (error) {
            setQuote(null);
            setNotice({ tone: "error", text: errorMessage(error, "试算失败，请稍后重试。") });
        } finally {
            setQuoteLoading(false);
        }
    };

    const handleCheckout = async () => {
        if (!selectedPlan || checkoutBusy) return;
        setCheckoutBusy(true);
        setNotice(null);
        const code = couponCode.trim();
        try {
            // 券在这里只作为"意向"传给服务端：真正能不能用、抵扣多少，以服务端下单结果为准。
            const order = await createBillingOrder({ planCode: selectedPlan.code, couponCode: code || undefined });
            const launch = await payBillingOrder(order.id);
            applyPaymentLaunch(launch);
            setQuote(null);
            setCouponCode("");
            setOrdersPage(1);
            // 订单已经落库，立刻重载列表，用户马上能看到这笔待支付订单。
            reloadOrders();
        } catch (error) {
            setNotice({ tone: "error", text: errorMessage(error, "下单失败，请稍后重试。") });
        } finally {
            setCheckoutBusy(false);
        }
    };

    const handleContinuePay = async (order: BillingOrder) => {
        if (orderBusyId) return;
        setOrderBusyId(order.id);
        setNotice(null);
        try {
            const launch = await payBillingOrder(order.id);
            applyPaymentLaunch(launch);
        } catch (error) {
            setNotice({ tone: "error", text: errorMessage(error, "发起支付失败，请稍后重试。") });
        } finally {
            setOrderBusyId("");
        }
    };

    const handleCancelOrder = async (order: BillingOrder) => {
        if (orderBusyId) return;
        setOrderBusyId(order.id);
        setNotice(null);
        try {
            await cancelBillingOrder(order.id);
            setNotice({ tone: "success", text: `订单 ${order.orderNo} 已取消。` });
            reloadOrders();
        } catch (error) {
            setNotice({ tone: "error", text: errorMessage(error, "取消订单失败，请稍后重试。") });
        } finally {
            setOrderBusyId("");
        }
    };

    const originalFen = selectedPlan?.priceFen ?? 0;
    const discountFen = quote?.discountFen ?? 0;
    const payableFen = quote?.payableFen ?? originalFen;

    return (
        <WorkspacePage>
            <PageHeader
                title="订阅与充值"
                description="选择套餐、使用优惠券下单，支付完成后配额自动生效"
                meta={accountName ? <Tag className="m-0">{accountName}</Tag> : null}
                actions={
                    <Button icon={<RefreshCw className="size-3.5" />} loading={catalogLoading} onClick={() => setCatalogReloadKey((value) => value + 1)}>
                        刷新套餐
                    </Button>
                }
            />

            {/* 全局 message 在本项目里是关闭的，所有反馈都必须落在页面内。 */}
            {notice ? (
                <Callout tone={notice.tone} className="mt-3" onClose={() => setNotice(null)}>
                    {notice.text}
                </Callout>
            ) : null}

            {catalogLoading && !plans.length ? <WorkspaceLoadingState label="正在加载订阅信息" detail="读取当前订阅与在售套餐" rows={2} /> : null}
            {!catalogLoading && catalogError ? <WorkspaceErrorState title="订阅信息加载失败" description={catalogError} onRetry={() => setCatalogReloadKey((value) => value + 1)} /> : null}

            {/* 加载结束且没有错误就必须渲染货架骨架：即使服务端返回空套餐，也要让用户看到空态而不是空白页。 */}
            {!catalogLoading && !catalogError ? (
                <>
                    <section className="mt-4 rounded-[var(--r-xl)] border border-border bg-surface p-4" aria-label="当前订阅">
                        <div className="flex flex-wrap items-start justify-between gap-3">
                            <div className="min-w-0">
                                <h2 className="text-[var(--fs-heading)] font-semibold text-foreground">当前订阅</h2>
                                {entitlements?.active ? (
                                    <>
                                        <p className="mt-1 text-[var(--fs-body-lg)] font-medium text-foreground">
                                            {entitlements.planName || entitlements.planCode}
                                            {entitlements.planCode ? <span className="ml-2 font-mono text-[var(--fs-label)] text-foreground/50">{entitlements.planCode}</span> : null}
                                        </p>
                                        <p className="mt-0.5 text-[var(--fs-caption)] text-foreground/58">到期时间：{formatDateTime(entitlements.expiresAt)}</p>
                                    </>
                                ) : (
                                    <>
                                        <p className="mt-1 text-[var(--fs-body)] text-foreground">订阅后即可使用平台模型，无需自备密钥。</p>
                                        <p className="mt-0.5 text-[var(--fs-caption)] text-foreground/58">当前账号还没有生效中的订阅，选择下方套餐即可开通。</p>
                                    </>
                                )}
                            </div>
                            <Tag className="m-0" color={entitlements?.active ? "green" : "default"}>
                                {entitlements?.active ? "生效中" : "未订阅"}
                            </Tag>
                        </div>
                        <div className="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-3">
                            <QuotaStat label="调用次数" value={entitlements ? formatQuota(entitlements.quotaCalls, "次") : "—"} />
                            <QuotaStat label="存储空间" value={entitlements ? formatStorageQuota(entitlements.quotaStorageMb) : "—"} />
                            <QuotaStat label="成员数" value={entitlements ? formatQuota(entitlements.quotaMembers, "人") : "—"} />
                        </div>
                    </section>

                    <section className="mt-6" aria-label="套餐货架">
                        <h2 className="text-[var(--fs-heading)] font-semibold text-foreground">选择套餐</h2>
                        {plans.length ? (
                            <div className="mt-3 grid grid-cols-1 gap-4 sm:grid-cols-[repeat(auto-fill,minmax(248px,1fr))]">
                                {plans.map((plan) => (
                                    <PlanCard key={plan.code} plan={plan} selected={plan.code === selectedCode} onSelect={() => selectPlan(plan)} />
                                ))}
                            </div>
                        ) : (
                            <WorkspaceState icon="wallet" compact title="暂无可购买的套餐" description="运营还没有上架套餐，请稍后再来看。" />
                        )}
                    </section>
                </>
            ) : null}

            {selectedPlan ? (
                <section ref={checkoutRef} className="mt-6 rounded-[var(--r-xl)] border border-border bg-surface p-4" aria-label="结算面板">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                        <h2 className="text-[var(--fs-heading)] font-semibold text-foreground">结算 · {selectedPlan.name}</h2>
                        <Button type="text" icon={<X className="size-3.5" />} onClick={closeCheckout}>
                            收起
                        </Button>
                    </div>
                    <div className="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-3">
                        <QuotaStat label="原价" value={formatMoneyFen(originalFen)} />
                        <QuotaStat label="抵扣金额" value={discountFen > 0 ? `-${formatMoneyFen(discountFen)}` : formatMoneyFen(0)} />
                        <QuotaStat label="实付金额" value={formatMoneyFen(payableFen)} emphasis />
                    </div>
                    <div className="mt-3 flex flex-wrap items-end gap-2">
                        <label className="flex flex-col gap-1">
                            <span className="text-[var(--fs-label)] text-foreground/58">优惠券</span>
                            <Input className="w-56" value={couponCode} placeholder="输入券码后点右侧试算" onChange={(event) => setCouponCode(event.target.value)} onPressEnter={() => void handleQuote()} allowClear />
                        </label>
                        <Button loading={quoteLoading} onClick={() => void handleQuote()}>
                            试算
                        </Button>
                        <Button type="primary" loading={checkoutBusy} onClick={() => void handleCheckout()}>
                            立即支付
                        </Button>
                    </div>
                    {quote?.couponError ? (
                        <Callout tone="error" title="优惠券不可用" className="mt-3">
                            {quote.couponError}；仍可按原价下单，无需先删掉券码。
                        </Callout>
                    ) : null}
                </section>
            ) : null}

            <section className="mt-8" aria-label="我的订单">
                <div className="flex flex-wrap items-center justify-between gap-2">
                    <h2 className="text-[var(--fs-heading)] font-semibold text-foreground">我的订单</h2>
                    <Button
                        icon={<RefreshCw className="size-3.5" />}
                        loading={ordersLoading}
                        onClick={() => {
                            setNotice(null);
                            reloadOrders();
                        }}
                    >
                        刷新状态
                    </Button>
                </div>
                {ordersError ? <WorkspaceErrorState compact title="订单加载失败" description={ordersError} onRetry={reloadOrders} /> : null}
                {!ordersError && ordersLoading && !orders.length ? <WorkspaceLoadingState label="正在加载订单" detail="读取历史下单与支付状态" rows={2} /> : null}
                {!ordersError && !ordersLoading && !orders.length ? <WorkspaceState icon="wallet" compact title="还没有订单" description="选择上方套餐下单后，订单会出现在这里。" /> : null}
                {!ordersError && orders.length ? (
                    <div className="mt-3 flex flex-col gap-2">
                        {orders.map((order) => (
                            <OrderRow key={order.id} order={order} busy={orderBusyId === order.id} onContinuePay={(target) => void handleContinuePay(target)} onCancel={(target) => void handleCancelOrder(target)} />
                        ))}
                    </div>
                ) : null}
                <PaginationBar
                    current={ordersPage}
                    pageSize={ordersPageSize}
                    total={ordersTotal}
                    pageSizeOptions={[10, 20, 50]}
                    onChange={(nextPage, nextPageSize) => {
                        setOrdersPage(nextPageSize !== ordersPageSize ? 1 : nextPage);
                        setOrdersPageSize(nextPageSize);
                    }}
                />
            </section>
        </WorkspacePage>
    );
}

function QuotaStat({ label, value, emphasis = false }: { label: string; value: string; emphasis?: boolean }) {
    return (
        <div className="rounded-[var(--r-lg)] bg-surface-secondary px-3 py-2">
            <div className="text-[var(--fs-label)] text-foreground/58">{label}</div>
            <div className={cn("mt-0.5 tabular-nums", emphasis ? "text-[var(--fs-heading)] font-semibold text-foreground" : "text-[var(--fs-body)] text-foreground")}>{value}</div>
        </div>
    );
}

function PlanCard({ plan, selected, onSelect }: { plan: BillingPlan; selected: boolean; onSelect: () => void }) {
    return (
        <article className={cn("flex flex-col rounded-[var(--r-xl)] border bg-surface p-4", selected ? "border-foreground/45" : "border-border")}>
            <div className="flex items-start justify-between gap-2">
                <h3 className="text-[var(--fs-heading)] font-semibold text-foreground">{plan.name}</h3>
                {selected ? (
                    <Tag className="m-0" color="blue">
                        已选择
                    </Tag>
                ) : null}
            </div>
            <p className="mt-1 min-h-10 text-[var(--fs-caption)] leading-5 text-foreground/58">{plan.description || "暂无套餐说明"}</p>
            <p className="mt-2 text-[var(--fs-title)] font-semibold tabular-nums text-foreground">
                {formatMoneyFen(plan.priceFen)}
                <span className="ml-1 text-[var(--fs-label)] font-normal text-foreground/50">/ {formatCount(plan.periodDays)} 天</span>
            </p>
            <ul className="mt-3 flex flex-col gap-1 text-[var(--fs-label)] text-foreground/70">
                <li>调用次数：{formatQuota(plan.quotaCalls, "次")}</li>
                <li>存储空间：{formatStorageQuota(plan.quotaStorageMb)}</li>
                <li>成员数：{formatQuota(plan.quotaMembers, "人")}</li>
            </ul>
            <div className="mt-3">
                <Button block type={selected ? "primary" : "default"} onClick={onSelect}>
                    {selected ? "重新选择" : "选择"}
                </Button>
            </div>
        </article>
    );
}

function OrderRow({ order, busy, onContinuePay, onCancel }: { order: BillingOrder; busy: boolean; onContinuePay: (order: BillingOrder) => void; onCancel: (order: BillingOrder) => void }) {
    const status = orderStatusMeta[order.status];
    return (
        <div className="flex flex-col gap-2.5 rounded-[var(--r-lg)] border border-border bg-surface px-3.5 py-3 sm:flex-row sm:items-center sm:justify-between">
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
