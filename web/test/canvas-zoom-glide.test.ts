import { describe, expect, test } from "bun:test";

import { CANVAS_ZOOM_GLIDE_TIME_CONSTANT_MS, glideViewportTowards, isViewportGlideSettled } from "../src/lib/canvas/canvas-zoom-glide";
import type { ViewportTransform } from "../src/types/canvas";

const FRAME_MS = 1000 / 60;

/** 模拟一次滚轮：目标推一格，然后按 60fps 追到静止。 */
function runGlide(from: ViewportTransform, target: ViewportTransform) {
    const frames: ViewportTransform[] = [];
    let current = from;
    for (let i = 0; i < 120; i += 1) {
        current = glideViewportTowards(current, target, FRAME_MS);
        frames.push(current);
        if (isViewportGlideSettled(current, target)) break;
    }
    return frames;
}

describe("滚轮缩放滑行", () => {
    test("首帧不跳：deltaMs 为 0 时视口原样返回", () => {
        const current = { x: 10, y: 20, k: 1 };
        expect(glideViewportTowards(current, { x: 90, y: 20, k: 1.14 }, 0)).toBe(current);
    });

    test("单帧只走一小段，一档缩放在 300ms 内走完", () => {
        const from = { x: 0, y: 0, k: 1 };
        const target = { x: -40, y: 12, k: 1.14 };
        const first = glideViewportTowards(from, target, FRAME_MS);
        // 一个 16.7ms 帧只吃掉 14% 里的一小部分，画面因此是"推"过去而不是"弹"过去
        expect(first.k).toBeGreaterThan(1);
        expect(first.k).toBeLessThan(1.05);
        const frames = runGlide(from, target);
        // 一次滚轮整档缩放（含收尾）约 260ms，超过这个数说明滑行变"粘"了
        expect(frames.length * FRAME_MS).toBeLessThan(300);
        expect(frames.at(-1)!.k).toBeCloseTo(1.14, 2);
    });

    test("单调收敛，不会过冲（相机式推镜不允许来回弹）", () => {
        const frames = runGlide({ x: 0, y: 0, k: 1 }, { x: -40, y: 12, k: 1.14 });
        for (let i = 1; i < frames.length; i += 1) {
            expect(frames[i].k).toBeGreaterThanOrEqual(frames[i - 1].k);
            expect(frames[i].x).toBeLessThanOrEqual(frames[i - 1].x);
        }
        expect(Math.max(...frames.map((frame) => frame.k))).toBeLessThanOrEqual(1.14);
    });

    test("单帧超过 64ms（后台标签页恢复）不会一帧跳到位", () => {
        const next = glideViewportTowards({ x: 0, y: 0, k: 1 }, { x: 0, y: 0, k: 2 }, 5000);
        // 单帧最多按 64ms 计，也就是最多吃掉约 60% 的距离，不会瞬间跳到目标
        expect(next.k).toBeGreaterThan(1.5);
        expect(next.k).toBeLessThan(1.65);
    });

    test("到位判定同时看倍率与像素，避免提前收尾留下最后几像素", () => {
        expect(isViewportGlideSettled({ x: 0, y: 0, k: 1 }, { x: 0, y: 0, k: 1 })).toBe(true);
        // 倍率到位但还差 4px 平移：继续滑行
        expect(isViewportGlideSettled({ x: 4, y: 0, k: 1 }, { x: 0, y: 0, k: 1 })).toBe(false);
        // 平移到位但倍率还差 1%：继续滑行
        expect(isViewportGlideSettled({ x: 0, y: 0, k: 1.01 }, { x: 0, y: 0, k: 1 })).toBe(false);
    });

    test("时间常数是滑行手感唯一的旋钮", () => {
        expect(CANVAS_ZOOM_GLIDE_TIME_CONSTANT_MS).toBeGreaterThan(40);
        expect(CANVAS_ZOOM_GLIDE_TIME_CONSTANT_MS).toBeLessThan(200);
    });
});
