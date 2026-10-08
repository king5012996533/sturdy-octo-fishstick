import { describe, expect, test } from "bun:test";

import { pollGeminiVeoTask } from "@/services/api/video-provider-gemini";
import type { ResolvedAiConfig, VideoGenerationTask } from "@/services/api/video-contracts";
import type { VideoProviderDeps } from "@/services/api/video-provider-deps";

const config = { baseUrl: "https://generativelanguage.googleapis.com", apiKey: "PRIVATE_SENTINEL" } as unknown as ResolvedAiConfig;
const task = { id: "operation-1" } as VideoGenerationTask;

function depsFor(videoUri: string) {
    const captured: { url?: string; headers?: Record<string, string> } = {};
    const deps = {
        transport: {
            get: async () => ({ done: true, response: { uri: videoUri } }),
            getExternalBlob: async (url: string, headers: Record<string, string>) => {
                captured.url = url;
                captured.headers = headers;
                return new Blob(["video"]);
            },
        },
        response: { assertVideoBlob: async () => {} },
    } as unknown as VideoProviderDeps;
    return { deps, captured };
}

describe("Gemini Veo 成片下载的凭据范围", () => {
    test("成片地址与渠道同源时仍然携带 x-goog-api-key", async () => {
        const { deps, captured } = depsFor("https://generativelanguage.googleapis.com/v1beta/files/abc:download?alt=media");
        await pollGeminiVeoTask(deps, config, task);
        expect(captured.headers).toEqual({ "x-goog-api-key": "PRIVATE_SENTINEL" });
    });

    test("上游返回第三方地址时不带密钥，避免把它骗到攻击者服务器", async () => {
        const { deps, captured } = depsFor("https://attacker.invalid/steal");
        await pollGeminiVeoTask(deps, config, task);
        expect(captured.url).toBe("https://attacker.invalid/steal");
        expect(captured.headers).toBeUndefined();
    });

    test("子域不等于同源，同样不带密钥", async () => {
        const { deps, captured } = depsFor("https://generativelanguage.googleapis.com.attacker.invalid/x");
        await pollGeminiVeoTask(deps, config, task);
        expect(captured.headers).toBeUndefined();
    });

    test("渠道没配 baseUrl 时按失败关闭处理，不带密钥", async () => {
        const { deps, captured } = depsFor("https://generativelanguage.googleapis.com/v1beta/files/abc:download");
        await pollGeminiVeoTask(deps, { ...config, baseUrl: "" } as unknown as ResolvedAiConfig, task);
        expect(captured.headers).toBeUndefined();
    });
});
