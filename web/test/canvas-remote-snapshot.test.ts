import { describe, expect, test } from "bun:test";

import { canApplyRemoteCanvasSnapshot } from "@/lib/canvas/canvas-remote-snapshot";

const idle = { nodeDragging: false, viewportInteracting: false, hasPendingLocalWrite: false };

describe("后台画布快照回写准入", () => {
    test("编辑器空闲且本地无未落盘写入时允许套用", () => {
        expect(canApplyRemoteCanvasSnapshot(idle)).toBe(true);
    });

    test("拖拽节点期间不允许回写，否则卡片会被拉回旧位置", () => {
        expect(canApplyRemoteCanvasSnapshot({ ...idle, nodeDragging: true })).toBe(false);
    });

    test("视口手势进行中不允许回写，否则正在缩放平移的画布会跳", () => {
        expect(canApplyRemoteCanvasSnapshot({ ...idle, viewportInteracting: true })).toBe(false);
    });

    test("本地还有未落盘编辑时不允许回写，否则刚改的尺寸会被云端旧值覆盖", () => {
        expect(canApplyRemoteCanvasSnapshot({ ...idle, hasPendingLocalWrite: true })).toBe(false);
    });

    test("多个条件同时成立时依然拦下", () => {
        expect(canApplyRemoteCanvasSnapshot({ nodeDragging: true, viewportInteracting: true, hasPendingLocalWrite: true })).toBe(false);
    });
});
