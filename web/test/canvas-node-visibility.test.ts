import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { isCanvasNodeHiddenFromView } from "@/lib/canvas/canvas-project-domain";
import type { CanvasNodeData } from "@/types/canvas";

const root = resolve(import.meta.dir, "..");

function read(relativePath: string) {
    return readFileSync(resolve(root, relativePath), "utf8");
}

function node(id: string, parentId?: string) {
    return { id, type: "image", position: { x: 0, y: 0 }, width: 200, height: 120, title: id, parentId } as unknown as CanvasNodeData;
}

function frame(id: string, collapsed: boolean) {
    return { id, type: "frame", position: { x: 0, y: 0 }, width: 600, height: 400, title: id, metadata: { frame: { collapsed } } } as unknown as CanvasNodeData;
}

// 展开/收起标志跟着批量表的根节点走，子节点只记得自己属于哪张表。
function batchChild(id: string, rootId: string) {
    return { id, type: "image", position: { x: 0, y: 0 }, width: 200, height: 120, title: id, metadata: { batchRootId: rootId } } as unknown as CanvasNodeData;
}

function batchTable(id: string, expanded: boolean) {
    return { id, type: "batch-table", position: { x: 0, y: 0 }, width: 200, height: 120, title: id, metadata: { imageBatchExpanded: expanded } } as unknown as CanvasNodeData;
}

describe("节点是否真的看不见", () => {
    test("折叠背板里的子节点算看不见，展开的不算", () => {
        const collapsed = [frame("frame-1", true), node("a", "frame-1")];
        expect(isCanvasNodeHiddenFromView(collapsed[1], collapsed)).toBe(true);
        expect(isCanvasNodeHiddenFromView(collapsed[0], collapsed)).toBe(false);

        const expanded = [frame("frame-1", false), node("a", "frame-1")];
        expect(isCanvasNodeHiddenFromView(expanded[1], expanded)).toBe(false);
    });

    test("收进批量表的子节点算看不见，展开的不算", () => {
        const grouped = [batchChild("a", "table-1"), batchTable("table-1", false)];
        expect(isCanvasNodeHiddenFromView(grouped[0], grouped)).toBe(true);
        expect(isCanvasNodeHiddenFromView(grouped[1], grouped)).toBe(false);

        const expanded = [batchChild("a", "table-1"), batchTable("table-1", true)];
        expect(isCanvasNodeHiddenFromView(expanded[0], expanded)).toBe(false);
    });

    test("顶层节点永远算看得见", () => {
        const nodes = [node("a"), node("b")];
        expect(nodes.some((item) => isCanvasNodeHiddenFromView(item, nodes))).toBe(false);
    });

    test("适合屏幕和连线吸附共用这一份判定", () => {
        const viewportController = read("src/pages/canvas/use-canvas-viewport-controller.ts");
        const connectionController = read("src/pages/canvas/use-canvas-connection-controller.ts");

        for (const source of [viewportController, connectionController]) {
            expect(source).toContain("isCanvasNodeHiddenFromView");
            expect(source).not.toContain("isHiddenBatchChild(node, nodesRef.current) && !isNodeHiddenByCollapsedFrame");
        }
    });
});
