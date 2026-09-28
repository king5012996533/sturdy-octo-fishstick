import assert from "node:assert/strict";
import test from "node:test";

// Bun 直接执行 TypeScript 测试时需要保留扩展名；生产 tsconfig 不包含 test/。
import { DEFAULT_VIDEO_PROMPT_MAX_CHARS, defaultModelCapabilityConfig, modelCapabilityConfigFor, normalizeVideoValue } from "../src/lib/model-capabilities.ts";
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
    assert.ok(profile.operations.includes("audio_to_video"));
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
