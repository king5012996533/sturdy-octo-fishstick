import { afterEach, describe, expect, test } from "bun:test";

import { buildGenerationTaskNodeResult, defaultGenerationResultMediaIO } from "../src/lib/canvas/canvas-generation-task-sync";
import type { GenerationTask } from "../src/services/api/task-center";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";

const originalFetch = globalThis.fetch;
afterEach(() => {
    globalThis.fetch = originalFetch;
});

function audioNode(): CanvasNodeData {
    return { id: "audio-node", type: CanvasNodeType.Audio, title: "audio", position: { x: 0, y: 0 }, width: 320, height: 180, metadata: { prompt: "配音" } };
}

function audioTask(result: unknown): GenerationTask {
    return {
        id: "task-canvas_audio",
        projectId: "project-1",
        type: "canvas_audio",
        status: "succeeded",
        prompt: "配音",
        resultJson: JSON.stringify(result),
        attempts: 1,
        createdAt: "2026-09-24T00:00:00.000Z",
        updatedAt: "2026-09-24T00:00:00.000Z",
    };
}

function stubFetch(status: number, body: Uint8Array | string) {
    const requested: string[] = [];
    globalThis.fetch = (async (url: string | URL | Request) => {
        requested.push(String(url));
        return new Response(body, { status, headers: { "content-type": status === 200 ? "audio/mpeg" : "text/html" } });
    }) as unknown as typeof fetch;
    return requested;
}

/** 真实下载路径 + 只替换存储侧，这样断言的是"下载到的字节"，而不是一个桩。 */
function mediaIOWithRecordingStore(recorded: Blob[]) {
    return {
        ...defaultGenerationResultMediaIO,
        storeGeneratedAudio: async (blob: Blob) => {
            recorded.push(blob);
            return { url: "blob:stored-audio", storageKey: "", bytes: blob.size, mimeType: "audio/mpeg" };
        },
    };
}

describe("生成结果下载的 HTTP 状态检查", () => {
    test("404 时抛错且不落库，而不是把错误页当成音频字节存下来", async () => {
        const requested = stubFetch(404, "<html>not found</html>");
        const recorded: Blob[] = [];
        await expect(buildGenerationTaskNodeResult(audioNode(), audioTask({ audio: { url: "https://cdn.invalid/missing.mp3" } }), [audioNode()], mediaIOWithRecordingStore(recorded)))
            .rejects.toThrow("生成结果下载失败（HTTP 404）");
        expect(requested).toEqual(["https://cdn.invalid/missing.mp3"]);
        expect(recorded).toHaveLength(0);
    });

    test("500 时同样抛错且不落库", async () => {
        const requested = stubFetch(500, "<html>boom</html>");
        const recorded: Blob[] = [];
        await expect(buildGenerationTaskNodeResult(audioNode(), audioTask({ audio: { url: "https://cdn.invalid/boom.mp3" } }), [audioNode()], mediaIOWithRecordingStore(recorded)))
            .rejects.toThrow("生成结果下载失败（HTTP 500）");
        expect(requested).toHaveLength(1);
        expect(recorded).toHaveLength(0);
    });

    test("2xx 时把真实字节交给存储侧，并落到节点元数据上", async () => {
        const requested = stubFetch(200, new Uint8Array([1, 2, 3]));
        const recorded: Blob[] = [];
        const node = await buildGenerationTaskNodeResult(audioNode(), audioTask({ audio: { url: "https://cdn.invalid/ok.mp3" } }), [audioNode()], mediaIOWithRecordingStore(recorded));
        expect(requested).toEqual(["https://cdn.invalid/ok.mp3"]);
        expect(recorded).toHaveLength(1);
        expect([...new Uint8Array(await recorded[0].arrayBuffer())]).toEqual([1, 2, 3]);
        expect(node.metadata.status).toBe("success");
        expect(node.metadata.content).toBe("blob:stored-audio");
    });
});
