import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const canvas = readFileSync(new URL("../src/components/canvas/infinite-canvas.tsx", import.meta.url), "utf8");
const glideHook = readFileSync(new URL("../src/components/canvas/use-canvas-wheel-zoom-glide.ts", import.meta.url), "utf8");
const controller = readFileSync(new URL("../src/pages/canvas/use-canvas-viewport-controller.ts", import.meta.url), "utf8");
const page = readFileSync(new URL("../src/pages/canvas/project.tsx", import.meta.url), "utf8");

/**
 * 回归：滚轮整档缩放必须滑行，而且滑行只能有一个写入方。
 *
 * 整档跳变直接写视口就是"一格一格弹"；但滑行期间如果有别的指令（面板按钮、小地图、
 * 过渡动画、指针拖拽）也在写视口，两边会互相覆盖——用户看到的是点了放大又被拉回去。
 */
describe("滚轮滑行接线", () => {
    test("整档缩放交给滑行，捏合仍然直接跟手", () => {
        expect(canvas).toContain("glideTo({");
        expect(canvas).toContain('intent.source === "notch" && canvasZoomGlideEnabled()');
        expect(canvas).toContain("const base = glideTarget() ?? current;");
    });

    test("指针落到画布上立刻停掉滑行", () => {
        const pointerDown = canvas.slice(canvas.indexOf("const handlePointerDown"));
        expect(pointerDown.slice(0, 400)).toContain("cancelGlide();");
    });

    test("滚轮平移先停滑行，避免两边同时写视口", () => {
        const panBranch = canvas.slice(canvas.indexOf('if (intent.kind === "pan")'));
        expect(panBranch.slice(0, 200)).toContain("cancelGlide();");
    });

    test("捏合这类连续输入不收尾，保留 120ms 空闲提交（否则合成层每帧摘挂）", () => {
        expect(glideHook).toContain("applyViewport(target, true);");
        expect(glideHook).not.toContain("applyViewport(target, false);");
    });

    test("画布把停滑行交给页面控制器，外部视口指令先停滑行再写", () => {
        expect(canvas).toContain("registerViewportGlideCancel(cancelGlide);");
        expect(page).toContain("registerViewportGlideCancel={registerViewportGlideCancel}");
        expect(controller).toContain("const registerViewportGlideCancel = useCallback");
        const preview = controller.slice(controller.indexOf("const previewViewport = useCallback"));
        expect(preview.slice(0, 300)).toContain("glideCancelRef.current?.();");
        const commit = controller.slice(controller.indexOf("const commitViewport = useCallback"));
        expect(commit.slice(0, 300)).toContain("glideCancelRef.current?.();");
    });
});
