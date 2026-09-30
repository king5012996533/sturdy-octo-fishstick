import { useMessageCharge } from "@/hooks/use-message-charge";
import { cn } from "@/lib/utils";

import type { CreationStatus } from "./creation-types";

/**
 * 消息下方的"这次生成花了多少积分"。
 *
 * 数字只能来自流水：提交时按报价预扣，失败后由服务端按"上游有没有收到请求"决定退不退，
 * 界面自己记一份账迟早会和用户钱包对不上。所以这里不做任何金额计算，只把流水讲清楚：
 * 花了多少、退了多少、还是根本没产生记录——最后一种不会显示成 0 积分。
 */
export function CreationMessageCharge({ taskIds, status, className }: { taskIds: string[]; status?: CreationStatus; className?: string }) {
    const terminal = status === "done" || status === "error" || status === "cancelled";
    const charge = useMessageCharge(taskIds, {
        // 桌面与本地没有账号库也没有计费，编译期就关掉这条通道（连同后台代码一起被摇掉）。
        enabled: __BEEFTV_HOSTED_AUTH__ && terminal,
        expectRefund: status === "error" || status === "cancelled",
    });
    if (!terminal) return null;
    if (charge.error) {
        return (
            <div className={cn("creation-message-charge is-error", className)} title={charge.error} role="status">
                积分记录读取失败
            </div>
        );
    }
    // 查过但一条流水都没有：可能是上游整单失败没扣，也可能是任务根本没走计费。
    // 如实说"没有记录"，不写 0 积分——0 会被读成"这次免费"。
    if (charge.charged <= 0) {
        return (
            <div className={cn("creation-message-charge is-muted", className)} title="没有查到这次生成对应的扣费流水；如果余额变动异常，请联系客服核对。" role="status">
                本次没有扣费记录
            </div>
        );
    }
    const settled = charge.refunded > 0;
    // 失败却没退：请求已经发到上游时按规则不退（上游可能已经计费）。这句话必须自己出现，
    // 否则用户只看到一个扣了钱又没产出的数字，会以为是漏退了。
    const withheld = !settled && (status === "error" || status === "cancelled");
    return (
        <div
            className={cn("creation-message-charge", settled && "is-refunded", className)}
            title={
                settled
                    ? `预扣 ${charge.charged} 积分，其中 ${charge.refunded} 积分因未实际提交到上游而退回。`
                    : withheld
                      ? "提交生成时按报价预扣。这次请求已经发到上游，上游可能已经计费，所以预扣不退回；任务详情里能看到判定原因。"
                      : "提交生成时按报价预扣；任务失败且上游未收到请求时会自动退回。"
            }
            role="status"
        >
            {charge.net > 0 ? (
                <>
                    <span>本次消耗</span>
                    <strong>{charge.net.toLocaleString("zh-CN")}</strong>
                    <span>积分</span>
                </>
            ) : (
                <>
                    <span>本次已退回</span>
                    <strong>{charge.charged.toLocaleString("zh-CN")}</strong>
                    <span>积分</span>
                </>
            )}
            {settled && charge.net > 0 ? <em>已退 {charge.refunded.toLocaleString("zh-CN")}</em> : null}
            {withheld ? <em>未退回</em> : null}
        </div>
    );
}
