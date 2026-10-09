import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { CONNECTION_REPLACE_HOVER_PROBE_INTERVAL_MS, shouldProbeConnectionReplaceHover } from "@/pages/canvas/use-canvas-connection-controller";

const source = readFileSync(resolve(import.meta.dir, "../src/pages/canvas/use-canvas-connection-controller.ts"), "utf8");

describe("连线悬停命中探测的节流", () => {
    test("首次探测不被节流（时间戳为 0）", () => {
        expect(shouldProbeConnectionReplaceHover(0, 1000)).toBe(true);
    });

    test("间隔未到时不重复探测", () => {
        expect(shouldProbeConnectionReplaceHover(1000, 1001)).toBe(false);
        expect(shouldProbeConnectionReplaceHover(1000, 1000 + CONNECTION_REPLACE_HOVER_PROBE_INTERVAL_MS - 1)).toBe(false);
    });

    test("间隔到达后恢复探测", () => {
        expect(shouldProbeConnectionReplaceHover(1000, 1000 + CONNECTION_REPLACE_HOVER_PROBE_INTERVAL_MS)).toBe(true);
        expect(shouldProbeConnectionReplaceHover(1000, 5000)).toBe(true);
    });

    test("节流间隔必须明显长于一帧，否则等于没节流", () => {
        expect(CONNECTION_REPLACE_HOVER_PROBE_INTERVAL_MS).toBeGreaterThanOrEqual(32);
    });
});

describe("elementFromPoint 不再是每帧开销", () => {
    test("探测调用被节流守卫包住", () => {
        const guarded = source.slice(source.indexOf("shouldProbeConnectionReplaceHover(replaceHoverProbeAtRef.current, now)"));
        const probeAt = guarded.indexOf("document.elementFromPoint");
        const nextGuard = guarded.indexOf("const dropTarget");
        // elementFromPoint 必须出现在节流分支之内、并且在同帧后续逻辑之前
        expect(probeAt).toBeGreaterThan(-1);
        expect(nextGuard).toBeGreaterThan(probeAt);
    });

    test("页面上没有参考芯片时直接跳过探测", () => {
        expect(source).toContain('if (!document.querySelector("[data-reference-chip]"))');
    });

    test("每轮拖拽开始会重置时间戳，避免第一帧被上一轮节流掉", () => {
        expect(source).toContain("replaceHoverProbeAtRef.current = 0;");
    });
});
