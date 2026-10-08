import { expect, test } from "bun:test";

import { CANVAS_SHORTCUT_OVERLAY_SELECTOR, hasCanvasTextSelection, isCanvasShortcutOverlayTarget } from "../src/pages/canvas/use-canvas-keyboard";

test("Canvas copy shortcut yields to a real browser text selection", () => {
    expect(hasCanvasTextSelection(null)).toBe(false);
    expect(hasCanvasTextSelection({ isCollapsed: true, rangeCount: 1, toString: () => "Agent 文本" })).toBe(false);
    expect(hasCanvasTextSelection({ isCollapsed: false, rangeCount: 0, toString: () => "Agent 文本" })).toBe(false);
    expect(hasCanvasTextSelection({ isCollapsed: false, rangeCount: 1, toString: () => "" })).toBe(false);
    expect(hasCanvasTextSelection({ isCollapsed: false, rangeCount: 1, toString: () => "Agent 文本" })).toBe(true);
});

test("Canvas keyboard keeps node copy as the fallback when no text is selected", async () => {
    const source = await Bun.file(new URL("../src/pages/canvas/use-canvas-keyboard.ts", import.meta.url)).text();
    expect(source).toContain("if (hasCanvasTextSelection(window.getSelection())) return;");
    expect(source).toContain("event.preventDefault();\n                copySelectedNodes();");
});

test("Canvas delete prioritizes an explicitly selected connection over a stale node selection", async () => {
    const source = await Bun.file(new URL("../src/pages/canvas/use-canvas-keyboard.ts", import.meta.url)).text();
    expect(source).toContain("if (selectedConnectionId) deleteConnection(selectedConnectionId);");
    expect(source).toContain("else if (selectedNodeIdsRef.current.size) deleteNodes(new Set(selectedNodeIdsRef.current));");
});

test("浮层选择器覆盖 Drawer，而不只是 modal/dropdown/popover", () => {
    for (const selector of [".ant-modal-wrap", ".ant-drawer", ".ant-dropdown", ".ant-popover", ".ant-select-dropdown", ".ant-picker-dropdown"]) {
        expect(CANVAS_SHORTCUT_OVERLAY_SELECTOR).toContain(selector);
    }
});

test("判断浮层目标时用完整浮层选择器查询，且空目标不误判", () => {
    const seen: string[] = [];
    const insideOverlay = { closest: (selector: string) => { seen.push(selector); return {} as Element; } } as unknown as Element;
    expect(isCanvasShortcutOverlayTarget(insideOverlay)).toBe(true);
    expect(seen[0]).toBe(CANVAS_SHORTCUT_OVERLAY_SELECTOR);
    expect(seen[0]).toContain(".ant-drawer");

    const plainCanvasTarget = { closest: () => null } as unknown as Element;
    expect(isCanvasShortcutOverlayTarget(plainCanvasTarget)).toBe(false);
    expect(isCanvasShortcutOverlayTarget(null)).toBe(false);
    expect(isCanvasShortcutOverlayTarget(undefined)).toBe(false);
});

test("Delete 分支在浮层内直接返回，不会删掉画布节点", async () => {
    const source = await Bun.file(new URL("../src/pages/canvas/use-canvas-keyboard.ts", import.meta.url)).text();
    const deleteBranch = source.slice(source.indexOf('if (event.key === "Delete" || event.key === "Backspace")'));
    // 守卫必须出现在 preventDefault 之前，否则抽屉里的按键仍会被画布消费。
    const guardAt = deleteBranch.indexOf("if (isCanvasShortcutOverlayTarget(target)) return;");
    const preventAt = deleteBranch.indexOf("event.preventDefault();");
    expect(guardAt).toBeGreaterThan(-1);
    expect(preventAt).toBeGreaterThan(-1);
    expect(guardAt).toBeLessThan(preventAt);
});
