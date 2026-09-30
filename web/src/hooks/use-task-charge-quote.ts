import { useEffect, useMemo, useRef, useState } from "react";

import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { quoteTaskCharge, type TaskChargeQuote } from "@/services/api/credit";
import { taskChargeQuoteInput, type BackendGenerationMode } from "@/services/api/generation-task";
import { ApiError } from "@/services/api/request";
import type { AiConfig } from "@/stores/use-config-store";

/**
 * 生成前的积分试算。
 *
 * 价格只能由后端算：渠道、档位、倍率都在服务端，前端自己乘一遍迟早会与实扣分叉。
 * 这里只负责"什么时候问、问到之后怎么展示"，不做任何金额计算。
 */
export type TaskChargeEstimate = {
    loading: boolean;
    quote: TaskChargeQuote | null;
    balance: number | null;
    /** 余额是否够这次扣款。由后端判断，前端不自己比大小。 */
    sufficient: boolean;
    /** 未定价、余额不足、模型不可用等原因，界面必须原样展示，不能吞掉。 */
    error: string;
    /** 当前实例没有计费能力（本地/桌面形态），此时界面不该显示任何消耗提示。 */
    unsupported: boolean;
};

const EMPTY_ESTIMATE: TaskChargeEstimate = { loading: false, quote: null, balance: null, sufficient: true, error: "", unsupported: false };

export function useTaskChargeQuote(options: {
    enabled: boolean;
    mode: BackendGenerationMode;
    config: AiConfig;
    prompt: string;
    /** 与提交共用的素材摘要（图片/视频/音频的数量），决定视频操作与路由。 */
    inputSummary?: { imageCount?: number; videoCount?: number; audioCount?: number };
    videoEditOperation?: string;
}): TaskChargeEstimate {
    const { enabled, mode, config, prompt, inputSummary, videoEditOperation } = options;
    const payload = useMemo(
        () => (enabled ? taskChargeQuoteInput({ mode, prompt, config, inputSummary, videoEditOperation }) : null),
        // config 的每次变更都来自用户改选模型或参数，这里按整体引用比较即可；
        // 逐字段展开反而会在新增能力参数时漏掉一处，报价随之停留在旧值。
        [enabled, mode, config, prompt, inputSummary, videoEditOperation],
    );
    // 串行化后做防抖：用户拖时长滑块或改张数时会连点，每次改都打一次后端既慢又吵。
    const debouncedPayload = useDebouncedValue(payload ? JSON.stringify(payload) : "", 250);
    const [estimate, setEstimate] = useState<TaskChargeEstimate>(EMPTY_ESTIMATE);
    const requestSeq = useRef(0);

    useEffect(() => {
        if (!enabled || !debouncedPayload) {
            setEstimate(EMPTY_ESTIMATE);
            return;
        }
        const seq = ++requestSeq.current;
        setEstimate((current) => ({ ...current, loading: true, error: "" }));
        void (async () => {
            try {
                const result = await quoteTaskCharge(JSON.parse(debouncedPayload));
                // 迟到的响应不能覆盖新一次请求的结果：用户已经改了参数，
                // 界面却回退到上一组参数的价格，比不显示更糟。
                if (seq !== requestSeq.current) return;
                setEstimate({ loading: false, quote: result.quote, balance: result.wallet.balance, sufficient: result.wallet.sufficient, error: "", unsupported: false });
            } catch (error) {
                if (seq !== requestSeq.current) return;
                if (error instanceof ApiError && error.status === 503) {
                    setEstimate({ ...EMPTY_ESTIMATE, unsupported: true });
                    return;
                }
                const message = error instanceof Error ? error.message : "无法试算本次消耗";
                setEstimate({ loading: false, quote: null, balance: null, sufficient: true, error: message, unsupported: false });
            }
        })();
    }, [debouncedPayload, enabled]);

    return estimate;
}
