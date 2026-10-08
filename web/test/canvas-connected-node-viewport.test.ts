import { describe, expect, it } from "bun:test";

import { resolveCanvasMinimalPan } from "@/lib/canvas/canvas-connected-node-viewport";

const safe = { left: 24, top: 64, right: 1576, bottom: 928 };

describe("resolveCanvasMinimalPan", () => {
    it("目标已在安全区内时不动视口", () => {
        expect(resolveCanvasMinimalPan({ left: 100, top: 200, right: 400, bottom: 600 }, safe)).toBeNull();
    });

    it("完全包含安全区时也不动视口（目标比视口还大，挪也露不全）", () => {
        expect(resolveCanvasMinimalPan({ left: -500, top: -500, right: 5000, bottom: 5000 }, safe)).toBeNull();
    });

    it("只在一个轴上比视口大时，另一个轴仍然正常平移", () => {
        // 横向盖满（不平移），纵向越出下边界（平移）
        expect(resolveCanvasMinimalPan({ left: -500, top: 800, right: 5000, bottom: 1000 }, safe)).toEqual({ x: 0, y: -72 });
    });

    it("越出右边界时只平移溢出的那部分", () => {
        expect(resolveCanvasMinimalPan({ left: 1400, top: 100, right: 1700, bottom: 400 }, safe)).toEqual({ x: -124, y: 0 });
    });

    it("越出左边界时向右平移", () => {
        expect(resolveCanvasMinimalPan({ left: -10, top: 100, right: 300, bottom: 400 }, safe)).toEqual({ x: 34, y: 0 });
    });

    it("越出下边界时向上平移", () => {
        expect(resolveCanvasMinimalPan({ left: 100, top: 800, right: 400, bottom: 1000 }, safe)).toEqual({ x: 0, y: -72 });
    });

    it("越出上边界时向下平移", () => {
        expect(resolveCanvasMinimalPan({ left: 100, top: 10, right: 400, bottom: 300 }, safe)).toEqual({ x: 0, y: 54 });
    });

    it("单个轴向溢出时另一轴保持 0，避免无谓抖动", () => {
        const pan = resolveCanvasMinimalPan({ left: 100, top: 100, right: 5000, bottom: 400 }, safe);
        expect(pan).toEqual({ x: -3424, y: 0 });
    });

    it("安全区退化时不动视口", () => {
        expect(resolveCanvasMinimalPan({ left: 0, top: 0, right: 10, bottom: 10 }, { left: 100, top: 100, right: 100, bottom: 100 })).toBeNull();
        expect(resolveCanvasMinimalPan({ left: 0, top: 0, right: 10, bottom: 10 }, { left: 100, top: 100, right: 50, bottom: 50 })).toBeNull();
    });
});
