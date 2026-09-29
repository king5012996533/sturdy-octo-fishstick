import { useEffect, useState, type ReactNode } from "react";

import type { CalloutTone } from "@/components/ui/product/callout";
import { formatCount } from "@/lib/format-usage";
import { cn } from "@/lib/utils";
import { isInsufficientCredits } from "@/services/api/credit";

/**
 * 积分中心三个 Zone 共用的物料。
 *
 * 面料：明色走 --surface（白），暗色用登录页已定型的玻璃面——顶部高光渐变 + 内高光 +
 * 外层深投影。描边不写死颜色，走 --workspace-border：用户端外壳已把它在明色下定为
 * 深色发丝、暗色下定为白色发丝，所以两个主题都不需要额外分支。
 */
const walletPanelClass = cn(
    "rounded-[var(--r-wide-tight)] border border-[var(--workspace-border)] bg-surface",
    "dark:bg-[linear-gradient(180deg,rgba(255,255,255,0.062),rgba(255,255,255,0.018))]",
    "dark:shadow-[0_40px_90px_rgba(0,0,0,0.5),inset_0_1px_0_rgba(255,255,255,0.06)]",
);

export function WalletPanel({ children, className }: { children: ReactNode; className?: string }) {
    return <div className={cn(walletPanelClass, "p-4 sm:p-5", className)}>{children}</div>;
}

/**
 * 加载指示要压到 300ms 之后才出现：接口正常时页面不该先闪一下骨架。
 *
 * 这里不做"超过 15 秒追加提示"的升级：apiClient 的超时是 4 秒，请求不可能挂到 15 秒，
 * 慢下来时用户拿到的是可重试的错误态。
 */
export function useDelayedLoading(loading: boolean, delay = 300) {
    const [visible, setVisible] = useState(false);
    useEffect(() => {
        if (!loading) {
            setVisible(false);
            return;
        }
        const timer = window.setTimeout(() => setVisible(true), delay);
        return () => window.clearTimeout(timer);
    }, [loading, delay]);
    return loading && visible;
}

/** 服务端返回的中文文案要原样透出，只有拿不到文案时才回退到本地兜底。 */
export function errorMessage(error: unknown, fallback: string) {
    return error instanceof Error && error.message ? error.message : fallback;
}

/**
 * 余额不足（402 + insufficient_credits）要告诉用户"怎么办"：充值入口就在本页上方，
 * 把这种情况渲染成普通错误、或让用户去联系客服，都是把可自愈的问题变成工单。
 */
export function errorNotice(error: unknown, fallback: string): { tone: CalloutTone; text: string } {
    const text = errorMessage(error, fallback);
    if (isInsufficientCredits(error)) return { tone: "warning", text: `${text} 充值后积分立即生效，可在本页上方完成充值。` };
    return { tone: "error", text };
}

/** 变动金额：符号即方向（U+2212 减号与加号等宽），数字只做千分位，不做任何单位换算。 */
export function formatCreditDelta(amount: number) {
    if (!Number.isFinite(amount) || amount === 0) return formatCount(0);
    return `${amount > 0 ? "+" : "−"}${formatCount(Math.abs(amount))}`;
}
