import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const source = readFileSync(new URL("../src/components/canvas/infinite-canvas.tsx", import.meta.url), "utf8");

/**
 * 回归：交互结束后世界层变换必须复位。
 *
 * 缩放过程里虚拟化节流会把同一个视口先提交进 React，交互真正结束时 setViewport 因值相同
 * 而 bail，复位用的 layout effect 不会再跑，世界层就停在"交互期倍率"上——节点层与 Leafer
 * 连线层错开，用户看到连线从节点上崩开（Windows 滚轮缩放时最明显）。
 */
describe("视口交互结束后的复位", () => {
    test("复位 effect 依赖交互结束信号，而不是只依赖视口值", () => {
        expect(source).toContain("const [viewportCommitEpoch, setViewportCommitEpoch] = useState(0);");
        expect(source).toContain("}, [containerRef, viewport, viewportCommitEpoch]);");
        expect(source).toContain("applyCanvasLiveViewport(containerRef.current, viewport, { commit: true });");
    });

    test("滚轮超时、指针抬起、组件失活都走统一的结束入口", () => {
        const ends = source.match(/endViewportInteraction\(\);/g) || [];
        expect(ends.length).toBeGreaterThanOrEqual(4);
        expect(source).toContain("setViewportCommitEpoch((epoch) => epoch + 1);");
    });

    test("滚轮缩放不再依赖固定像素除数和 100 整数倍判定", () => {
        expect(source).not.toContain("WHEEL_ZOOM_DELTA");
        expect(source).not.toContain("looksLikeMouseWheel");
        expect(source).toContain("resolveCanvasWheelIntent(event)");
        expect(source).toContain("canvasWheelZoomFactor(intent.notches)");
    });
});
