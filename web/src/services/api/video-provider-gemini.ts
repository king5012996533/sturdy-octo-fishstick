import { imageToDataUrl } from "@/services/image-storage";
import { isSameUpstreamHost } from "@/lib/upstream-host";
import { modelOptionName } from "@/stores/use-config-store";
import type { ReferenceImage } from "@/types/image";
import type { ReferenceAudio, ReferenceVideo } from "@/types/media";

import { normalizeVideoResolution, normalizeVideoSeconds, normalizeVideoSize } from "./video-validation";
import type { RequestOptions, ResolvedAiConfig, VideoGenerationTask, VideoGenerationTaskState } from "./video-contracts";
import type { VideoProviderDeps } from "./video-provider-deps";

type GeminiVeoOperation = {
    name?: string;
    done?: boolean;
    error?: { message?: string };
    response?: Record<string, unknown>;
};

export async function createGeminiVeoTask(deps: VideoProviderDeps, config: ResolvedAiConfig, model: string, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[], options?: RequestOptions): Promise<VideoGenerationTask> {
    if (references.length > 1 || videoReferences.length || audioReferences.length) throw new Error("Gemini Veo 当前只支持 1 张起始图，不支持参考视频或音频");
    if (references.length && options?.videoEditOperation === "reference_to_video") throw new Error("Gemini Veo 当前不支持角色或风格参考图生视频，请改用支持 reference_to_video 的模型");
    const instance: Record<string, unknown> = { prompt: prompt.trim() };
    if (references[0]) {
        const dataUrl = await imageToDataUrl(references[0]);
        const matched = /^data:([^;,]+);base64,(.+)$/s.exec(dataUrl);
        if (!matched) throw new Error("Gemini Veo 起始图读取失败");
        instance.image = { bytesBase64Encoded: matched[2], mimeType: matched[1] };
    }
    const payload = {
        instances: [instance],
        parameters: {
            aspectRatio: normalizeVideoSize(config.size) || "16:9",
            durationSeconds: Number.parseInt(normalizeVideoSeconds(config.videoSeconds), 10) || 6,
            resolution: normalizeVideoResolution(config.vquality),
            sampleCount: 1,
        },
    };
    try {
        const response = await deps.transport.post<GeminiVeoOperation>(geminiVeoCreateUrl(config, modelOptionName(model)), payload, options);
        if (!response.name) throw new Error("Gemini Veo 没有返回 operation name");
        return { id: response.name, provider: "gemini-veo", model };
    } catch (error) {
        throw new Error(deps.response.readAxiosError(error, "Gemini Veo 任务创建失败"));
    }
}

export async function pollGeminiVeoTask(deps: VideoProviderDeps, config: ResolvedAiConfig, task: VideoGenerationTask, options?: RequestOptions): Promise<VideoGenerationTaskState> {
    try {
        const operation = await deps.transport.get<GeminiVeoOperation>(geminiVeoOperationUrl(config, task.id), options);
        if (operation.error?.message) return { status: "failed", error: operation.error.message };
        if (!operation.done) return { status: "pending" };
        const url = findGeminiVideoURL(operation.response);
        if (!url) return { status: "failed", error: "Gemini Veo 任务已完成但没有返回视频地址" };
        const blob = await deps.transport.getExternalBlob(url, geminiVeoDownloadHeaders(config, url), options);
        await deps.response.assertVideoBlob(blob);
        return { status: "completed", result: { blob } };
    } catch (error) {
        throw new Error(deps.response.readAxiosError(error, "Gemini Veo 任务查询失败"));
    }
}

function geminiVeoCreateUrl(config: ResolvedAiConfig, model: string) {
    return `${geminiVeoBaseUrl(config)}/models/${encodeURIComponent(model)}:predictLongRunning`;
}

function geminiVeoOperationUrl(config: ResolvedAiConfig, operationName: string) {
    return `${geminiVeoBaseUrl(config)}/${operationName.replace(/^\/+/, "")}`;
}

function geminiVeoBaseUrl(config: ResolvedAiConfig) {
    const base = config.baseUrl.replace(/\/+$/, "");
    return /\/v1beta$/i.test(base) ? base : `${base}/v1beta`;
}

function geminiVeoHeaders(config: ResolvedAiConfig) {
    return { "x-goog-api-key": config.apiKey };
}

/**
 * 下载成片时的请求头。
 *
 * 成片地址来自上游响应（findGeminiVideoURL 从 operation.response 里挖出来的任意 uri）。
 * 密钥只发给渠道自己配置的那个端点（协议 + 主机 + 端口都一致，见 isSameUpstreamHost）：
 * 正常 Gemini/Gemini 兼容渠道返回的 files/... 地址与 API 同源，仍然带密钥；第三方地址、明文
 * 降级或同主机的另一个端口都不带——否则自定义渠道只要回一条构造的 uri，就能把用户的
 * x-goog-api-key 骗到自己的服务器上。
 */
function geminiVeoDownloadHeaders(config: ResolvedAiConfig, url: string) {
    return isSameUpstreamHost(config.baseUrl, url) ? geminiVeoHeaders(config) : undefined;
}

function findGeminiVideoURL(value: unknown): string {
    if (!value || typeof value !== "object") return "";
    if (Array.isArray(value)) {
        for (const item of value) {
            const found = findGeminiVideoURL(item);
            if (found) return found;
        }
        return "";
    }
    const record = value as Record<string, unknown>;
    for (const key of ["uri", "url", "videoUri", "video_url"]) {
        if (typeof record[key] === "string" && /^https?:\/\//i.test(record[key])) return record[key];
    }
    for (const child of Object.values(record)) {
        const found = findGeminiVideoURL(child);
        if (found) return found;
    }
    return "";
}
