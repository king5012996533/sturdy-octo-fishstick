import assert from "node:assert/strict";
import test from "node:test";

// @ts-expect-error -- Node 原生 TypeScript 测试运行器需要保留扩展名。
import { modelBrandIconId } from "./model-brand-icon.ts";

test("平台模型按厂商标识兜底到真实品牌图标", () => {
    assert.equal(modelBrandIconId("bytedance/seedance-1-pro"), "ByteDance");
    assert.equal(modelBrandIconId("bytedance/seedream-4"), "ByteDance");
    assert.equal(modelBrandIconId("google/veo-3"), "Google");
    assert.equal(modelBrandIconId("kwaivgi/kling-v2.6"), "Kling");
    assert.equal(modelBrandIconId("minimax/hailuo-02"), "Hailuo");
    assert.equal(modelBrandIconId("wan-video/wan-2.5-t2v"), "Alibaba");
    assert.equal(modelBrandIconId("deepseek-flash"), "DeepSeek");
});

test("具体模型优先于厂商通配", () => {
    // google/nano-banana 必须是 Nano Banana 自己的图标，而不是 Google 通配。
    assert.equal(modelBrandIconId("google/nano-banana"), "NanoBanana");
    // 黑森林实验室的 FLUX 用 Flux 图标，比厂商 Bfl 图标更好认。
    assert.equal(modelBrandIconId("black-forest-labs/flux-schnell"), "Flux");
    assert.equal(modelBrandIconId("prunaai/flux-fast"), "Flux");
});

test("渠道名带厂商时品牌列表也能出行标", () => {
    assert.equal(modelBrandIconId("Replicate · 主账号"), "Replicate");
    assert.equal(modelBrandIconId("SiliconFlow · 主账号"), "SiliconCloud");
    assert.equal(modelBrandIconId("BeefAPI"), "");
});

test("识别不出来的模型返回空串，交给调用方用占位图标", () => {
    assert.equal(modelBrandIconId("my-finetune-v3"), "");
    assert.equal(modelBrandIconId(""), "");
    assert.equal(modelBrandIconId(undefined), "");
});
