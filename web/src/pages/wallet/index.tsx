import { Button } from "antd";
import { RefreshCw } from "lucide-react";
import { useReducedMotion } from "motion/react";
import { useCallback, useEffect, useRef, useState } from "react";

import { PageHeader, WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceErrorState } from "@/components/layout/workspace-state";
import { Callout, type CalloutTone } from "@/components/ui/product/callout";
import { formatCount, formatDateTime } from "@/lib/format-usage";
import { cn } from "@/lib/utils";
import { getCreditWallet, type CreditWallet } from "@/services/api/credit";
import { useUserStore } from "@/stores/use-user-store";

import { CreditLedgerSection } from "./wallet-ledger";
import { CreditTopUpSection } from "./wallet-top-up";
import { WalletPanel, errorMessage, useDelayedLoading } from "./wallet-kit";

/**
 * 用户端积分中心（/wallet）：余额 → 充值 → 流水，三级信息层级。
 *
 * 余额是唯一主角，充值是它的下一步动作，流水是账目凭据。三个 Zone 各自加载、各自失败、
 * 各自重试：账目接口抖动不能让用户看不到余额，反之亦然。
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
        <div className="rounded-[var(--r-lg)] bg-surface-secondary px-3 py-2">
            <p className="text-[var(--fs-label)] text-foreground/58">{label}</p>
            <p className="mt-0.5 font-mono text-[var(--fs-body)] tabular-nums text-foreground">{formatCount(value)}</p>
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
            <WalletPanel className="mt-4">
                <div className="flex flex-col gap-3" aria-busy="true" aria-live="polite">
                    <span className="block h-4 w-20 rounded-[var(--r-sm)] bg-surface-active" />
                    <span className="block h-9 w-40 rounded-[var(--r-md)] bg-surface-active" />
                    <span className="mt-4 block h-12 w-full max-w-md rounded-[var(--r-lg)] bg-surface-active" />
                </div>
            </WalletPanel>
        );
    }

    const zeroBalance = balance <= 0;
    return (
        <WalletPanel className="mt-4">
            <div className={cn("flex flex-col gap-3", !zeroBalance && "sm:flex-row sm:items-start sm:justify-between")}>
                <div className="min-w-0">
                    <p className="text-[var(--fs-label)] tracking-[0.02em] text-foreground/58">剩余积分</p>
                    <p
                        aria-hidden
                        className={cn(
                            "mt-1 font-[family-name:var(--font-display)] font-semibold tabular-nums text-foreground",
                            zeroBalance ? "text-[var(--fs-title)] leading-7" : "text-[var(--fs-display)] leading-[1.1]",
                        )}
                    >
                        {formatCount(shown)}
                    </p>
                    {/* 数字在跳动，屏幕阅读器只播报落定后的终值，不逐帧播报。 */}
                    <span className="sr-only" aria-live="polite">{`当前余额 ${formatCount(balance)} 积分`}</span>
                    <p className="mt-1 text-[var(--fs-label)] text-foreground/58">更新于 {formatDateTime(wallet.updatedAt)}</p>
                </div>
                <Button className="h-11 shrink-0 px-5" type="primary" onClick={onTopUp}>
                    充值
                </Button>
            </div>
            {/* 底部一道 hairline 分层：读数在上、累计读数在下，不靠投影造层级。 */}
            <div className="mt-4 border-t border-[var(--workspace-border)] pt-4">
                <div className="grid max-w-md grid-cols-2 gap-3">
                    <Readout label="累计获得" value={wallet.lifetimeIn} />
                    <Readout label="累计消耗" value={wallet.lifetimeOut} />
                </div>
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
            <PageHeader
                title="积分中心"
                description="账户积分由平台统一计费：每次生成按模型用量扣减，失败自动退回。"
                meta={
                    accountName ? (
                        <span className="rounded-[var(--r-full)] border border-[var(--workspace-border)] bg-surface-secondary px-2 py-0.5 font-mono text-[var(--fs-label)] text-foreground/70">{accountName}</span>
                    ) : null
                }
                actions={
                    <Button icon={<RefreshCw className="size-3.5" />} loading={walletLoading} onClick={reloadAll}>
                        刷新
                    </Button>
                }
            />

            {/* 全局 message 在本项目里是关闭的，所有反馈都必须落在页面内。 */}
            {notice ? (
                <Callout tone={notice.tone} className="mt-3" onClose={() => setNotice(null)}>
                    {notice.text}
                </Callout>
            ) : null}

            <BalanceCard wallet={wallet} loading={walletLoading} error={walletError} onRetry={reloadAll} onTopUp={scrollToTopUp} />

            <div ref={topUpRef} className="scroll-mt-16">
                <CreditTopUpSection revision={revision} onNotice={setNotice} onSettled={reloadAll} />
            </div>

            <CreditLedgerSection revision={revision} onTopUp={scrollToTopUp} />
        </WorkspacePage>
    );
}
