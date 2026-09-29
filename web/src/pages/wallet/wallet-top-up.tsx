import { Button, Input, Skeleton } from "antd";
import { Link } from "react-router";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";

import { CollectionGrid } from "@/components/layout/workspace-page";
import { WorkspaceErrorState } from "@/components/layout/workspace-state";
import type { CalloutTone } from "@/components/ui/product/callout";
import { formatCount } from "@/lib/format-usage";
import { cn } from "@/lib/utils";
import { createBillingOrder, formatMoneyFen, payBillingOrder, type BillingPaymentLaunch } from "@/services/api/billing";
import { getCreditTopUpPlans, type CreditTopUpPlan } from "@/services/api/credit";

import { WalletPanel, errorMessage, errorNotice, useDelayedLoading } from "./wallet-kit";

/**
 * Zone B —— 充值区。
 *
 * 商品卡只负责"哪一档"，下单与发起支付放在网格之下的结算条：把决策和付款分成两层，
 * 避免用户把"点一下卡"误当成"已经付钱"。
 *
 * 档位是单选语义（radiogroup）：方向键在档位间移动并选中，Tab 只进出整个组一次；
 * 卡片内部不放第二个可聚焦元素，键盘操作不会在卡内迷路。
 */

const cardClass = cn(
    "flex min-h-11 w-full flex-col rounded-[var(--r-2xl)] border bg-surface p-4 text-left",
    "transition-[border-color,background-color] duration-[var(--motion-state)] ease-[var(--ease-product-enter)]",
    "hover:bg-[var(--library-surface-hover)]",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
);

export function CreditTopUpSection({ revision, onNotice, onSettled }: { revision: number; onNotice: (notice: { tone: CalloutTone; text: string }) => void; onSettled: () => void }) {
    const [plans, setPlans] = useState<CreditTopUpPlan[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [reloadKey, setReloadKey] = useState(0);
    const [selectedCode, setSelectedCode] = useState("");
    const [couponCode, setCouponCode] = useState("");
    const [checkoutBusy, setCheckoutBusy] = useState(false);
    const cardRefs = useRef<Array<HTMLButtonElement | null>>([]);
    const showSkeleton = useDelayedLoading(loading);

    useEffect(() => {
        let cancelled = false;
        setLoading(true);
        setError("");
        getCreditTopUpPlans()
            .then((list) => {
                if (cancelled) return;
                setPlans(list);
                // 换货架后旧的选择可能已经下架，这里以服务端返回为准回一次选。
                setSelectedCode((current) => (list.some((plan) => plan.code === current) ? current : list[0]?.code ?? ""));
            })
            .catch((cause) => {
                if (cancelled) return;
                setPlans([]);
                setSelectedCode("");
                setError(errorMessage(cause, "充值档位加载失败"));
            })
            .finally(() => {
                if (!cancelled) setLoading(false);
            });
        return () => {
            cancelled = true;
        };
        // revision 由页面级「刷新」与支付成功推进：档位、余额、流水一起重取，避免三者读数互相矛盾。
    }, [reloadKey, revision]);

    const selectedPlan = useMemo(() => plans.find((plan) => plan.code === selectedCode) || null, [plans, selectedCode]);
    const giftLeaderCode = useMemo(() => {
        // 角标必须能被解释：这里给的是"赠送比例最高的那一档"，不是编一个"最划算"的名头。
        let leader: { code: string; ratio: number } | null = null;
        for (const plan of plans) {
            if (plan.credits <= 0 || plan.giftCredits <= 0) continue;
            const ratio = plan.giftCredits / plan.credits;
            if (!leader || ratio > leader.ratio) leader = { code: plan.code, ratio };
        }
        return leader?.code ?? "";
    }, [plans]);

    const rateSource = selectedPlan ?? plans[0] ?? null;
    const unitRate = rateSource && rateSource.priceFen > 0 ? Math.round(((rateSource.credits + rateSource.giftCredits) * 100) / rateSource.priceFen) : 0;

    const selectPlan = (plan: CreditTopUpPlan) => setSelectedCode(plan.code);

    const moveSelection = (index: number) => {
        if (!plans.length) return;
        const next = (index + plans.length) % plans.length;
        setSelectedCode(plans[next].code);
        cardRefs.current[next]?.focus();
    };

    const handleCardKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
        if (event.key === "ArrowRight" || event.key === "ArrowDown") {
            event.preventDefault();
            moveSelection(index + 1);
        } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
            event.preventDefault();
            moveSelection(index - 1);
        } else if (event.key === "Home") {
            event.preventDefault();
            moveSelection(0);
        } else if (event.key === "End") {
            event.preventDefault();
            moveSelection(plans.length - 1);
        }
    };

    /**
     * 发起支付的结果只有两种：有收银台地址就跳转，没有就交给运营确认。
     * 两种都不算失败——下单本身已经成功，此时说"支付失败"会让人重复付款。
     */
    const launchPayment = (launch: BillingPaymentLaunch) => {
        if (launch.payUrl) {
            window.open(launch.payUrl, "_blank", "noopener,noreferrer");
            onNotice({ tone: "info", text: "已打开收银台，支付完成后积分会自动入账，可稍后回到本页刷新。" });
            return;
        }
        onNotice({
            tone: "warning",
            text: launch.provider === "MANUAL" ? "订单已创建，本渠道由运营人工确认到账，确认后积分自动入账。" : "订单已创建，但暂未获取到收银台地址，可在下方「充值订单」里继续支付。",
        });
    };

    const handleCheckout = async () => {
        if (!selectedPlan || checkoutBusy) return;
        setCheckoutBusy(true);
        try {
            const order = await createBillingOrder({ planCode: selectedPlan.code, couponCode: couponCode.trim() || undefined });
            const launch = await payBillingOrder(order.id);
            launchPayment(launch);
            setCouponCode("");
            onSettled();
        } catch (cause) {
            onNotice(errorNotice(cause, "下单失败，请稍后重试。"));
        } finally {
            setCheckoutBusy(false);
        }
    };

    return (
        <section className="mt-8" aria-label="充值积分">
            <div className="flex flex-wrap items-end justify-between gap-3">
                <h2 className="font-[family-name:var(--font-display)] text-[var(--fs-heading-lg)] font-semibold leading-[1.35] text-foreground">充值积分</h2>
                <p className="min-w-0 text-[var(--fs-caption)] leading-relaxed text-foreground/58">
                    {unitRate > 0 ? `充值后积分立即到账，按当前档位约 1 元 = ${formatCount(unitRate)} 积分（含赠送）。` : "充值后积分立即到账，可直接用于平台模型生成。"}
                </p>
            </div>

            {showSkeleton && !plans.length ? (
                <CollectionGrid>
                    {[0, 1, 2].map((index) => (
                        <div key={index} className="rounded-[var(--r-2xl)] border border-[var(--workspace-border)] bg-surface p-4">
                            <Skeleton active title={{ width: "42%" }} paragraph={{ rows: 3 }} />
                        </div>
                    ))}
                </CollectionGrid>
            ) : null}

            {!loading && error ? <WorkspaceErrorState compact title="充值档位加载失败" description={error} onRetry={() => setReloadKey((value) => value + 1)} /> : null}

            {/* 加载结束且没有错误就不能留白：一档都没有时给出说明与支持入口。 */}
            {!loading && !error && !plans.length ? (
                <WalletPanel className="mt-4">
                    <h3 className="font-[family-name:var(--font-display)] text-[var(--fs-heading)] font-semibold text-foreground">暂无可购买的积分包</h3>
                    <p className="mt-1 max-w-[42ch] text-[var(--fs-caption)] leading-relaxed text-foreground/58">
                        当前账号看不到在售的积分包。如果你希望先开通或批量采购，可以通过帮助与反馈联系运营开通。
                    </p>
                    <div className="mt-4">
                        <Link
                            to="/support"
                            className="inline-flex h-9 items-center rounded-[var(--r-lg)] border border-[var(--workspace-border)] px-3 text-[var(--fs-caption)] font-medium text-foreground transition-colors hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        >
                            帮助与反馈
                        </Link>
                    </div>
                </WalletPanel>
            ) : null}

            {plans.length ? (
                <>
                    <div role="radiogroup" aria-label="积分档位">
                        <CollectionGrid>
                            {plans.map((plan, index) => {
                                const selected = plan.code === selectedCode;
                                return (
                                    <button
                                        key={plan.code}
                                        type="button"
                                        role="radio"
                                        aria-checked={selected}
                                        tabIndex={selected || (!selectedCode && index === 0) ? 0 : -1}
                                        ref={(node) => {
                                            cardRefs.current[index] = node;
                                        }}
                                        className={cn(cardClass, selected || plan.code === giftLeaderCode ? "border-[var(--workspace-border-strong)]" : "border-[var(--workspace-border)]")}
                                        onClick={() => selectPlan(plan)}
                                        onKeyDown={(event) => handleCardKeyDown(event, index)}
                                    >
                                        <span className="flex items-start justify-between gap-2">
                                            <span className="truncate font-[family-name:var(--font-display)] text-[var(--fs-heading)] font-semibold text-foreground">{plan.name}</span>
                                            {plan.code === giftLeaderCode ? (
                                                <span className="shrink-0 rounded-[var(--r-full)] border border-[var(--workspace-border-strong)] px-2 py-0.5 text-[var(--fs-tiny)] font-medium tracking-[0.02em] text-foreground/70">赠送最多</span>
                                            ) : null}
                                        </span>
                                        <span className="mt-3 flex items-baseline gap-1">
                                            <span className="font-[family-name:var(--font-display)] text-[var(--fs-title)] font-semibold tabular-nums text-foreground">{formatCount(plan.credits)}</span>
                                            <span className="text-[var(--fs-caption)] text-foreground/58">积分</span>
                                        </span>
                                        <span className="mt-1 font-[family-name:var(--font-display)] text-[var(--fs-heading-lg)] font-medium tabular-nums text-foreground/85">{formatMoneyFen(plan.priceFen)}</span>
                                        <span className="mt-2 block text-[var(--fs-caption)] text-foreground/70">
                                            {plan.giftCredits > 0 ? <>赠送 <span className="font-mono tabular-nums text-foreground">{formatCount(plan.giftCredits)}</span> 积分</> : "无赠送积分"}
                                        </span>
                                        {plan.periodDays > 0 ? <span className="mt-0.5 block text-[var(--fs-label)] text-foreground/58">有效期 {formatCount(plan.periodDays)} 天</span> : null}
                                        <span
                                            className={cn(
                                                "mt-4 inline-flex h-9 w-full items-center justify-center rounded-[var(--r-lg)] border px-3 text-[var(--fs-caption)] font-medium transition-colors",
                                                selected ? "border-transparent bg-[var(--workspace-accent-soft)] text-foreground" : "border-[var(--workspace-border)] text-foreground/70",
                                            )}
                                        >
                                            {selected ? "已选择" : "选择"}
                                        </span>
                                    </button>
                                );
                            })}
                        </CollectionGrid>
                    </div>

                    <div className="mt-4 flex flex-col gap-3 rounded-[var(--r-2xl)] border border-[var(--workspace-border)] bg-surface-secondary p-3 sm:flex-row sm:items-center sm:justify-between sm:p-4">
                        <p className="min-w-0 text-[var(--fs-caption)] leading-relaxed text-foreground/70">
                            {selectedPlan ? (
                                <>
                                    <span className="font-medium text-foreground">{selectedPlan.name}</span>
                                    <span className="mx-2 text-foreground/30">·</span>到账 <span className="font-mono tabular-nums text-foreground">{formatCount(selectedPlan.credits)}</span> 积分
                                    {selectedPlan.giftCredits > 0 ? <>，赠送 <span className="font-mono tabular-nums text-foreground">{formatCount(selectedPlan.giftCredits)}</span> 积分</> : null}
                                    <span className="mx-2 text-foreground/30">·</span>实付 <span className="font-mono tabular-nums text-foreground">{formatMoneyFen(selectedPlan.priceFen)}</span>
                                </>
                            ) : (
                                "选择上方任意档位后再下单。"
                            )}
                        </p>
                        <div className="flex shrink-0 items-center gap-2">
                            <Input className="w-40" placeholder="优惠券（可选）" value={couponCode} disabled={!selectedPlan || checkoutBusy} onChange={(event) => setCouponCode(event.target.value)} />
                            <Button className="h-11 px-5" type="primary" disabled={!selectedPlan} loading={checkoutBusy} onClick={() => void handleCheckout()}>
                                立即充值
                            </Button>
                        </div>
                    </div>
                </>
            ) : null}
        </section>
    );
}
