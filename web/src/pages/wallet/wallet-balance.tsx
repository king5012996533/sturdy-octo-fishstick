import { Button } from "antd";
import { useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";

import { WorkspaceErrorState } from "@/components/layout/workspace-state";
import { Callout } from "@/components/ui/product/callout";
import { formatCount, formatDateTime } from "@/lib/format-usage";
import type { CreditWallet } from "@/services/api/credit";

import { WalletPanel, useDelayedLoading } from "./wallet-kit";

/**
 * Zone A —— 余额区。
 *
 * 版式是「左读数 + 右台账」两栏，而不是把读数、动作、累计数各自摊在四角：读数与它的
 * 下一步动作（充值）同属左栏，视线不用横穿整张卡去找按钮；累计数与更新时间收进右栏，
 * 靠一道 hairline 与左栏分开。宽屏下卡片自然被两栏填满，不需要再在卡里套一个盒子
 * 来"填"右侧的空白——套盒子正是让这一块看起来像后台表单的原因。
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

type BalanceCardProps = {
    wallet: CreditWallet | null;
    loading: boolean;
    error: string;
    onRetry: () => void;
    onTopUp: () => void;
    onLedger: () => void;
};

/** 零余额是状态而不是错误：卡片照常完整渲染，只是读数降一档字号。 */
export function BalanceCard({ wallet, loading, error, onRetry, onTopUp, onLedger }: BalanceCardProps) {
    const showSkeleton = useDelayedLoading(loading);
    const balance = wallet?.balance ?? 0;
    const shown = useCountUp(balance);

    if (!wallet) {
        if (error) return <WorkspaceErrorState title="余额加载失败" description={error} onRetry={onRetry} />;
        if (!showSkeleton) return null;
        return (
            <WalletPanel>
                {/* 骨架复刻两栏版式：数字落位之前，卡片的高度已经稳定，加载完成不会跳版。 */}
                <div className="wallet-balance-grid" aria-busy="true" aria-live="polite">
                    <div className="wallet-balance-lead">
                        <span className="block h-4 w-16 rounded-[var(--r-sm)] bg-surface-active" />
                        <span className="mt-3 block h-12 w-40 rounded-[var(--r-md)] bg-surface-active" />
                        <span className="mt-5 block h-11 w-28 rounded-[var(--r-lg)] bg-surface-active" />
                    </div>
                    <div className="wallet-balance-stats">
                        <span className="block h-4 w-2/3 rounded-[var(--r-sm)] bg-surface-active" />
                        <span className="block h-4 w-2/3 rounded-[var(--r-sm)] bg-surface-active" />
                    </div>
                </div>
            </WalletPanel>
        );
    }

    return (
        <WalletPanel>
            <div className="wallet-balance-grid">
                <div className="wallet-balance-lead">
                    <p className="wallet-balance-label">剩余积分</p>
                    <p aria-hidden className="wallet-balance-value">{formatCount(shown)}</p>
                    {/* 数字在跳动，屏幕阅读器只播报落定后的终值，不逐帧播报。 */}
                    <span className="sr-only" aria-live="polite">{`当前余额 ${formatCount(balance)} 积分`}</span>
                    <div className="wallet-balance-actions">
                        <Button className="h-11 shrink-0 px-6" type="primary" onClick={onTopUp}>
                            充值
                        </Button>
                        <button
                            type="button"
                            className="wallet-balance-secondary rounded-[var(--r-sm)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
                            onClick={onLedger}
                        >
                            查看积分流水
                        </button>
                    </div>
                </div>
                <div className="wallet-balance-stats">
                    <dl className="wallet-stat-list">
                        <div className="wallet-stat">
                            <dt className="wallet-stat-label">累计获得</dt>
                            <dd className="wallet-stat-value">{formatCount(wallet.lifetimeIn)}</dd>
                        </div>
                        <div className="wallet-stat">
                            <dt className="wallet-stat-label">累计消耗</dt>
                            <dd className="wallet-stat-value">{formatCount(wallet.lifetimeOut)}</dd>
                        </div>
                    </dl>
                    <p className="wallet-stat-freshness">更新于 {formatDateTime(wallet.updatedAt)}</p>
                </div>
            </div>
            {error ? (
                <Callout className="mt-4" tone="warning" title="余额刷新失败" action={<Button size="small" onClick={onRetry}>重新加载</Button>}>
                    {error}
                </Callout>
            ) : null}
        </WalletPanel>
    );
}
