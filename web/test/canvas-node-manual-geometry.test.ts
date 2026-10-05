import { describe, expect, test } from "bun:test";

import { buildGenerationTaskNodeResult, type GenerationResultMediaIO } from "@/lib/canvas/canvas-generation-task-sync";
import { MEDIA_NODE_MAX_SIZE, hasManualNodeGeometry } from "@/lib/canvas/canvas-node-size";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";
import type { GenerationTask } from "@/services/api/task-center";

const mediaIO: GenerationResultMediaIO = {
    resolveImageUrl: async (storageKey, fallback = "") => `resolved-image:${storageKey}:${fallback}`,
    uploadImage: async () => ({ url: "uploaded-image", storageKey: "image:new", width: 1024, height: 1024, bytes: 4, mimeType: "image/png" }),
    resolveMediaUrl: async (storageKey, fallback = "") => `resolved-media:${storageKey}:${fallback}`,
    storeGeneratedVideo: async () => ({ url: "uploaded-video", storageKey: "video:new", bytes: 8, mimeType: "video/mp4", width: 1920, height: 1080 }),
    storeGeneratedAudio: async () => ({ url: "uploaded-audio", storageKey: "audio:new", bytes: 8, mimeType: "audio/mpeg", durationMs: 1200 }),
    fetchBlob: async () => new Blob(["x"], { type: "audio/mpeg" }),
};

function mediaNode(type: CanvasNodeType.Image | CanvasNodeType.Video, metadata: CanvasNodeData["metadata"] = {}): CanvasNodeData {
    return { id: `${type}-node`, type, title: type, position: { x: 40, y: 40 }, width: 320, height: 180, metadata: { prompt: "结果回填", ...metadata } };
}

function task(type: string, result: unknown): GenerationTask {
    return {
        id: `task-${type}`,
        projectId: "7vvfM674HnenwTekmj88V",
        type,
        status: "succeeded",
        prompt: "结果回填",
        resultJson: JSON.stringify(result),
        attempts: 1,
        createdAt: "2026-09-24T00:00:00.000Z",
        updatedAt: "2026-09-24T00:00:00.000Z",
    };
}

describe("hasManualNodeGeometry", () => {
    test("锁定、自由比例、手动拉过任一命中即视为手动定过几何", () => {
        expect(hasManualNodeGeometry({ metadata: {} } as CanvasNodeData)).toBe(false);
        expect(hasManualNodeGeometry({ metadata: { locked: true } } as CanvasNodeData)).toBe(true);
        expect(hasManualNodeGeometry({ metadata: { freeResize: true } } as CanvasNodeData)).toBe(true);
        expect(hasManualNodeGeometry({ metadata: { manualSize: true } } as CanvasNodeData)).toBe(true);
    });
});

describe("生成结果回填不覆盖用户拉过的卡片", () => {
    test("普通视频节点仍按上游产物的真实比例重算尺寸", async () => {
        const video = await buildGenerationTaskNodeResult(mediaNode(CanvasNodeType.Video), task("canvas_video", { video: { dataUrl: "blob:expired-video", storageKey: "video:local-1", width: 1920, height: 1080 } }), undefined, mediaIO);
        expect(video.width).toBe(MEDIA_NODE_MAX_SIZE.width);
        expect(video.height).toBe(315);
    });

    test("手动拉过的视频节点保留原几何", async () => {
        const video = await buildGenerationTaskNodeResult(
            mediaNode(CanvasNodeType.Video, { freeResize: true }),
            task("canvas_video", { video: { dataUrl: "blob:expired-video", storageKey: "video:local-1", width: 1920, height: 1080 } }),
            undefined,
            mediaIO,
        );
        expect(video.width).toBe(320);
        expect(video.height).toBe(180);
        expect(video.position).toEqual({ x: 40, y: 40 });
        // 内容仍然要换成新结果，只是几何不动。
        expect(video.metadata?.storageKey).toBe("video:local-1");
    });

    test("手动拉过的图片节点保留原几何", async () => {
        const image = await buildGenerationTaskNodeResult(
            mediaNode(CanvasNodeType.Image, { manualSize: true }),
            task("canvas_image", { images: [{ dataUrl: "blob:expired-image", storageKey: "image:local-1", width: 1024, height: 1024 }] }),
            undefined,
            mediaIO,
        );
        expect(image.width).toBe(320);
        expect(image.height).toBe(180);
        expect(image.position).toEqual({ x: 40, y: 40 });
        expect(image.metadata?.storageKey).toBe("image:local-1");
    });
});
