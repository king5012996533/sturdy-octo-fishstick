import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
// Use the explicit Node renderer so Bun cannot select the browser server
// build when another parallel test has installed partial DOM globals.
import { renderToStaticMarkup } from "react-dom/server.node";

import { CanvasEmptyViewportHintPill } from "@/components/canvas/canvas-empty-viewport-hint";
import { viewportContainsNodes } from "@/lib/canvas/canvas-viewport";
import type { CanvasNodeData } from "@/types/canvas";

const root = resolve(import.meta.dir, "..");
const viewportSize = { width: 1000, height: 700 };

function node(id: string, x: number, y: number, width = 200, height = 120, parentId?: string) {
    return { id, type: "image", position: { x, y }, width, height, title: id, parentId } as unknown as CanvasNodeData;
}

describe("视窗空节点判定", () => {
    test("节点落在视窗内时不算空", () => {
        const nodes = [node("a", 0, 0)];
        expect(viewportContainsNodes(nodes, { x: 0, y: 0, k: 1 }, viewportSize)).toBe(true);
    });

    test("缩放后按屏幕坐标判断交集", () => {
        const nodes = [node("a", 0, 0)];
        // 50% 下节点占 100×60，视窗仍在节点附近
        expect(viewportContainsNodes(nodes, { x: 0, y: 0, k: 0.5 }, viewportSize)).toBe(true);
        // 平移到很远的地方就看不见了
        expect(viewportContainsNodes(nodes, { x: -4000, y: -3000, k: 0.5 }, viewportSize)).toBe(false);
    });

    test("部分露出仍然算看得见", () => {
        const nodes = [node("a", 0, 0)];
        expect(viewportContainsNodes(nodes, { x: -190, y: -110, k: 1 }, viewportSize)).toBe(true);
    });

    test("完全滑出视窗时报空", () => {
        const nodes = [node("a", 0, 0)];
        expect(viewportContainsNodes(nodes, { x: -201, y: 0, k: 1 }, viewportSize)).toBe(false);
        expect(viewportContainsNodes(nodes, { x: 0, y: -121, k: 1 }, viewportSize)).toBe(false);
    });

    test("没有节点本身不算空视窗（交给调用方决定是否提示）", () => {
        expect(viewportContainsNodes([], { x: 0, y: 0, k: 1 }, viewportSize)).toBe(false);
    });

    test("被折叠背板隐藏的节点不计入可见", () => {
        const nodes = [node("a", 0, 0, 200, 120, "frame-1")];
        expect(viewportContainsNodes(nodes, { x: 0, y: 0, k: 1 }, viewportSize, { isHidden: () => true })).toBe(false);
        expect(viewportContainsNodes(nodes, { x: 0, y: 0, k: 1 }, viewportSize)).toBe(true);
    });

    test("视野尺寸未知时不做误报", () => {
        expect(viewportContainsNodes([node("a", 0, 0)], { x: 0, y: 0, k: 1 }, { width: 0, height: 0 })).toBe(true);
    });
});

describe("画布空视窗提示接线", () => {
    test("提示本体渲染文案和返回入口，关闭入口是可选的", () => {
        const withDismiss = renderToStaticMarkup(<CanvasEmptyViewportHintPill onReturnToNodes={() => {}} onDismiss={() => {}} />);
        expect(withDismiss).toContain("当前视窗没有节点");
        expect(withDismiss).toContain("返回节点");
        expect(withDismiss).toContain("关闭提示");

        const withoutDismiss = renderToStaticMarkup(<CanvasEmptyViewportHintPill onReturnToNodes={() => {}} />);
        expect(withoutDismiss).toContain("返回节点");
        expect(withoutDismiss).not.toContain("关闭提示");
    });

    test("动效外壳只在可见时挂载提示本体", () => {
        const hint = readFileSync(resolve(root, "src/components/canvas/canvas-empty-viewport-hint.tsx"), "utf8");
        expect(hint).toContain("visible ? (");
        expect(hint).toContain("data-canvas-empty-viewport-hint");
        expect(hint).toContain("<CanvasEmptyViewportHintPill");
    });

    test("提示文案与返回入口稳定", () => {
        const hint = readFileSync(resolve(root, "src/components/canvas/canvas-empty-viewport-hint.tsx"), "utf8");
        expect(hint).toContain("当前视窗没有节点");
        expect(hint).toContain("返回节点");
        expect(hint).toContain("data-canvas-empty-viewport-hint");
        expect(hint).toContain("关闭提示");
    });

    test("画布页在非专注模式下挂上提示，并用适合屏幕回到内容", () => {
        const project = readFileSync(resolve(root, "src/pages/canvas/project.tsx"), "utf8");
        expect(project).toContain("useCanvasViewportEmptiness(nodes, viewport, size)");
        expect(project).toContain("onReturnToNodes={fitCanvasContent}");
        expect(project).toContain("visible={viewportEmpty && !emptyViewportHintDismissed}");
        expect(project).toContain("if (!viewportEmpty && emptyViewportHintDismissed) setEmptyViewportHintDismissed(false);");
    });

    test("空视窗判定复用统一的“看不见”口径", () => {
        const hook = readFileSync(resolve(root, "src/pages/canvas/use-canvas-viewport-emptiness.ts"), "utf8");
        expect(hook).toContain("isCanvasNodeHiddenFromView(node, nodes)");
        expect(hook).not.toContain("isNodeHiddenByCollapsedFrame");
    });
});
