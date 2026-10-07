import assert from "node:assert/strict";
import test from "node:test";

// Bun 直接执行 TypeScript 测试时需要保留扩展名；生产 tsconfig 不包含 test/。
import { DEFAULT_VIDEO_PROMPT_MAX_CHARS, defaultModelCapabilityConfig, modelCapabilityConfigFor, normalizeVideoValue, videoDurationAllowed, videoDurationConfigFor, videoDurationOptions } from "../src/lib/model-capabilities.ts";
import { modelCompatibilityError } from "../src/lib/model-selection.ts";
import type { AiConfig } from "../src/stores/use-config-store.ts";

test("switching to MiniMax H3 replaces an unsupported 720p value with 768P", () => {
    const profile = defaultModelCapabilityConfig("minimax-video", "MiniMax-H3").video!;

    assert.deepEqual(normalizeVideoValue(profile, { seconds: "11", ratio: "16:9", resolution: "720" }), {
        seconds: "11",
        ratio: "16:9",
        resolution: "768P",
    });
});

// 视频提示词由「输入框文本 + 连线内容 + 技能上下文」合成，技能上下文预算为 32000，
// 合成结果远长于用户手输内容。默认上限过小会把正常可用的画布工作流拦在本地预检。
// 这里锁定默认值本身，避免被改回偏小值（前端放行/后端拒绝的判定必须同源）。
test("video prompt default allows a composed canvas prompt", () => {
    assert.equal(DEFAULT_VIDEO_PROMPT_MAX_CHARS, 8000);
    for (const protocol of [undefined, "seedance-videos-compatible", "agnes-video", "volcengine-ark-video"]) {
        const profile = defaultModelCapabilityConfig(protocol, "test-model");
        assert.equal(profile.video!.references.promptMaxChars, DEFAULT_VIDEO_PROMPT_MAX_CHARS);
    }
});

test("raising the video default leaves text and image limits untouched", () => {
    // 只放宽视频默认值，避免顺带改变其它能力的判定口径。
    const profile = defaultModelCapabilityConfig("seedance-videos-compatible", "sd-2.5");
    assert.equal(profile.text!.references.promptMaxChars, 32000);
    assert.equal(profile.image!.references.promptMaxChars, 32000);
});

test("APIMart NewAPI channel exposes Seedance multimodal reference operations", () => {
    const profile = defaultModelCapabilityConfig("newapi-channel-2", "seedance-2.0-fast").video!;
    assert.deepEqual(profile.references.maxVideos, 3);
    assert.ok(profile.operations.includes("reference_to_video"));
    assert.ok(!profile.operations.includes("audio_to_video"));
});

test("BeefAPI generic newapi Seedance accepts mixed image and video references without a persisted capability profile", () => {
    const model = "beefapi::seedance-2.0-fast";
    const config = {
        channels: [{
            id: "beefapi",
            name: "BeefAPI",
            baseUrl: "https://enterprise.beefapi.com",
            apiKey: "test",
            apiFormat: "openai",
            models: ["seedance-2.0-fast"],
            modelProfiles: [{
                model: "seedance-2.0-fast",
                capability: "video",
                protocol: "newapi",
                billingMode: "fixed_request",
                unitPriceMicrocredits: 0,
            }],
        }],
    } as AiConfig;

    const profile = modelCapabilityConfigFor(config, model).video!;
    assert.ok(profile.operations.includes("reference_to_video"));
    assert.ok(profile.references.maxVideos >= 1);
    assert.equal(modelCompatibilityError(config, model, {
        capability: "video",
        input: { textCount: 1, imageCount: 1, videoCount: 1, audioCount: 0, characterCount: 0 },
        videoSeconds: "5",
    }), "");
});

// Replicate 的能力合同必须逐模型对齐上游 schema：比例写成前端归一不了的值（9:21 → 3:7）
// 会被上游拒绝，数量或参考图多报会让用户拿到比承诺更少的图。
test("replicate image capability keeps 1:1 ratio and never normalizes to 3:7", () => {
    const flux = defaultModelCapabilityConfig("replicate-prediction-image", "black-forest-labs/flux-schnell").image!;

    assert.equal(flux.size.parameter, "aspect_ratio");
    assert.equal(flux.size.allowCustom, false);
    assert.equal(flux.maxOutputs, 4);
    assert.equal(flux.references.maxImages, 0);
    assert.ok(flux.size.values.includes("16:9"));
    assert.ok(!flux.size.values.includes("9:21"));
    assert.deepEqual(flux.quality.values, ["1k"]);
});

test("replicate image capability matches each upstream schema", () => {
    const imagen = defaultModelCapabilityConfig("replicate-prediction-image", "google/imagen-4").image!;
    assert.deepEqual(imagen.quality.values, ["1k", "2k"]);
    assert.equal(imagen.maxOutputs, 1);

    const seedream = defaultModelCapabilityConfig("replicate-prediction-image", "bytedance/seedream-4").image!;
    assert.deepEqual(seedream.quality.values, ["1k", "2k", "4k"]);
    assert.equal(seedream.maxOutputs, 10);

    const unknown = defaultModelCapabilityConfig("replicate-prediction-image", "someone/unknown-image-model").image!;
    assert.equal(unknown.maxOutputs, 1);
    assert.equal(unknown.references.maxImages, 0);
});

// gpt-image 与 Go 侧共用同一份合同：1-10 张输出、最多 4 张参考图。2.5 上游 quality 五档
// 齐备（价目五档各一行），2.0 只到 high——前缀相同但档位不同，必须按 2.5 细分。
test("replicate gpt-image contract splits 2.0 three tiers from 2.5 five tiers", () => {
    for (const model of ["openai/gpt-image-2.5-sunburst", "openai/gpt-image-2.5-flare"]) {
        const image = defaultModelCapabilityConfig("replicate-prediction-image", model).image!;
        assert.deepEqual(image.quality.values, ["low", "medium", "high", "xhigh", "max"], model);
        assert.equal(image.quality.default, "low", model);
        assert.equal(image.maxOutputs, 10, model);
        assert.equal(image.references.maxImages, 4, model);
        assert.equal(image.size.parameter, "aspect_ratio", model);
        assert.equal(image.size.allowCustom, false, model);
    }
    // 2.0 上游没有这两档：写宽了用户会在提交时被上游拒绝。
    const image20 = defaultModelCapabilityConfig("replicate-prediction-image", "openai/gpt-image-2").image!;
    assert.deepEqual(image20.quality.values, ["low", "medium", "high"]);
    assert.equal(image20.quality.default, "low");
    assert.equal(image20.maxOutputs, 10);
    assert.equal(image20.references.maxImages, 4);

    // imagen-4-fast 没有 image_size 参数，档位只剩 1k；imagen-4 有 1k/2k。
    const imagenFast = defaultModelCapabilityConfig("replicate-prediction-image", "google/imagen-4-fast").image!;
    assert.deepEqual(imagenFast.quality.values, ["1k"]);
    assert.equal(imagenFast.maxOutputs, 1);
    assert.deepEqual(imagenFast.references.maxImages === 0, true);
});

// 视频侧同理：时长档位与分辨率枚举来自各模型 schema，写宽了用户会在提交时被上游拒绝。
test("replicate video capability matches each upstream schema", () => {
    const veo = defaultModelCapabilityConfig("replicate-prediction-video", "google/veo-3").video!;
    assert.deepEqual(veo.duration.values, [4, 6, 8]);
    assert.equal(veo.duration.default, 8);
    assert.deepEqual(veo.resolutions, ["720p", "1080p"]);
    assert.equal(veo.generateAudio.supported, true);

    const kling = defaultModelCapabilityConfig("replicate-prediction-video", "kwaivgi/kling-v2.1").video!;
    assert.deepEqual(kling.operations, ["image_to_video"]);
    assert.equal(kling.references.minImages, 1);
    // 尾帧只在该模型 pro 档开放，平台不收尾帧，否则上游会直接拒绝。
    assert.equal(kling.references.maxImages, 1);

    // kling-v2.1 停用后换上的在售型号：turbo pro 首尾帧都开放，2.6 只有首帧字段。
    const klingPro = defaultModelCapabilityConfig("replicate-prediction-video", "kwaivgi/kling-v2.5-turbo-pro").video!;
    assert.equal(klingPro.references.maxImages, 2);
    assert.equal(klingPro.references.minImages, 0);
    assert.deepEqual(klingPro.operations, ["text_to_video", "image_to_video"]);

    const kling26 = defaultModelCapabilityConfig("replicate-prediction-video", "kwaivgi/kling-v2.6").video!;
    assert.equal(kling26.references.maxImages, 1);
    assert.equal(kling26.duration.default, 5);

    const seedance = defaultModelCapabilityConfig("replicate-prediction-video", "bytedance/seedance-1-pro").video!;
    assert.equal(seedance.duration.selection, "range");
    assert.equal(seedance.duration.min, 2);
    assert.equal(seedance.references.maxImages, 2);
    assert.ok(seedance.ratios.includes("21:9"));

    const hailuo = defaultModelCapabilityConfig("replicate-prediction-video", "minimax/hailuo-02").video!;
    assert.deepEqual(hailuo.ratios, []);
    assert.equal(hailuo.defaultRatio, "");
    assert.deepEqual(hailuo.resolutions, ["512p", "768p", "1080p"]);
});

test("replicate video capability falls back to the minimal shape for unknown models", () => {
    const unknown = defaultModelCapabilityConfig("replicate-prediction-video", "someone/unknown-video-model").video!;
    assert.deepEqual(unknown.operations, ["text_to_video"]);
    assert.deepEqual(unknown.ratios, []);
    assert.deepEqual(unknown.resolutions, []);
    assert.equal(unknown.references.maxImages, 0);
    assert.deepEqual(unknown.duration.values, [5]);
});

// aigenvideo-seedance 插件的请求模板只映射 images[]（自己声明最多 10 张参考图），
// 但通用视频合同只声明文生/图生。画布挂 3 张以上参考图会被推断成 reference_to_video，
// 合同里缺这个操作会让整组视频模型在下拉里被判成不兼容 —— 灰掉且点不动。
test("Aigen Seedance 通道放开多图参考，但不放开音视频参考", () => {
    const model = "CHANNEL_000007::seedance-2.0";
    const config = {
        channels: [{
            id: "CHANNEL_000007",
            name: "Aigen Seedance · 主账号",
            baseUrl: "/api/ai/system/CHANNEL_000007",
            apiKey: "system",
            apiFormat: "aigenvideo-seedance-v20",
            interfaceType: "aigenvideo-seedance-v20",
            models: ["seedance-2.0"],
            modelProfiles: [{
                model: "seedance-2.0",
                displayName: "Seedance 2.0",
                capability: "video",
                protocol: "aigenvideo-seedance-v20",
                capabilityConfig: {
                    version: 1,
                    video: {
                        references: { promptMaxChars: 8000, minImages: 0, maxImages: 10, maxImageBytes: 31457280, maxVideos: 0, maxVideoBytes: 0, maxVideoDurationSeconds: 0, maxAudios: 0, maxAudioBytes: 0, maxAudioDurationSeconds: 0 },
                        duration: { selection: "enum", values: [5, 10, 15], default: 5 },
                        ratios: ["16:9"],
                        defaultRatio: "16:9",
                        resolutions: ["720p"],
                        defaultResolution: "720p",
                        generateAudio: { supported: false, default: false },
                        watermark: { supported: false, default: false },
                        operations: ["text_to_video", "image_to_video"],
                        defaultOperation: "text_to_video",
                    },
                },
            }],
        }],
    } as unknown as AiConfig;

    const profile = modelCapabilityConfigFor(config, model).video!;
    assert.ok(profile.operations.includes("reference_to_video"));
    assert.equal(profile.references.maxImages, 10);
    assert.equal(profile.references.maxVideos, 0);
    assert.equal(profile.references.maxAudios, 0);

    const fourImages = { textCount: 0, imageCount: 4, videoCount: 0, audioCount: 0, characterCount: 0 };
    assert.equal(modelCompatibilityError(config, model, { capability: "video", input: fourImages }), "");
    const withVideoReference = { textCount: 0, imageCount: 1, videoCount: 1, audioCount: 0, characterCount: 0 };
    assert.equal(modelCompatibilityError(config, model, { capability: "video", input: withVideoReference }), "最多支持 0 个参考视频");
});

// 上游可能按分辨率档位限定时长：Seedance 2.0 mini 的 720p 最长 12 秒、480p 能到 15 秒。
// 合同里只有一组 duration 时，界面只能二选一：把 15 秒露给拿不到的档位（用户选完只拿到
// 12 秒的成片），或为 720p 把 480p 的 15 秒也砍掉。durationByResolution 就是给这种情形留的口子。
test("per-resolution duration hides options the tier cannot produce", () => {
    const profile = defaultModelCapabilityConfig("zongheng-video", "saedancMini2.0").video!;
    profile.duration = { selection: "enum", values: [10, 12, 15], default: 15 };
    profile.durationByResolution = {
        "720p": { selection: "enum", values: [10, 12], default: 12 },
        "480p": { selection: "enum", values: [10, 12, 15], default: 15 },
    };
    profile.resolutions = ["480p", "720p"];
    profile.defaultResolution = "720p";

    assert.deepEqual(videoDurationOptions(profile, "720p"), [10, 12]);
    assert.deepEqual(videoDurationOptions(profile, "480p"), [10, 12, 15]);
    assert.equal(videoDurationAllowed(profile, 15, "720p"), false);
    assert.equal(videoDurationAllowed(profile, 15, "480p"), true);
    // auto / 未指定走默认档位，不能因为读不到分辨率就放开到顶层枚举。
    assert.equal(videoDurationAllowed(profile, 15, "auto"), false);
    assert.equal(videoDurationAllowed(profile, 15, undefined), false);
    assert.equal(videoDurationConfigFor(profile, "480p").default, 15);
});

// 从 480p 的 15 秒切到 720p 时，秒数必须收拢到新档位合法的值，
// 否则表单会停在一个点提交就被拒的数字上。
test("normalizeVideoValue snaps second when the resolution changes", () => {
    const profile = defaultModelCapabilityConfig("zongheng-video", "saedancMini2.0").video!;
    profile.duration = { selection: "enum", values: [10, 12, 15], default: 15 };
    profile.durationByResolution = {
        "720p": { selection: "enum", values: [10, 12], default: 12 },
        "480p": { selection: "enum", values: [10, 12, 15], default: 15 },
    };
    profile.resolutions = ["480p", "720p"];
    profile.defaultResolution = "720p";

    assert.equal(normalizeVideoValue(profile, { seconds: "15", ratio: "16:9", resolution: "480p" }).seconds, "15");
    assert.equal(normalizeVideoValue(profile, { seconds: "15", ratio: "16:9", resolution: "720p" }).seconds, "12");
    // 没有按档位登记的模型行为不变。
    const plain = { ...profile, durationByResolution: undefined };
    assert.equal(normalizeVideoValue(plain, { seconds: "15", ratio: "16:9", resolution: "720p" }).seconds, "15");
});
