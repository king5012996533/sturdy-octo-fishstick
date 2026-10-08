import { Button, Input, Skeleton } from "antd";
import { ArrowRight, Check } from "lucide-react";
import { Link } from "react-router";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";

import { CollectionGrid } from "@/components/layout/workspace-page";
import { WorkspaceErrorState } from "@/components/layout/workspace-state";
import type { CalloutTone } from "@/components/ui/product/callout";
import { formatCount } from "@/lib/format-usage";
import { cn } from "@/lib/utils";
import { createBillingOrder, formatMoneyFen, listMyBillingOrders, type BillingOrder } from "@/services/api/billing";
import { getCreditTopUpPlans, type CreditTopUpPlan } from "@/services/api/credit";

import { WalletPanel, WalletSectionHead, errorMessage, errorNotice, useDelayedLoading } from "./wallet-kit";

/**
 * Zone B —— 充值区。
 *
 * 商品卡只负责"哪一档"，下单放在网格之下的结算条：把决策和付款分成两层，避免用户把
 * "点一下卡"误当成"已经付钱"。真正的付款动作在结算条点击之后，由页面级的支付弹窗
 * （WalletPayDialog）承接——收款码不在本区常驻，只有付某一单的时候才出现。
 *
 * 档位是单选语义（radiogroup）：方向键在档位间移动并选中，Tab 只进出整个组一次；
 * 卡片内部不放第二个可聚焦元素，键盘操作不会在卡内迷路。
 */

const cardClass = cn(
    "wallet-plan w-full min-w-0 rounded-[var(--r-2xl)] border bg-surface p-4 text-left",
    "transition-[border-color,background-color] duration-[var(--motion-state)] ease-[var(--ease-product-enter)]",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
);

/** 每一档自己的换算率（含赠送）。写死一个全局比例，会和这一档的实付对不上。 */
function planUnitRate(plan: CreditTopUpPlan) {
    return plan.priceFen > 0 ? Math.round(((plan.credits + plan.giftCredits) * 100) / plan.priceFen) : 0;
}

export function CreditTopUpSection({ revision, onNotice, onSettled, onOpenPayment }: { revision: number; onNotice: (notice: { tone: CalloutTone; text: string }) => void; onSettled: () => void; onOpenPayment: (order: BillingOrder) => void }) {
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
    const unitRate = rateSource ? planUnitRate(rateSource) : 0;

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

    const handleCheckout = async () => {
        if (!selectedPlan || checkoutBusy) return;
        setCheckoutBusy(true);
        try {
            // 同一档位已经有一笔待支付订单时复用它：点一次没看到反馈就再点一次的人，
            // 会在这里拿到同一笔订单，而不是在后台又多一条待支付记录。
            // 只复用还没过期的：过期订单的「继续支付」会被服务端拒掉，复用等于把用户卡在一个
            // 打不开的收银台上，还不如直接下一笔新单。
            const existing = await listMyBillingOrders({ page: 1, pageSize: 20 });
            const now = Date.now();
            const pending = (existing.orders || []).find((order) => order.status === "PENDING" && order.planCode === selectedPlan.code && (!order.expiresAt || new Date(order.expiresAt).getTime() > now));
            if (pending) {
                onNotice({ tone: "info", text: `已有一笔「${selectedPlan.name}」待支付订单，已为你打开，完成付款即可。` });
                onOpenPayment(pending);
                return;
            }
            const order = await createBillingOrder({ planCode: selectedPlan.code, couponCode: couponCode.trim() || undefined });
            setCouponCode("");
            onOpenPayment(order);
            // 订单已经落库，订单区要立刻显示这条待支付记录，否则用户会以为没下单成功。
            onSettled();
        } catch (cause) {
            onNotice(errorNotice(cause, "下单失败，请稍后重试。"));
        } finally {
            setCheckoutBusy(false);
        }
    };

    return (
        <section className="wallet-section" aria-label="充值积分">
            <WalletSectionHead
                eyebrow="Top up"
                title="充值积分"
                note={unitRate > 0 ? `选好档位后点「立即充值」，弹窗里扫码付款；积分到账后按约 1 元 = ${formatCount(unitRate)} 积分（含赠送）。` : "选好档位后点「立即充值」，弹窗里扫码付款，积分到账后可直接用于平台模型生成。"}
            />

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
                                const rate = planUnitRate(plan);
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
                                        className={cn(
                                            cardClass,
                                            selected || plan.code === giftLeaderCode ? "border-[var(--workspace-border-strong)]" : "border-[var(--workspace-border)]",
                                            // 选中态的背景由 .wallet-plan[aria-checked] 负责，hover 只补在未选中的卡上，免得悬停把选中态涂掉。
                                            selected ? "" : "hover:bg-[var(--library-surface-hover)]",
                                        )}
                                        onClick={() => selectPlan(plan)}
                                        onKeyDown={(event) => handleCardKeyDown(event, index)}
                                    >
                                        <span className="wallet-plan-top">
                                            <span className="wallet-plan-name">{plan.name}</span>
                                            {plan.code === giftLeaderCode ? <span className="wallet-plan-badge">赠送最多</span> : null}
                                        </span>
                                        <span className="wallet-plan-credits">
                                            {formatCount(plan.credits)}
                                            <em>积分</em>
                                        </span>
                                        <span className="wallet-plan-points">
                                            <span className="wallet-plan-point">
                                                <Check aria-hidden strokeWidth={2.5} />
                                                {plan.giftCredits > 0 ? `含赠送 ${formatCount(plan.giftCredits)} 积分` : "无赠送积分"}
                                            </span>
                                            {plan.periodDays > 0 ? (
                                                <span className="wallet-plan-point">
                                                    <Check aria-hidden strokeWidth={2.5} />
                                                    有效期 {formatCount(plan.periodDays)} 天
                                                </span>
                                            ) : null}
                                            {rate > 0 ? (
                                                <span className="wallet-plan-point">
                                                    <Check aria-hidden strokeWidth={2.5} />
                                                    约 1 元 = {formatCount(rate)} 积分
                                                </span>
                                            ) : null}
                                        </span>
                                        <span className="wallet-plan-foot">
                                            <span className="wallet-plan-price">{formatMoneyFen(plan.priceFen)}</span>
                                            <span className="wallet-plan-action">
                                                {selected ? "已选择" : "选择"}
                                                <ArrowRight aria-hidden strokeWidth={2} />
                                            </span>
                                        </span>
                                    </button>
                                );
                            })}
                        </CollectionGrid>
                    </div>

                    <div className="wallet-checkout">
                        <p className="wallet-checkout-summary">
                            {selectedPlan ? (
                                <>
                                    <strong>{selectedPlan.name}</strong>
                                    <span className="mx-2 text-foreground/30">·</span>到账 <strong className="font-mono tabular-nums">{formatCount(selectedPlan.credits)}</strong> 积分
                                    {selectedPlan.giftCredits > 0 ? <>，赠送 <strong className="font-mono tabular-nums">{formatCount(selectedPlan.giftCredits)}</strong> 积分</> : null}
                                    <span className="mx-2 text-foreground/30">·</span>实付 <span className="wallet-checkout-amount">{formatMoneyFen(selectedPlan.priceFen)}</span>
                                </>
                            ) : (
                                "选择上方任意档位后再下单。"
                            )}
                        </p>
                        <div className="wallet-checkout-fields">
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
