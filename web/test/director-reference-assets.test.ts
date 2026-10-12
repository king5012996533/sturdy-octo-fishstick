import { describe, expect, test } from "bun:test";

import { connectDirectorReferenceNodes, createDirectorScreenshotCanvasNode, directorScreenshotNodeMetadata } from "@/lib/canvas/director/director-reference-assets";
import { createCanvasNode } from "@/lib/canvas/canvas-project-domain";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function image(id: string, title: string): CanvasNodeData {
    return { ...createCanvasNode(CanvasNodeType.Image, { x: 0, y: 0 }), id, title, metadata: { content: `/assets/${id}.png` } };
}

describe("导演台参考图片接入画布资源图", () => {
    test("只连接有效图片、去重，并把图片 id 合并到导演节点引用", () => {
        const target = { ...createCanvasNode(CanvasNodeType.Video, { x: 0, y: 0 }, { workflowKind: "shot", composerContent: "夜晚的街道" }), id: "director", metadata: { workflowKind: "shot", composerContent: "夜晚的街道", referenceAssetNodeIds: ["old-image"] } } as CanvasNodeData;
        const first = image("image-1", "参考图 1");
        const second = image("image-2", "参考图 2");
        const nonImage = { ...first, id: "video-1", type: CanvasNodeType.Video };

        const result = connectDirectorReferenceNodes([target, first, second, nonImage], [], ["image-1", "image-1", "image-2", "video-1"], "director", () => "edge");

        expect(result.connections.map(({ fromNodeId, toNodeId }) => [fromNodeId, toNodeId])).toEqual([["image-1", "director"], ["image-2", "director"]]);
        expect(result.linkedSourceIds).toEqual(["image-1", "image-2"]);
        expect(result.nodes[0].metadata?.referenceAssetNodeIds).toEqual(["old-image", "image-1", "image-2"]);
        expect(result.nodes[0].metadata?.composerContent).toBe("夜晚的街道");
    });

    test("重复操作不重复建边，非导演节点和缺失资源不改变数据", () => {
        const target = { ...createCanvasNode(CanvasNodeType.Video, { x: 0, y: 0 }, { workflowKind: "shot" }), id: "director" };
        const source = image("image-1", "参考图");
        const edge = { id: "existing", fromNodeId: source.id, toNodeId: target.id };
        const input = [target, source];

        const repeated = connectDirectorReferenceNodes(input, [edge], [source.id], target.id, () => "new-edge");
        const orphan = connectDirectorReferenceNodes(input, [], ["missing"], target.id, () => "new-edge");
        const ordinaryNode = { ...target, metadata: { workflowKind: "free" } };
        const rejected = connectDirectorReferenceNodes([ordinaryNode, source], [], [source.id], target.id, () => "new-edge");

        expect(repeated.connections).toEqual([edge]);
        expect(orphan.connections).toEqual([]);
        expect(orphan.nodes).toBe(input);
        expect(rejected.connections).toEqual([]);
    });

    test("替换图源只解除导演台已连接的旧图片，不删除素材或其他节点连接", () => {
        const target = { ...createCanvasNode(CanvasNodeType.Video, { x: 0, y: 0 }, { workflowKind: "shot", referenceAssetNodeIds: ["old-image", "old-video"] }), id: "director" };
        const oldImage = image("old-image", "旧参考图");
        const nextImage = image("new-image", "新参考图");
        const video = { ...createCanvasNode(CanvasNodeType.Video, { x: 0, y: 0 }), id: "old-video" };
        const unrelatedEdge = { id: "video-edge", fromNodeId: video.id, toNodeId: target.id };
        const imageEdge = { id: "image-edge", fromNodeId: oldImage.id, toNodeId: target.id };
        const input = [target, oldImage, nextImage, video];

        const result = connectDirectorReferenceNodes(input, [imageEdge, unrelatedEdge], [nextImage.id], target.id, () => "new-edge", "replace");

        expect(result.connections.map(({ fromNodeId, toNodeId }) => [fromNodeId, toNodeId])).toEqual([["old-video", "director"], ["new-image", "director"]]);
        expect(result.nodes.find((node) => node.id === target.id)?.metadata?.referenceAssetNodeIds).toEqual(["old-video", "new-image"]);
        expect(result.nodes.some((node) => node.id === oldImage.id)).toBe(true);
    });
});

describe("导演台截图接入画布", () => {
    const screenshot = { id: "shot-1", name: "主摄影机-1", url: "/assets/a.png", storageKey: "resource:a", width: 1280, height: 720, createdAt: "2026-10-12T00:00:00.000Z" };

    test("截图节点沿用截图已有资源，不重新上传也不改动 storageKey", () => {
        expect(directorScreenshotNodeMetadata(screenshot)).toEqual({ content: "/assets/a.png", storageKey: "resource:a", status: "success", naturalWidth: 1280, naturalHeight: 720, mimeType: "image/png" });
    });

    test("生成的是可被画布识别的图片节点，标题沿用截图名", () => {
        const node = createDirectorScreenshotCanvasNode(screenshot, { x: 100, y: 60 });
        expect(node.type).toBe(CanvasNodeType.Image);
        expect(node.title).toBe("主摄影机-1");
        expect(node.metadata?.content).toBe("/assets/a.png");
        expect(node.metadata?.storageKey).toBe("resource:a");
        expect(node.width).toBeGreaterThan(0);
        expect(node.height).toBeGreaterThan(0);
    });

    test("同一张截图重复发送是幂等的：节点 id 每次都新，但不会复用被顶掉的引用", () => {
        const first = createDirectorScreenshotCanvasNode(screenshot, { x: 0, y: 0 });
        const second = createDirectorScreenshotCanvasNode(screenshot, { x: 0, y: 0 });
        expect(first.id).not.toBe(second.id);
        const target = { ...createCanvasNode(CanvasNodeType.Video, { x: 0, y: 0 }, { workflowKind: "shot", referenceAssetNodeIds: ["existing"] }), id: "director" } as CanvasNodeData;
        const linked = connectDirectorReferenceNodes([target, first], [], [first.id], "director", () => "edge", "append");
        expect(linked.nodes[0].metadata?.referenceAssetNodeIds).toEqual(["existing", first.id]);
    });
});
