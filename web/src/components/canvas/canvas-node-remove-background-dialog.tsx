import { useEffect, useState } from "react";
import { Button, Modal } from "antd";
import { Eraser } from "lucide-react";

import { REMOVE_BACKGROUND_PROMPT } from "@/lib/canvas/canvas-image-remove-background";
import { readImageMeta } from "@/lib/image-utils";
import { useTaskChargeQuote, type TaskChargeEstimate } from "@/hooks/use-task-charge-quote";
import { creditUnitRateLabel } from "@/lib/credit-price-label";
import { defaultConfig, type AiConfig } from "@/stores/use-config-store";

type CanvasNodeRemoveBackgroundDialogProps = {
    /** 已解析成可直接渲染的地址；为空表示原图还没就绪。 */
    source: string;
    /** 收敛到抠图模型的配置；平台没上架这条模型时为空，此时按钮禁用并说明原因。 */
    config: AiConfig | null;
    onClose: () => void;
    onConfirm: () => void;
};

/**
 * 移除背景的确认弹窗。
 *
 * 三件事必须在这里说清楚：这是付费动作、这一张要花多少、拿到的是什么。价格一律走
 * 后端试算（与实扣同一段解析），前端不写死数字——运营在后台调价之后，界面上的读数
 * 不能停在旧值上。
 */
export function CanvasNodeRemoveBackgroundDialog({ source, config, onClose, onConfirm }: CanvasNodeRemoveBackgroundDialogProps) {
    const open = Boolean(source);
    const [size, setSize] = useState<{ width: number; height: number } | null>(null);
    const estimate = useTaskChargeQuote({
        enabled: open && Boolean(config),
        mode: "image",
        config: config || defaultConfig,
        prompt: REMOVE_BACKGROUND_PROMPT,
        inputSummary: { imageCount: 1 },
    });

    useEffect(() => {
        if (!open) {
            setSize(null);
            return;
        }
        setSize(null);
        let cancelled = false;
        void readImageMeta(source).then((meta) => {
            if (!cancelled) setSize({ width: meta.width, height: meta.height });
        });
        return () => {
            cancelled = true;
        };
    }, [open, source]);

    // 试算失败（未定价、余额不足）或通道缺失时不给按：按下去也只会拿到一个 402/409，
    // 让用户先看到原因，比让他点一次再被拒要好。
    const blocked = !config || !source || Boolean(estimate.error) || !estimate.sufficient;

    return (
        <Modal title={null} open={open} onCancel={onClose} footer={null} width={720} centered destroyOnHidden>
            <div className="space-y-5">
                <div>
                    <h2 className="text-xl font-semibold">移除背景</h2>
                    <p className="mt-2 text-sm opacity-60">抠掉背景，输出透明底 PNG，原图保留。尺寸与原图一致，不做放大。</p>
                </div>
                <div className="grid gap-6 md:grid-cols-[minmax(240px,1fr)_minmax(240px,1fr)]">
                    <div className="rounded-xl border p-4">
                        <div className="grid min-h-[240px] place-items-center rounded-lg bg-black/5">
                            <img src={source} alt="" className="max-h-[300px] max-w-full rounded-lg object-contain shadow-xl" draggable={false} />
                        </div>
                        <div className="mt-3 flex items-center justify-between text-sm">
                            <span className="opacity-60">原图</span>
                            <span className="font-semibold">{size ? `${size.width} × ${size.height} px` : "读取中"}</span>
                        </div>
                    </div>
                    <div className="space-y-4 py-1 text-sm">
                        <div className="rounded-xl border px-4 py-3">
                            <div className="flex items-center justify-between">
                                <span className="opacity-60">输出尺寸</span>
                                <span className="font-semibold">{size ? `${size.width} × ${size.height} px` : "读取中"}</span>
                            </div>
                            <div className="mt-1 flex items-center justify-between">
                                <span className="opacity-60">输出格式</span>
                                <span className="font-semibold">PNG（透明底）</span>
                            </div>
                        </div>
                        <RemoveBackgroundCharge estimate={estimate} available={Boolean(config)} />
                    </div>
                </div>
                <div className="flex justify-end gap-2">
                    <Button size="large" onClick={onClose}>
                        取消
                    </Button>
                    <Button type="primary" size="large" icon={<Eraser className="size-4" />} disabled={blocked} onClick={onConfirm}>
                        移除背景
                    </Button>
                </div>
            </div>
        </Modal>
    );
}

function RemoveBackgroundCharge({ estimate, available }: { estimate: TaskChargeEstimate; available: boolean }) {
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
    // 试算要等防抖加一次往返，这期间 quote 一定是空的。按 loading 有没有置位来分流，会让
    // 弹窗每次打开都先闪一句"该通道尚未定价"再变成真实价格——把"还没问到"说成"没定价"，
    // 用户看到的是"这个功能坏了"。未定价只能在后端明确回了 priced=false 之后才说。
    if (!estimate.quote) {
        return <div className="rounded-xl border px-4 py-3 opacity-60">正在试算消耗…</div>;
    }
    if (!estimate.quote.priced) {
        return (
            <div className="rounded-xl border px-4 py-3 text-status-error">
                该通道尚未定价，暂时无法使用
            </div>
        );
    }
    const rate = estimate.quote.sellUnitPrice === null ? "" : creditUnitRateLabel(estimate.quote.unit, estimate.quote.sellUnitPrice);
    return (
        <div className="rounded-xl border px-4 py-3" title={rate}>
            <div className="flex items-center justify-between">
                <span className="opacity-60">本次消耗</span>
                <span className="font-semibold">{estimate.quote.credits.toLocaleString("zh-CN")} 积分</span>
            </div>
            {estimate.balance !== null ? (
                <div className="mt-1 flex items-center justify-between">
                    <span className="opacity-60">当前余额</span>
                    <span>{estimate.balance.toLocaleString("zh-CN")} 积分</span>
                </div>
            ) : null}
            {!estimate.sufficient ? <div className="mt-1 font-medium text-status-error">余额不足，请先充值</div> : null}
        </div>
    );
}
