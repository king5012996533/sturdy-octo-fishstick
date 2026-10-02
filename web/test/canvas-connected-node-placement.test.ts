import { describe, expect, it } from "bun:test";

import { connectedNodeCenterFromEdgeDrop, resolveConnectedNodePlacement } from "@/lib/canvas/canvas-connected-node-placement";
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
