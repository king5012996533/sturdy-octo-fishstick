import { describe, expect, test } from "bun:test";

import { canvasLiveScaleValue } from "@/lib/canvas/canvas-live-viewport";

// 分档倍率（canvas-node-scale-bucket）只够做可见性判定；手柄尺寸与偏移这些需要真实倍率的
// 地方都读 --canvas-live-* 变量。这里锁定"读到什么算什么、读不到就回退"的语义：一旦解析
// 规则放松，分档值就会悄悄回到这些尺寸数学里。
describe("canvasLiveScaleValue", () => {
    test("解析正常的倍率字符串", () => {
        expect(canvasLiveScaleValue("1", 9)).toBe(1);
        expect(canvasLiveScaleValue("0.35", 9)).toBe(0.35);
        expect(canvasLiveScaleValue(" 2.5 ", 9)).toBe(2.5);
    });

    test("缺失、空串、非法值一律回退", () => {
        expect(canvasLiveScaleValue(undefined, 9)).toBe(9);
        expect(canvasLiveScaleValue(null, 9)).toBe(9);
        expect(canvasLiveScaleValue("", 9)).toBe(9);
        expect(canvasLiveScaleValue("calc(1/2)", 9)).toBe(9);
        expect(canvasLiveScaleValue("auto", 9)).toBe(9);
        expect(canvasLiveScaleValue("NaN", 9)).toBe(9);
    });

    test("非正数一律回退，避免把 0 倍率当成真实值算出手柄尺寸 0", () => {
        expect(canvasLiveScaleValue("0", 9)).toBe(9);
        expect(canvasLiveScaleValue("-2", 9)).toBe(9);
    });
});
