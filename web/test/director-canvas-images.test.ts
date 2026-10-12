import { describe, expect, test } from "bun:test";

import { buildDirectorCanvasImageRows, directorBillboardSourceNodeIds } from "@/lib/canvas/director/director-canvas-images";
import { createCanvasNode } from "@/lib/canvas/canvas-project-domain";
import { createDirectorBillboard } from "@/lib/canvas/director/director-scene";
import { CanvasNodeType } from "@/types/canvas";

function imageNode(id: string, title: string) {
    const node = createCanvasNode(CanvasNodeType.Image, { x: 0, y: 0 }, { content: "blob:test", storageKey: `resource:${id}` });
    return { ...node, id, title };
}

describe("导演台画布图片列表", () => {
    test("没有连线也没有立牌时保持画布原始顺序", () => {
        const rows = buildDirectorCanvasImageRows([imageNode("a", "图 A"), imageNode("b", "图 B"), imageNode("c", "图 C")], [], []);
        expect(rows.map((row) => row.node.id)).toEqual(["a", "b", "c"]);
        expect(rows.every((row) => !row.connected && !row.inScene)).toBe(true);
    });

    test("已连线的排最前，其次是在场景里放了立牌的，剩下保持原始顺序", () => {
        const nodes = [imageNode("a", "图 A"), imageNode("b", "图 B"), imageNode("c", "图 C"), imageNode("d", "图 D")];
        const rows = buildDirectorCanvasImageRows(nodes, ["c", "d"], ["b"]);
        expect(rows.map((row) => row.node.id)).toEqual(["c", "d", "b", "a"]);
        expect(rows.map((row) => (row.connected ? "已连线" : row.inScene ? "场中" : "普通"))).toEqual(["已连线", "已连线", "场中", "普通"]);
    });

    test("同一优先级内不因为排序而打乱画布顺序", () => {
        const nodes = [imageNode("a", "图 A"), imageNode("b", "图 B"), imageNode("c", "图 C")];
        const rows = buildDirectorCanvasImageRows(nodes, [], ["a", "b", "c"]);
        expect(rows.map((row) => row.node.id)).toEqual(["a", "b", "c"]);
    });

    test("既连线又立牌时只按连线归类，不会重复出现", () => {
        const rows = buildDirectorCanvasImageRows([imageNode("a", "图 A"), imageNode("b", "图 B")], ["a"], ["a", "b"]);
        expect(rows.map((row) => row.node.id)).toEqual(["a", "b"]);
        expect(rows[0]).toMatchObject({ connected: true, inScene: true });
        expect(rows[1]).toMatchObject({ connected: false, inScene: true });
    });

    test("连线里出现画布上不存在的节点不会凭空造行", () => {
        const rows = buildDirectorCanvasImageRows([imageNode("a", "图 A")], ["ghost"], []);
        expect(rows.map((row) => row.node.id)).toEqual(["a"]);
    });

    test("只把引用了画布节点的立牌算作场中", () => {
        const billboard = createDirectorBillboard("画布图片", "blob:test", "resource:a", "a");
        const actorOnly = createDirectorBillboard("本地上传的参考", "blob:other", "resource:x");
        expect(directorBillboardSourceNodeIds([billboard, actorOnly])).toEqual(["a"]);
    });
});
