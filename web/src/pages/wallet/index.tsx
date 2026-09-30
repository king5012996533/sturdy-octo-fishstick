import { Button } from "antd";
import { RefreshCw } from "lucide-react";
import { useReducedMotion } from "motion/react";
import { useCallback, useEffect, useRef, useState } from "react";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceErrorState } from "@/components/layout/workspace-state";
import { Callout, type CalloutTone } from "@/components/ui/product/callout";
import { formatCount, formatDateTime } from "@/lib/format-usage";
import { cn } from "@/lib/utils";
import { getCreditWallet, type CreditWallet } from "@/services/api/credit";
import { useUserStore } from "@/stores/use-user-store";

import { CreditLedgerSection } from "./wallet-ledger";
import { CreditOrdersSection } from "./wallet-orders";
import { CreditTopUpSection } from "./wallet-top-up";
import { WalletPanel, WalletSectionHead, errorMessage, useDelayedLoading } from "./wallet-kit";

/**
 * 用户端积分中心（/wallet）：余额 → 充值 → 订单 → 流水，四级信息层级。
 *
 * 余额是唯一主角，充值是它的下一步动作，订单是付款凭据，流水是账目明细。四个 Zone 各自
 * 加载、各自失败、各自重试：账目接口抖动不能让用户看不到余额，反之亦然。
 */

/** 余额刷新时的一次性 count-up（≤600ms）；reduced-motion 下直接落到终值。 */
function useCountUp(value: number) {
    const reducedMotion = useReducedMotion();
    const [shown, setShown] = useState(value);
    const settledRef = useRef(value);

    useEffect(() => {
        if (reducedMotion || settledRef.current === value) {
            settledRef.current = value;
            setShown(value);
            return;
        }
        const from = settledRef.current;
        const startedAt = performance.now();
        let frame = window.requestAnimationFrame(function step(now) {
            const progress = Math.min(1, (now - startedAt) / 600);
            setShown(Math.round(from + (value - from) * progress));
            if (progress < 1) frame = window.requestAnimationFrame(step);
            else settledRef.current = value;
        });
        return () => window.cancelAnimationFrame(frame);
    }, [value, reducedMotion]);

    return shown;
}

function Readout({ label, value }: { label: string; value: number }) {
    return (
        <div className="wallet-metric">
            <p className="wallet-metric-label">{label}</p>
            <p className="wallet-metric-value">{formatCount(value)}</p>
        </div>
    );
}

/** Zone A —— 余额区。零余额是状态而不是错误：卡片照常完整渲染，只是读数降一档字号。 */
function BalanceCard({ wallet, loading, error, onRetry, onTopUp }: { wallet: CreditWallet | null; loading: boolean; error: string; onRetry: () => void; onTopUp: () => void }) {
    const showSkeleton = useDelayedLoading(loading);
    const balance = wallet?.balance ?? 0;
    const shown = useCountUp(balance);

    if (!wallet) {
        if (error) return <WorkspaceErrorState title="余额加载失败" description={error} onRetry={onRetry} />;
        if (!showSkeleton) return null;
        return (
            <WalletPanel>
                <div className="flex flex-col gap-3" aria-busy="true" aria-live="polite">
                    <span className="block h-4 w-20 rounded-[var(--r-sm)] bg-surface-active" />
                    <span className="block h-9 w-40 rounded-[var(--r-md)] bg-surface-active" />
                    <span className="mt-4 block h-12 w-full max-w-md rounded-[var(--r-lg)] bg-surface-active" />
                </div>
            </WalletPanel>
        );
    }

    return (
        <WalletPanel className="wallet-balance-panel">
            <div className="wallet-balance-row">
                <div className="min-w-0">
                    <p className="wallet-balance-label">剩余积分</p>
                    <p aria-hidden className="wallet-balance-value">{formatCount(shown)}</p>
                    {/* 数字在跳动，屏幕阅读器只播报落定后的终值，不逐帧播报。 */}
                    <span className="sr-only" aria-live="polite">{`当前余额 ${formatCount(balance)} 积分`}</span>
                    <p className="wallet-balance-meta">更新于 {formatDateTime(wallet.updatedAt)}</p>
                </div>
                <Button className="h-11 shrink-0 px-6" type="primary" onClick={onTopUp}>
                    充值
                </Button>
            </div>
            <div className="wallet-metrics">
                <Readout label="累计获得" value={wallet.lifetimeIn} />
                <Readout label="累计消耗" value={wallet.lifetimeOut} />
            </div>
            {error ? (
                <Callout className="mt-3" tone="warning" title="余额刷新失败" action={<Button size="small" onClick={onRetry}>重新加载</Button>}>
                    {error}
                </Callout>
            ) : null}
        </WalletPanel>
    );
}

export function WalletPage() {
    const accountName = useUserStore((state) => state.user?.displayName || state.user?.username || "");

    const [wallet, setWallet] = useState<CreditWallet | null>(null);
    const [walletLoading, setWalletLoading] = useState(true);
    const [walletError, setWalletError] = useState("");
    const [notice, setNotice] = useState<{ tone: CalloutTone; text: string } | null>(null);
    // 一个计数器驱动三个 Zone：刷新、充值成功后余额/档位/流水一起重取，读数不会互相矛盾。
    const [revision, setRevision] = useState(0);
    const topUpRef = useRef<HTMLDivElement | null>(null);

    useEffect(() => {
        let cancelled = false;
        setWalletLoading(true);
        setWalletError("");
        getCreditWallet()
            .then((next) => {
                if (!cancelled) setWallet(next);
            })
            .catch((cause) => {
                if (!cancelled) setWalletError(errorMessage(cause, "余额加载失败，请稍后重试。"));
            })
            .finally(() => {
                if (!cancelled) setWalletLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [revision]);

    const reloadAll = useCallback(() => setRevision((value) => value + 1), []);
    const scrollToTopUp = useCallback(() => {
        topUpRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }, []);

    return (
        <WorkspacePage>
            {/* 负边距正好抵消 WorkspacePage 的 px-3 py-3：横幅铺满内容区，从这一屏的第一个像素开始。
                标题压在剧照上，与首页是同一套语言；外壳页头在这条路由上不再出现。 */}
            <section className="wallet-hero -mx-3 -mt-3 sm:-mx-4 sm:-mt-4 xl:-mx-5" aria-labelledby="wallet-hero-title">
                <img className="wallet-hero-art" src="/home/hero.webp" alt="" aria-hidden="true" draggable={false} />
                <div className="wallet-hero-copy">
                    <p className="wallet-hero-brand">Creation credits</p>
                    <h1 id="wallet-hero-title" className="wallet-hero-title">
                        积分中心
                    </h1>
                    <p className="wallet-hero-sub">账户积分由平台统一计费：每次生成按模型用量扣减，失败自动退回。</p>
                    <div className="wallet-hero-actions">
                        <button type="button" className="wallet-hero-action" disabled={walletLoading} onClick={reloadAll}>
                            <RefreshCw className={cn(walletLoading && "animate-spin")} aria-hidden />
                            刷新
                        </button>
                        {accountName ? <span className="wallet-hero-account">{accountName}</span> : null}
                    </div>
                </div>
            </section>

            {/* 全局 message 在本项目里是关闭的，所有反馈都必须落在页面内。 */}
            {notice ? (
                <Callout tone={notice.tone} className="mt-3" onClose={() => setNotice(null)}>
                    {notice.text}
                </Callout>
            ) : null}

            {/* Zone A 与下面三段共用同一套段落头：四段的节奏一致，余额靠读数大小而不是靠没有标题来当主角。 */}
            <section className="wallet-section wallet-section-lead" aria-label="账户余额">
                <WalletSectionHead eyebrow="Balance" title="账户余额" />
                <BalanceCard wallet={wallet} loading={walletLoading} error={walletError} onRetry={reloadAll} onTopUp={scrollToTopUp} />
            </section>

            <div ref={topUpRef} className="scroll-mt-16">
                <CreditTopUpSection revision={revision} onNotice={setNotice} onSettled={reloadAll} />
            </div>

            <CreditOrdersSection revision={revision} onNotice={setNotice} />

            <CreditLedgerSection revision={revision} onTopUp={scrollToTopUp} />
        </WorkspacePage>
    );
}
