import { describe, expect, it } from "bun:test";

import { connectedNodeCenterFromEdgeDrop, resolveConnectedNodeCreatePosition, resolveConnectedNodePlacement } from "@/lib/canvas/canvas-connected-node-placement";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

describe("connectedNodeCenterFromEdgeDrop", () => {
    it("uses an output drop as the new target node's left-edge midpoint", () => {
        expect(connectedNodeCenterFromEdgeDrop({ x: 900, y: 320 }, { width: 720, height: 405 }, "source")).toEqual({ x: 1260, y: 320 });
    });

    it("uses an input drop as the new source node's right-edge midpoint", () => {
        expect(connectedNodeCenterFromEdgeDrop({ x: 900, y: 320 }, { width: 340, height: 120 }, "target")).toEqual({ x: 730, y: 320 });
    });
});

function node(overrides: Partial<CanvasNodeData> & { id: string }): CanvasNodeData {
    return { type: CanvasNodeType.Text, title: overrides.id, position: { x: 0, y: 0 }, width: 350, height: 350, ...overrides } as CanvasNodeData;
}

describe("resolveConnectedNodePlacement", () => {
    it("anchors the new node to the source node's right side", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const placement = resolveConnectedNodePlacement(
            [source],
            { connection: { nodeId: "text-1", handleType: "source" }, position: { x: 0, y: 0 } },
            { width: 640, height: 640 },
        );
        expect(placement).toEqual({ x: 766, y: 175 });
    });

    it("stacks a second output below the first instead of overlapping it", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const existing = node({ id: "director-1", type: CanvasNodeType.Director, position: { x: 446, y: -145 }, width: 640, height: 640 });
        const placement = resolveConnectedNodePlacement(
            [source, existing],
            { connection: { nodeId: "text-1", handleType: "source" }, position: { x: 0, y: 0 } },
            { width: 640, height: 640 },
        );
        // 已有节点底边 495，留 36 的间距后新节点顶部落在 531，中心即 531 + 640/2。
        expect(placement).toEqual({ x: 766, y: 851 });
    });

    it("falls back to the pointer drop when the source node is already gone", () => {
        const placement = resolveConnectedNodePlacement(
            [],
            { connection: { nodeId: "missing", handleType: "source" }, position: { x: 500, y: 500 } },
            { width: 200, height: 100 },
        );
        expect(placement).toEqual({ x: 600, y: 500 });
    });
});

describe("resolveConnectedNodeCreatePosition", () => {
    const size = { width: 640, height: 640 };

    it("拖拽到空白处释放时用落点，不再吸附到源节点右侧", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const dropped = { x: 1800, y: -400 };
        expect(resolveConnectedNodeCreatePosition([source], { connection: { nodeId: "text-1", handleType: "source" }, position: dropped }, size)).toEqual(dropped);
        // 与锚定摆放确实不同，否则这条测试没有意义。
        expect(resolveConnectedNodePlacement([source], { connection: { nodeId: "text-1", handleType: "source" }, position: dropped }, size)).not.toEqual(dropped);
    });

    it("点击快捷连接点（quick，无位移）时锚到源节点同侧，避免压在源卡片上", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const pending = { connection: { nodeId: "text-1" as const, handleType: "source" as const }, position: { x: 350, y: 175 }, quick: true };
        expect(resolveConnectedNodeCreatePosition([source], pending, size)).toEqual({ x: 766, y: 175 });
    });

    it("批量连接始终按落点，即使带 quick 标记", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const dropped = { x: 1200, y: 300 };
        expect(resolveConnectedNodeCreatePosition([source], { connection: { nodeId: "text-1", handleType: "source" }, position: dropped, quick: true, batchSourceNodeIds: ["text-1"] }, size)).toEqual(dropped);
    });

    it("只压住一点点时几乎不挪，仍贴近鼠标落点", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const small = { width: 200, height: 100 };
        // 落点让卡片左边缘落在 376，只比源卡片右边缘 + 间距（386）差 10
        const placement = resolveConnectedNodeCreatePosition([source], { connection: { nodeId: "text-1", handleType: "source" }, position: { x: 476, y: 175 } }, small);
        expect(placement).toEqual({ x: 486, y: 175 });
        // 只挪了 10，而不是整张卡片的高度
        expect(Math.abs(placement.x - 476)).toBe(10);
        expect(placement.y).toBe(175);
    });

    it("落点压在源卡片正中时，按拖拽方向让到源卡片外侧而不是掉到下方", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const placement = resolveConnectedNodeCreatePosition([source], { connection: { nodeId: "text-1", handleType: "source" }, position: { x: 175, y: 175 } }, size);
        const left = placement.x - size.width / 2;
        const top = placement.y - size.height / 2;
        expect(overlaps(source, { left, top }, size)).toBe(false);
        // 向右让开（source 输出侧），纵向保持在落点上
        expect(left).toBe(source.position.x + source.width + 36);
        expect(top).toBe(-145);
    });

    it("从输入侧拉出来时优先向左让开", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const placement = resolveConnectedNodeCreatePosition([source], { connection: { nodeId: "text-1", handleType: "target" }, position: { x: 175, y: 175 } }, size);
        const left = placement.x - size.width / 2;
        expect(left + size.width + 36).toBe(source.position.x);
    });

    it("落点压在别的卡片上也会让开", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const blocker = node({ id: "image-1", position: { x: 900, y: 600 }, width: 640, height: 640 });
        const placement = resolveConnectedNodeCreatePosition([source, blocker], { connection: { nodeId: "text-1", handleType: "source" }, position: { x: 1220, y: 920 } }, size);
        const left = placement.x - size.width / 2;
        const top = placement.y - size.height / 2;
        expect(overlaps(blocker, { left, top }, size)).toBe(false);
        expect(overlaps(source, { left, top }, size)).toBe(false);
    });

    it("落点空着时严格落在松手的位置", () => {
        const source = node({ id: "text-1", position: { x: 0, y: 0 }, width: 350, height: 350 });
        const dropped = { x: -900, y: 1400 };
        expect(resolveConnectedNodeCreatePosition([source], { connection: { nodeId: "text-1", handleType: "source" }, position: dropped }, size)).toEqual(dropped);
    });
});

function overlaps(placed: CanvasNodeData, candidate: { left: number; top: number }, size: { width: number; height: number }) {
    const gap = 36;
    const overlapsX = candidate.left < placed.position.x + placed.width + gap && candidate.left + size.width + gap > placed.position.x;
    const overlapsY = candidate.top < placed.position.y + placed.height + gap && candidate.top + size.height + gap > placed.position.y;
    return overlapsX && overlapsY;
}
