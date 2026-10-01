import { describe, expect, test } from "bun:test";

import {
    CANVAS_WHEEL_ZOOM_RATIO,
    canvasWheelZoomFactor,
    clampCanvasScale,
    resolveCanvasWheelIntent,
    type CanvasWheelInput,
} from "../src/lib/canvas/canvas-wheel-zoom";

function wheel(partial: Partial<CanvasWheelInput>): CanvasWheelInput {
    return { deltaX: 0, deltaY: 0, deltaMode: 0, shiftKey: false, ctrlKey: false, metaKey: false, ...partial };
}

describe("滚轮意图", () => {
    test("横向分量或 shift 一律当平移，不再误判成缩放", () => {
        expect(resolveCanvasWheelIntent(wheel({ deltaX: 30, deltaY: 2 }))).toEqual({ kind: "pan", deltaX: 30, deltaY: 2 });
        expect(resolveCanvasWheelIntent(wheel({ deltaY: 40, shiftKey: true }))).toEqual({ kind: "pan", deltaX: 40, deltaY: 0 });
    });

    test("触控板纵向小量仍是平移，不抢走画布滚动", () => {
        expect(resolveCanvasWheelIntent(wheel({ deltaY: 8 })).kind).toBe("pan");
    });

    test("零位移不产生任何意图", () => {
        expect(resolveCanvasWheelIntent(wheel({})).kind).toBe("none");
    });

    test("鼠标整档才是缩放，Ctrl + 整档不会被当成捏合", () => {
        expect(resolveCanvasWheelIntent(wheel({ deltaY: -100 }))).toEqual({ kind: "zoom", notches: -1 });
        expect(resolveCanvasWheelIntent(wheel({ deltaY: -100, ctrlKey: true }))).toEqual({ kind: "zoom", notches: -1 });
    });

    test("触控板捏合走连续缩放，可以只有半档", () => {
        expect(resolveCanvasWheelIntent(wheel({ deltaY: 12, ctrlKey: true }))).toEqual({ kind: "zoom", notches: 0.5 });
    });

    test("行模式按档取整，不受浏览器每行像素差异影响", () => {
        expect(resolveCanvasWheelIntent(wheel({ deltaY: 3, deltaMode: 1 }))).toEqual({ kind: "zoom", notches: 1 });
        expect(resolveCanvasWheelIntent(wheel({ deltaY: 6, deltaMode: 1 }))).toEqual({ kind: "zoom", notches: 2 });
    });
});

describe("档距归一化", () => {
    test("Windows 鼠标的不同档距（100/120/80/53/42）都是同一档、同一倍率", () => {
        const factors = [100, 120, 80, 53, 42, -100, -120, -53].map((deltaY) => {
            const intent = resolveCanvasWheelIntent(wheel({ deltaY }));
            if (intent.kind !== "zoom") throw new Error(`deltaY=${deltaY} 被判成 ${intent.kind}`);
            expect(Math.abs(intent.notches)).toBe(1);
            return Number(canvasWheelZoomFactor(intent.notches).toFixed(6));
        });
        // 正负方向各自只有一个值，且互为倒数
        expect(new Set(factors.filter((value) => value > 1)).size).toBe(1);
        expect(new Set(factors.filter((value) => value < 1)).size).toBe(1);
        expect(factors.filter((value) => value > 1)[0] * factors.filter((value) => value < 1)[0]).toBeCloseTo(1, 6);
    });

    test("一档就是一个固定倍率，放大缩小互为逆运算", () => {
        expect(CANVAS_WHEEL_ZOOM_RATIO).toBeGreaterThan(1);
        expect(canvasWheelZoomFactor(-1)).toBeCloseTo(CANVAS_WHEEL_ZOOM_RATIO, 10);
        expect(canvasWheelZoomFactor(1)).toBeCloseTo(1 / CANVAS_WHEEL_ZOOM_RATIO, 10);
        expect(canvasWheelZoomFactor(-2) * canvasWheelZoomFactor(0)).toBeCloseTo(CANVAS_WHEEL_ZOOM_RATIO ** 2, 10);
    });

    test("平滑滚动一次喷出上千像素也最多缩放三档", () => {
        expect(resolveCanvasWheelIntent(wheel({ deltaY: -1000 }))).toEqual({ kind: "zoom", notches: -3 });
        expect(resolveCanvasWheelIntent(wheel({ deltaY: 1000 }))).toEqual({ kind: "zoom", notches: 3 });
    });

    test("页模式一次算一档", () => {
        expect(resolveCanvasWheelIntent(wheel({ deltaY: -1, deltaMode: 2 }))).toEqual({ kind: "zoom", notches: -1 });
    });

    test("缩放倍率受画布上下限约束", () => {
        expect(clampCanvasScale(0.001)).toBe(0.05);
        expect(clampCanvasScale(99)).toBe(2);
        expect(clampCanvasScale(1.3)).toBe(1.3);
    });
});
