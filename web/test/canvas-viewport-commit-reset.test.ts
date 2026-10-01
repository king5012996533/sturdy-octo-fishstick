import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const source = readFileSync(new URL("../src/components/canvas/infinite-canvas.tsx", import.meta.url), "utf8");

/**
 * 回归：世界层补偿倍率与"已提交倍率"必须始终一致。
 *
 * 世界层写的是 scale(实时倍率 / 已提交倍率)，光栅层写的是 scale(已提交倍率)。
 *
 * 1) 缩放过程里虚拟化节流会把同一个视口先提交进 React，交互真正结束时 setViewport 因值相同
 *    而 bail，复位用的 layout effect 不会再跑，世界层就停在"交互期倍率"上——节点层与 Leafer
 *    连线层错开，用户看到连线从节点上崩开。
 * 2) 反过来，React 提交改变了光栅层倍率时，如果补偿还是上一次的值，两者相乘就会渲染成
 *    实时倍率 × 1.14（实测 +14%，持续约 115ms，下一次滚轮写入才纠正）。Windows 鼠标滚轮
 *    整档缩放每格都会踩到，用户感受就是"一卡一卡"；触控板与面板是连续小量，比值≈1.00x。
 */
describe("视口提交后的世界层补偿", () => {
    test("复位 effect 依赖交互结束信号，而不是只依赖视口值", () => {
        expect(source).toContain("const [viewportCommitEpoch, setViewportCommitEpoch] = useState(0);");
        expect(source).toContain("}, [containerRef, viewport, viewportCommitEpoch]);");
        expect(source).toContain("applyCanvasLiveViewport(container, viewport, { commit: true });");
    });

    test("交互期提交也要按实时视口重算补偿，不能直接跳过 effect", () => {
        expect(source).not.toContain("if (interactingRef.current) return;");
        expect(source).toContain("applyCanvasLiveViewport(container, viewportRef.current, { silent: true });");
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
