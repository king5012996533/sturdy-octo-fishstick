import { describe, expect, test } from "bun:test";

import { CANVAS_NODE_SCALE_STOPS, canvasNodeScaleBucket } from "@/lib/canvas/canvas-node-scale-bucket";

describe("canvasNodeScaleBucket", () => {
    test("阈值语义必须与原始倍率完全一致", () => {
        // 节点内部用 0.35 / 0.5 决定隐藏外置标题与分镜子标题，分档后判定必须不变。
        for (const threshold of [0.35, 0.5]) {
            for (let scale = 0.05; scale <= 5; scale += 0.001) {
                const rounded = Math.round(scale * 1000) / 1000;
                expect(canvasNodeScaleBucket(rounded) < threshold).toBe(rounded < threshold);
            }
        }
    });

    test("档位本身落在原值上", () => {
        for (const stop of CANVAS_NODE_SCALE_STOPS) expect(canvasNodeScaleBucket(stop)).toBe(stop);
    });

    test("取不超过原值的最大档位", () => {
        expect(canvasNodeScaleBucket(0.4)).toBe(0.35);
        expect(canvasNodeScaleBucket(0.49)).toBe(0.42);
        expect(canvasNodeScaleBucket(1.1)).toBe(1);
        expect(canvasNodeScaleBucket(3.9)).toBe(3.4);
    });

    test("低于最小档位时收敛到最小档，避免极小倍率下每拍换值", () => {
        expect(canvasNodeScaleBucket(0.01)).toBe(CANVAS_NODE_SCALE_STOPS[0]);
        expect(canvasNodeScaleBucket(0)).toBe(1);
    });

    test("非法输入回落到 1", () => {
        expect(canvasNodeScaleBucket(Number.NaN)).toBe(1);
        expect(canvasNodeScaleBucket(Number.POSITIVE_INFINITY)).toBe(1);
        expect(canvasNodeScaleBucket(Number.NEGATIVE_INFINITY)).toBe(1);
        expect(canvasNodeScaleBucket(-2)).toBe(1);
    });

    test("连续缩放的取值数量被显著压缩", () => {
        const raw = new Set<number>();
        const bucketed = new Set<number>();
        for (let scale = 0.1; scale <= 4; scale += 0.002) {
            const rounded = Math.round(scale * 1000) / 1000;
            raw.add(rounded);
            bucketed.add(canvasNodeScaleBucket(rounded));
        }
        expect(raw.size).toBeGreaterThan(1000);
        expect(bucketed.size).toBeLessThanOrEqual(CANVAS_NODE_SCALE_STOPS.length);
    });
});
