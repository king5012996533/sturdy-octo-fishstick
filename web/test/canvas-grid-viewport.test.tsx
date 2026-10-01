import { describe, expect, test } from "bun:test";
import { createRef } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { InfiniteCanvas } from "../src/components/canvas/infinite-canvas";
import { canvasAppearanceForTheme } from "../src/lib/canvas/canvas-appearance";
import { applyCanvasLiveViewport, CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, CANVAS_VIEWPORT_PREVIEW_EVENT, registerCanvasLiveScaleTarget } from "../src/lib/canvas/canvas-live-viewport";
import { viewportAtScale } from "../src/lib/canvas/canvas-viewport";
import type { CanvasBackgroundMode } from "../src/lib/canvas-theme";
import type { ViewportTransform } from "../src/types/canvas";

const viewports: ViewportTransform[] = [
    { x: 0, y: 0, k: 1 },
    { x: 389.5, y: -207.25, k: 0.05 },
    { x: -17.125, y: 20.875, k: 0.119 },
    { x: -17.125, y: 20.875, k: 0.12 },
    { x: 150.2, y: -40.1, k: 0.666 },
    { x: 150.2, y: -40.1, k: 0.667 },
    { x: -1800.5, y: 3100.75, k: 2 },
];

function renderGrid(mode: CanvasBackgroundMode, viewport: ViewportTransform, theme: "light" | "dark" = "dark") {
    const markup = renderToStaticMarkup(
        <InfiniteCanvas containerRef={createRef<HTMLDivElement>()} viewport={viewport} onViewportChange={() => {}} backgroundMode={mode} appearance={canvasAppearanceForTheme(theme)}>
            <div data-test-node />
        </InfiniteCanvas>,
    );
    return markup.match(/<div data-canvas-grid-layer[^>]*>/)?.[0] ?? null;
}

describe("canvas screen-space background", () => {
    for (const mode of ["lines", "dots"] as const) {
        test(`${mode} keeps fixed geometry across zoom, pan and committed renders`, () => {
            for (const theme of ["light", "dark"] as const) {
                const initial = renderGrid(mode, viewports[0], theme);
                expect(initial).not.toBeNull();
                expect(initial).toContain("background-size:48px 48px");
                expect(initial).toContain("inset:0");
                expect(initial).not.toContain("transform:");
                expect(initial).not.toContain("var(--canvas-");
                if (mode === "dots") expect(initial).toContain("0.8px, transparent 1px");
                for (const viewport of viewports) expect(renderGrid(mode, viewport, theme)).toBe(initial);
            }
        });
    }

    test("blank mode renders no grid at any viewport", () => {
        for (const viewport of viewports) expect(renderGrid("blank", viewport)).toBeNull();
    });

    test("live updates transform the world and emit events without mutating the background", () => {
        const properties = new Map<string, string>([["--canvas-committed-scale", "0.5"]]);
        const gridWrites: string[] = [];
        const world = { style: { transform: "", transformOrigin: "", willChange: "" } };
        const grid = { style: { setProperty: (name: string) => gridWrites.push(name) } };
        const container = Object.assign(new EventTarget(), {
            style: {
                getPropertyValue: (name: string) => properties.get(name) ?? "",
                setProperty: (name: string, value: string) => properties.set(name, value),
            },
            dataset: { canvasViewportInteracting: "true" },
            querySelector: (selector: string) => (selector === "[data-canvas-world-layer]" ? world : grid),
        });
        // 外置节点标题这类逐帧消费者：只有它自己需要实时逆倍率。
        const header = {
            isConnected: true,
            style: { values: new Map<string, string>(), getPropertyValue(name: string) { return this.values.get(name) ?? ""; }, setProperty(name: string, value: string) { this.values.set(name, value); } },
            closest: (selector: string) => (selector === "[data-canvas-viewport]" ? container : null),
        };
        const unregisterHeader = registerCanvasLiveScaleTarget(header as unknown as HTMLElement);
        const graphics: ViewportTransform[] = [];
        const previews: ViewportTransform[] = [];
        let scrolls = 0;
        container.addEventListener(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, (event) => graphics.push((event as CustomEvent<ViewportTransform>).detail));
        container.addEventListener(CANVAS_VIEWPORT_PREVIEW_EVENT, (event) => previews.push((event as CustomEvent<ViewportTransform>).detail));
        container.addEventListener("scroll", () => scrolls++);

        for (const viewport of viewports) {
            applyCanvasLiveViewport(container as unknown as HTMLDivElement, viewport, { notify: false });
            expect(world.style.transform).toBe(`translate3d(${viewport.x}px, ${viewport.y}px, 0) scale(${viewport.k / 0.5})`);
            // 逐帧不再往画布容器写继承型自定义属性：容器是整棵画布子树的祖先，
            // 每帧写一次会让全部节点重新计算样式，Windows 上就是缩放掉帧的来源。
            expect(properties.get("--canvas-live-x")).toBeUndefined();
            expect(properties.get("--canvas-live-y")).toBeUndefined();
            expect(properties.get("--canvas-live-scale")).toBeUndefined();
            // 逆倍率只写在注册过、真正读它的元素（外置节点标题）身上。
            expect(header.style.getPropertyValue("--canvas-live-inverse-scale")).toBe(String(1 / viewport.k));
            expect(properties.get("--canvas-live-inverse-scale")).toBeUndefined();
            expect(gridWrites).toEqual([]);
        }
        expect(graphics).toEqual(viewports);
        expect(previews).toEqual([]);
        expect(scrolls).toBe(0);
        expect(world.style.willChange).toBe("transform");
        unregisterHeader();

        // 提交态才把实时相机写回容器变量，供静止期布局与外部读取。
        applyCanvasLiveViewport(container as unknown as HTMLDivElement, viewports[0], { commit: true, notify: false });
        expect(properties.get("--canvas-live-x")).toBe(String(viewports[0].x));
        expect(properties.get("--canvas-live-y")).toBe(String(viewports[0].y));
        expect(properties.get("--canvas-live-scale")).toBe(String(viewports[0].k));
        expect(properties.get("--canvas-live-inverse-scale")).toBe(String(1 / viewports[0].k));

        container.dataset.canvasViewportInteracting = "false";
        applyCanvasLiveViewport(container as unknown as HTMLDivElement, viewports[0]);
        expect(world.style.willChange).toBe("");
        expect(previews).toEqual([viewports[0]]);
        expect(scrolls).toBe(1);
        expect(gridWrites).toEqual([]);
    });

    test("supports the LibTV 800% precision zoom ceiling", () => {
        const current = { x: 120, y: -80, k: 1 };
        expect(viewportAtScale(current, { width: 1440, height: 900 }, 8).k).toBe(8);
        expect(viewportAtScale(current, { width: 1440, height: 900 }, 12).k).toBe(8);
    });
});
