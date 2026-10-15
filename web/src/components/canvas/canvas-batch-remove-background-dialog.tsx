import { Button, Modal } from "antd";
import { Eraser } from "lucide-react";

import { REMOVE_BACKGROUND_PROMPT } from "@/lib/canvas/canvas-image-remove-background";
import { creditUnitRateLabel } from "@/lib/credit-price-label";
import { useTaskChargeQuote } from "@/hooks/use-task-charge-quote";
import { defaultConfig, type AiConfig } from "@/stores/use-config-store";

type CanvasBatchRemoveBackgroundDialogProps = {
    /** 已解析成可直接渲染的地址，按画布上的先后顺序排列；空数组表示不弹窗。 */
    sources: string[];
    /** 收敛到抠图模型的配置；平台没上架这条模型时为空，此时按钮禁用并说明原因。 */
    config: AiConfig | null;
    onClose: () => void;
    onConfirm: () => void;
};

const THUMBNAIL_LIMIT = 6;

/**
 * 批量移除背景的确认弹窗。
 *
 * 与单张弹窗同一套口径（价格一律走后端试算，前端不写死数字），差别只有一个：这里的花费
 * 是"张数 × 单张价"。
 *
 * 这个乘法之所以成立，是因为抠图是**一任务一张图**的固定端点：能力合同把 count 固定成 1、
 * 只有一行不分档的价目、也没有参考图加收。哪天这条线上了档位或者加收，总价就必须改由
 * 后端试算给出，不能继续乘。到那时这里要改成"张数交给后端"而不是前端乘。
 */
export function CanvasBatchRemoveBackgroundDialog({ sources, config, onClose, onConfirm }: CanvasBatchRemoveBackgroundDialogProps) {
    const count = sources.length;
    const open = count > 0;
    const estimate = useTaskChargeQuote({
        enabled: open && Boolean(config),
        mode: "image",
        config: config || defaultConfig,
        prompt: REMOVE_BACKGROUND_PROMPT,
        inputSummary: { imageCount: 1 },
    });

    const unitPrice = estimate.quote?.priced ? estimate.quote.sellUnitPrice : null;
    const totalCredits = unitPrice === null ? null : unitPrice * count;
    const enough = estimate.unsupported || totalCredits === null || estimate.balance === null ? estimate.sufficient : estimate.balance >= totalCredits;
    // 试算没回来之前不给按：批量的合计是这里拼出来的，报价还没到就点下去，等于在不知道总价的
    // 情况下一次性提交 N 个任务。通道缺失、余额不足同理——按下去只会让前排几张先扣费再失败。
    //
    // 本地/桌面形态没有计费端口（unsupported），那时 totalCredits 恒为空，不能把"没有计费"
    // 当成"没问到价"，否则本地形态永远按不动这个按钮。
    const blocked = !config || !count || Boolean(estimate.error) || (!estimate.unsupported && (totalCredits === null || !enough));

    return (
        <Modal title={null} open={open} onCancel={onClose} footer={null} width={720} centered destroyOnHidden>
            <div className="space-y-5">
                <div>
                    <h2 className="text-xl font-semibold">批量移除背景</h2>
                    <p className="mt-2 text-sm opacity-60">逐张抠掉背景，每张生成一个透明底 PNG 子节点挂在原图右侧，原图保留。尺寸与原图一致，不做放大。</p>
                </div>
                <div className="rounded-xl border p-4">
                    <div className="flex flex-wrap gap-2">
                        {sources.slice(0, THUMBNAIL_LIMIT).map((source) => (
                            <img key={source} src={source} alt="" className="size-16 rounded-lg bg-black/5 object-cover" draggable={false} />
                        ))}
                        {count > THUMBNAIL_LIMIT ? (
                            <div className="grid size-16 place-items-center rounded-lg bg-black/5 text-sm font-semibold">+{count - THUMBNAIL_LIMIT}</div>
                        ) : null}
                    </div>
                    <div className="mt-3 flex items-center justify-between text-sm">
                        <span className="opacity-60">共选中</span>
                        <span className="font-semibold">{count} 张</span>
                    </div>
                </div>
                <BatchRemoveBackgroundCharge estimate={estimate} available={Boolean(config)} count={count} totalCredits={totalCredits} enough={enough} />
                <div className="flex justify-end gap-2">
                    <Button size="large" onClick={onClose}>
                        取消
                    </Button>
                    <Button type="primary" size="large" icon={<Eraser className="size-4" />} disabled={blocked} onClick={onConfirm}>
                        开始抠图（{count} 张）
                    </Button>
                </div>
            </div>
        </Modal>
    );
}

function BatchRemoveBackgroundCharge({ estimate, available, count, totalCredits, enough }: { estimate: ReturnType<typeof useTaskChargeQuote>; available: boolean; count: number; totalCredits: number | null; enough: boolean }) {
    if (!available) {
        return (
            <div className="rounded-xl border px-4 py-3 text-status-error">
                当前未配置移除背景通道，请联系平台管理员。
            </div>
        );
    }
    // 本地/桌面形态没有账号库也没有计费：不显示任何消耗提示，而不是显示一个 0。
    if (estimate.unsupported) return null;
    if (estimate.error) {
        return (
            <div className="rounded-xl border px-4 py-3 text-status-error">
                {estimate.error}
            </div>
        );
    }
    if (!estimate.quote) {
        return <div className="rounded-xl border px-4 py-3 opacity-60">正在试算消耗…</div>;
    }
    if (!estimate.quote.priced || totalCredits === null || estimate.quote.sellUnitPrice === null) {
        return (
            <div className="rounded-xl border px-4 py-3 text-status-error">
                该通道尚未定价，暂时无法使用
            </div>
        );
    }
    const rate = creditUnitRateLabel(estimate.quote.unit, estimate.quote.sellUnitPrice);
    return (
        <div className="rounded-xl border px-4 py-3" title={rate}>
            <div className="flex items-center justify-between">
                <span className="opacity-60">单张</span>
                <span className="font-semibold">{rate}</span>
            </div>
            <div className="mt-1 flex items-center justify-between">
                <span className="opacity-60">合计</span>
                <span className="font-semibold">{`${count} × ${estimate.quote.sellUnitPrice.toLocaleString("zh-CN")} = ${totalCredits.toLocaleString("zh-CN")} 积分`}</span>
            </div>
            {estimate.balance !== null ? (
                <div className="mt-1 flex items-center justify-between">
                    <span className="opacity-60">当前余额</span>
                    <span>{estimate.balance.toLocaleString("zh-CN")} 积分</span>
                </div>
            ) : null}
            {!enough ? <div className="mt-1 font-medium text-status-error">余额不足，请先充值</div> : null}
            <div className="mt-2 text-xs opacity-60">每张单独扣费，失败的那张会退回。</div>
        </div>
    );
}
