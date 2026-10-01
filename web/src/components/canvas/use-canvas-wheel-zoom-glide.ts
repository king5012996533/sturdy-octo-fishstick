import { useCallback, useEffect, useRef } from "react";

import { glideViewportTowards, isViewportGlideSettled } from "@/lib/canvas/canvas-zoom-glide";
import type { ViewportTransform } from "@/types/canvas";

type UseCanvasWheelZoomGlideOptions = {
    /** 逐帧写视口。第二个参数恒为 false：滑行期间不排 120ms 空闲提交，由滑行自己收尾。 */
    applyViewport: (viewport: ViewportTransform, commitAfterIdle: boolean) => void;
    /** 读当前实时视口（滑行起点，也是每帧的输入）。 */
    readViewport: () => ViewportTransform;
    /** 滑行结束：清交互标记、把最终视口提交给 React。 */
    finish: () => void;
};

/**
 * 滚轮整档缩放的滑行驱动。
 *
 * 目标在半路变化时不重启动画，只把目标往前推（指数趋近速度连续），所以快速连滚是"一路
 * 推过去"而不是一格一格重新开始。触控板/捏合这类连续输入不该走这里——它们本来就是连续量，
 * 再套一层滑行只会变钝，调用方用 animated:false 直接落位。
 */
export function useCanvasWheelZoomGlide({ applyViewport, readViewport, finish }: UseCanvasWheelZoomGlideOptions) {
    const targetRef = useRef<ViewportTransform | null>(null);
    const frameRef = useRef<number | null>(null);
    const lastFrameAtRef = useRef(0);

    const cancel = useCallback(() => {
        if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
        frameRef.current = null;
        targetRef.current = null;
    }, []);

    useEffect(() => cancel, [cancel]);

    const glideTo = useCallback(
        (target: ViewportTransform, options: { animated: boolean }) => {
            if (!options.animated) {
                cancel();
                // 连续输入（捏合 / 系统要求减少动态效果）保持改动前的行为：
                // 直接落位，仍然等 120ms 空闲再收尾——每帧都收尾会让合成层反复摘挂。
                applyViewport(target, true);
                return;
            }
            targetRef.current = target;
            // 已经在滑行：只更新目标，继续用同一条曲线追，避免每格重启造成速度断点。
            if (frameRef.current !== null) return;
            lastFrameAtRef.current = performance.now();
            const step = (now: number) => {
                const pending = targetRef.current;
                if (!pending) {
                    frameRef.current = null;
                    return;
                }
                const deltaMs = now - lastFrameAtRef.current;
                lastFrameAtRef.current = now;
                const next = glideViewportTowards(readViewport(), pending, deltaMs);
                if (isViewportGlideSettled(next, pending)) {
                    frameRef.current = null;
                    targetRef.current = null;
                    applyViewport(pending, false);
                    finish();
                    return;
                }
                applyViewport(next, false);
                frameRef.current = requestAnimationFrame(step);
            };
            frameRef.current = requestAnimationFrame(step);
        },
        [applyViewport, cancel, finish, readViewport],
    );

    /** 滑行中的目标视口；连续滚动时按它累积，否则慢一拍的画面会吃掉后面的档位。 */
    const glideTarget = useCallback(() => targetRef.current, []);

    return { glideTo, glideTarget, cancelGlide: cancel };
}
