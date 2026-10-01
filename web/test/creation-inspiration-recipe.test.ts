import { describe, expect, test } from "bun:test";
import { creationRecipeAttachments, creationRecipePlan, creationRecipeSummary } from "../src/pages/create/creation-inspiration-recipe";

/**
 * 复刻配方是"提示词之外还需要什么"的载体。这些用例钉住三件事：什么该带走、带不动的
 * 怎么降级、以及搬参考图时单张失败不该让整次复用失败。
 */

function uploadedImage(index: number) {
    return {
        url: `https://app.example.com/api/public/resources/r${index}/file/a.jpg`,
        storageKey: `resource:r${index}`,
        width: 1280,
        height: 720,
        bytes: 180_000,
        mimeType: "image/jpeg",
    };
}

describe("复刻配方", () => {
    test("参考图、时长、比例、分辨率与原作模型都被读出来", () => {
        const plan = creationRecipePlan({
            referenceImages: ["https://cdn.example.com/a.png", "https://cdn.example.com/b.png"],
            sourceVideoModel: "star-video2",
            sourceRatio: "16:9",
            sourceResolution: "720p",
            sourceDurationSeconds: 15,
        });
        expect(plan).toEqual({
            sourceModel: "star-video2",
            referenceImages: ["https://cdn.example.com/a.png", "https://cdn.example.com/b.png"],
            ratio: "16:9",
            resolution: "720p",
            seconds: "15",
        });
    });

    test("没有任何配方信息时返回 null：调用方据此不显示提示条", () => {
        expect(creationRecipePlan({})).toBeNull();
        expect(creationRecipePlan({ referenceImages: [], sourceDurationSeconds: 0 })).toBeNull();
    });

    test("参考图截到 4 张，非 http(s) 地址被剔除", () => {
        const plan = creationRecipePlan({
            referenceImages: ["https://cdn.example.com/1.png", "/local/only.png", "https://cdn.example.com/2.png", "https://cdn.example.com/3.png", "https://cdn.example.com/4.png", "https://cdn.example.com/5.png"],
        });
        expect(plan?.referenceImages).toEqual([
            "https://cdn.example.com/1.png",
            "https://cdn.example.com/2.png",
            "https://cdn.example.com/3.png",
            "https://cdn.example.com/4.png",
        ]);
    });

    test("只有模型名也算配方：界面要如实说明原作与我们用的不是同一个模型", () => {
        expect(creationRecipePlan({ sourceVideoModel: "kling-v3-omni" })?.sourceModel).toBe("kling-v3-omni");
    });

    test("提示条文案只列出真正带过来的项", () => {
        expect(creationRecipeSummary({ referenceImages: ["a", "b"], ratio: "16:9", seconds: "15" })).toBe("2 张参考图 · 15 秒 · 16:9");
        expect(creationRecipeSummary({ referenceImages: [] })).toBe("");
    });
});

describe("参考图搬运", () => {
    test("搬过来的附件带 resource: 前缀，生成侧才认得出素材已在资源库里", async () => {
        const calls: Blob[] = [];
        const originalFetch = globalThis.fetch;
        globalThis.fetch = (async () => new Response(new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" }), { status: 200 })) as typeof fetch;
        try {
            const { attachments, failed } = await creationRecipeAttachments(
                { referenceImages: ["https://cdn.example.com/a.png", "https://cdn.example.com/b.png"] },
                {
                    upload: async (blob: Blob) => {
                        calls.push(blob);
                        return uploadedImage(calls.length);
                    },
                },
            );
            expect(failed).toBe(0);
            expect(attachments.map((item) => item.storageKey)).toEqual(["resource:r1", "resource:r2"]);
            expect(attachments.every((item) => item.previewUrl.startsWith("https://"))).toBe(true);
        } finally {
            globalThis.fetch = originalFetch;
        }
    });

    test("单张取不到只损失那一张，其余照常带走", async () => {
        const originalFetch = globalThis.fetch;
        globalThis.fetch = (async (input: RequestInfo | URL) => {
            const url = String(input);
            if (url.includes("broken")) return new Response("nope", { status: 403 });
            return new Response(new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" }), { status: 200 });
        }) as typeof fetch;
        try {
            let uploaded = 0;
            const { attachments, failed } = await creationRecipeAttachments(
                { referenceImages: ["https://cdn.example.com/ok-1.png", "https://cdn.example.com/broken.png", "https://cdn.example.com/ok-2.png"] },
                { upload: async () => uploadedImage(++uploaded) },
            );
            expect(attachments).toHaveLength(2);
            expect(failed).toBe(1);
        } finally {
            globalThis.fetch = originalFetch;
        }
    });

    test("上游整段不可用时全部记为失败，而不是抛出去打断使用流程", async () => {
        const originalFetch = globalThis.fetch;
        globalThis.fetch = (async () => {
            throw new Error("network down");
        }) as typeof fetch;
        try {
            const { attachments, failed } = await creationRecipeAttachments({ referenceImages: ["https://cdn.example.com/a.png"] }, { upload: async () => uploadedImage(1) });
            expect(attachments).toHaveLength(0);
            expect(failed).toBe(1);
        } finally {
            globalThis.fetch = originalFetch;
        }
    });
});
