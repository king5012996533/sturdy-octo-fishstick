import { requestAudioGeneration } from "@/services/api/audio";
import { requestGeneration, requestImageQuestion } from "@/services/api/image";
import { createVideoGenerationTask } from "@/services/api/video";
import { channelHasGenerationCredential, defaultConfig, encodeChannelModel, isBuiltinBeefAPIChannel, type ModelCapability, type ModelChannel } from "@/stores/use-config-store";
import type { ModelProtocol } from "@/lib/model-protocols";
import type { ImageCapabilityConfig } from "@/lib/model-capabilities";

/**
 * 测试连接用的尺寸取模型自己声明的第一个精确尺寸。
 *
 * 各家的可生成尺寸范围并不一样（例如火山方舟 Seedream 5.0 Lite 的最小总像素是 3.69M），
 * 写死 1024x1024 会让这些模型在"测试连接"里回一个上游参数错误，看起来像渠道配错了。
 * 模型没声明任何精确尺寸时退回 1024x1024。
 */
function testImageSize(image?: ImageCapabilityConfig) {
    const declared = image?.size?.values?.find((value) => value.trim() && value.trim().toLowerCase() !== "auto");
    return declared || "1024x1024";
}

export async function testChannelModelConnection(channel: ModelChannel, model: string, capability: ModelCapability, protocol: ModelProtocol) {
    if (!channel.baseUrl.trim()) throw new Error("请先填写 Base URL");
    if (!channelHasGenerationCredential(channel)) {
        throw new Error(isBuiltinBeefAPIChannel(channel) ? "请先连接 BeefAPI" : "请先填写 API Key");
    }
    const selectedModel = encodeChannelModel(channel.id, model);
    const modelProfile = channel.modelProfiles?.find((item) => item.model === model);
    const testProtocol = channel.apiFormat === "gemini" && !modelProfile?.protocol ? undefined : protocol;
    const testChannel: ModelChannel = {
        ...channel,
        models: channel.models.includes(model) ? channel.models : [...channel.models, model],
        modelProfiles: [
            {
                model,
                displayName: modelProfile?.displayName,
                capability,
                protocol: testProtocol,
                capabilityConfig: modelProfile?.capabilityConfig,
            },
            ...(channel.modelProfiles || []).filter((item) => item.model !== model),
        ],
    };
    const config = {
        ...defaultConfig,
        channelMode: "remote" as const,
        baseUrl: channel.baseUrl,
        apiKey: channel.apiKey,
        apiFormat: channel.apiFormat,
        channels: [testChannel],
        model: selectedModel,
        imageModel: selectedModel,
        videoModel: selectedModel,
        textModel: selectedModel,
        audioModel: selectedModel,
        models: [selectedModel],
        imageModels: capability === "image" ? [selectedModel] : [],
        videoModels: capability === "video" ? [selectedModel] : [],
        textModels: capability === "text" ? [selectedModel] : [],
        audioModels: capability === "audio" ? [selectedModel] : [],
        count: "1",
        size: capability === "image" ? testImageSize(modelProfile?.capabilityConfig?.image) : "16:9",
        videoSeconds: "6",
        vquality: "720",
        videoGenerateAudio: "false",
    };

    switch (capability) {
        case "text":
            await requestImageQuestion(config, [{ role: "user", content: "Reply with OK." }], () => undefined);
            return "文本响应正常";
        case "image":
            await requestGeneration(config, "A simple gray circle on a white background.");
            return "图片生成正常";
        case "audio":
            await requestAudioGeneration(config, "Model test.");
            return "音频生成正常";
        case "video": {
            const task = await createVideoGenerationTask(config, "A static gray circle on a white background.");
            return `视频任务已创建（${task.id}）`;
        }
    }
}
