import type { TaskChargeEstimate } from "@/hooks/use-task-charge-quote";
import { creditUnitRateLabel } from "@/lib/credit-price-label";
import type { TaskChargeQuote } from "@/services/api/credit";
import { cn } from "@/lib/utils";

/**
 * 生成按钮旁的积分消耗提示。
 *
 * 数字全部来自后端的试算接口：渠道、档位、倍率都在服务端，前端再乘一遍迟早会与实扣分叉。
 * 这里只做三件事——把金额说清楚、把算式说清楚、把"为什么现在生成不了"说清楚。
 */
export function CreationCreditEstimate({ estimate, className, compact = false }: { estimate: TaskChargeEstimate; className?: string; compact?: boolean }) {
    // 本地/桌面形态没有账号库也没有计费：不显示任何消耗提示，而不是显示一个 0。
    if (estimate.unsupported) return null;
    const quote = estimate.quote;
    if (estimate.error) {
        return (
            <span className={cn("creation-credit-estimate is-error", className)} title={estimate.error} role="status">
                {estimate.error}
            </span>
        );
    }
    if (!quote) {
        return estimate.loading ? (
            <span className={cn("creation-credit-estimate is-loading", className)} role="status">
                正在试算消耗…
            </span>
        ) : null;
    }
    if (!quote.priced) {
        // 后端对未定价直接回 409，正常走不到这里；留一条兜底是为了"未定价"永远不被显示成免费。
        return (
            <span className={cn("creation-credit-estimate is-error", className)} role="status">
                该模型尚未定价，暂时无法生成
            </span>
        );
    }
    if (quote.credits <= 0) {
        return (
            <span className={cn("creation-credit-estimate", className)} role="status">
                本次不消耗积分
            </span>
        );
    }
    const surcharge = quote.surchargeCredits ?? 0;
    const minimum = quote.minimumBalance ?? 0;
    // 文本的水位与"本次扣多少"是两件事：余额不到水位时用户根本发不出去。这时必须说清
    // "要留多少"，只把起步价染红会让人以为"充值 1 积分就能继续"。
    if (!estimate.sufficient && minimum > quote.credits) {
        return (
            <span className={cn("creation-credit-estimate is-error", className)} role="status" title={chargeHint(quote, surcharge)}>
                <span className="creation-credit-estimate-label">余额不足</span>
                <span className="creation-credit-estimate-balance">余额需 ≥ {minimum.toLocaleString("zh-CN")} 积分（当前 {(estimate.balance ?? 0).toLocaleString("zh-CN")}）</span>
            </span>
        );
    }
    return (
        <span className={cn("creation-credit-estimate", estimate.sufficient ? undefined : "is-error", className)} role="status" title={chargeHint(quote, surcharge)}>
            <span className="creation-credit-estimate-label">{estimate.sufficient ? "预计预扣" : "余额不足"}</span>
            <strong>{quote.credits.toLocaleString("zh-CN")}</strong>
            <span className="creation-credit-estimate-unit">积分</span>
            {surcharge > 0 ? <em className="creation-credit-estimate-balance">含素材加收 {surcharge.toLocaleString("zh-CN")}</em> : null}
            {!compact && estimate.balance !== null ? <em className="creation-credit-estimate-balance">余额 {estimate.balance.toLocaleString("zh-CN")}</em> : null}
        </span>
    );
}

/** chargeHint 把"单价 × 用量"渲染成悬停可看的算式；单价文案与模型广场共用一份。 */
function chargeHint(quote: TaskChargeQuote, surchargeCredits = 0) {
    if (quote.sellUnitPrice === null) return "";
    const rate = creditUnitRateLabel(quote.unit, quote.sellUnitPrice);
    const extra = surchargeCredits > 0 ? ` + 素材加收 ${surchargeCredits} 积分` : "";
    // 文本只有起步价是确定的，真实费用收尾才结算——不写清楚，用户会把"1 积分/次"
    // 当成这次对话的总价。
    if (quote.minimumBalance > 0) {
        return `${rate}（起步价，任务收尾时按实际 token 用量结算；余额需 ≥ ${quote.minimumBalance.toLocaleString("zh-CN")} 积分）${extra}`;
    }
    switch (quote.unit) {
        case "IMAGE":
            return `${rate} × ${quote.quantity} 张${extra}`;
        case "SECOND":
            return `${rate} × ${quote.quantity} 秒${extra}`;
        case "TOKEN_1M":
            return `${rate}（起步价，不足一次调用按一次计）${extra}`;
        default:
            return `${rate}${extra}`;
    }
}
