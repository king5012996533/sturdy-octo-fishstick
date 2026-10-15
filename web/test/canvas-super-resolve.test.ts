import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { MAX_UPSCALE_LONG_EDGE } from "@/lib/canvas/canvas-image-data";
import { SUPER_RESOLVE_MODEL, SUPER_RESOLVE_PROMPT, resolveSuperResolveConfig, resolveSuperResolveOutputSize } from "@/lib/canvas/canvas-image-super-resolve";
import { createModelChannel, defaultConfig, type AiConfig } from "@/stores/use-config-store";

const DIALOG = readFileSync(new URL("../src/components/canvas/canvas-node-super-resolve-dialog.tsx", import.meta.url), "utf8");
const STATUS_DIALOGS = readFileSync(new URL("../src/pages/canvas/canvas-project-status-dialogs.tsx", import.meta.url), "utf8");
const PROJECT = readFileSync(new URL("../src/pages/canvas/project.tsx", import.meta.url), "utf8");
const MEDIA_TOOLS = readFileSync(new URL("../src/pages/canvas/use-canvas-media-tools.ts", import.meta.url), "utf8");

function configWithUpscaleChannel(): AiConfig {
    return {
        ...defaultConfig,
        model: "某个别的图片模型",
        imageModel: "某个别的图片模型",
        channels: [
            createModelChannel({ id: "default", name: "默认渠道", baseUrl: "https://example.com", apiKey: "k", models: ["某个别的图片模型"] }),
            createModelChannel({ id: "CHANNEL_000012", name: "fal.ai · 主账号", baseUrl: "https://fal.run", apiKey: "system", scope: "system", models: [SUPER_RESOLVE_MODEL] }),
        ],
    };
}

test("super resolution targets its own registered model instead of the selected one", () => {
    const config = configWithUpscaleChannel();
    const resolved = resolveSuperResolveConfig(config);
    expect(resolved?.model).toBe(SUPER_RESOLVE_MODEL);
    expect(resolved?.imageModel).toBe(SUPER_RESOLVE_MODEL);
    // 超分是一条固定端点：一张输入、固定出 1 张。面板上的张数/比例/质量都不该漏进去。
    expect(resolved?.count).toBe("1");
    expect(resolved?.size).toBe("auto");
    expect(resolved?.quality).toBe("auto");
});

test("super resolution is unavailable rather than redirected when the channel is missing", () => {
    // 目录里没有这条模型时不能回落到用户当前选中的图片模型：那要么报错，要么按别的模型的价格扣钱。
    const config: AiConfig = { ...defaultConfig, channels: [] };
    expect(resolveSuperResolveConfig(config)).toBeNull();
});

test("the canvas submits the platform endpoint and re-encodes its oversized output", () => {
    expect(MEDIA_TOOLS).toContain("resolveSuperResolveConfig(effectiveConfig)");
    expect(MEDIA_TOOLS).toContain("reencodeImageDataUrl(image.dataUrl, { maxLongEdge: MAX_UPSCALE_LONG_EDGE })");
    expect(MEDIA_TOOLS).toContain("prompt: SUPER_RESOLVE_PROMPT");
    expect(MAX_UPSCALE_LONG_EDGE).toBe(4096);
});

test("the quoted output size follows the real 4x result instead of the canvas ceiling", () => {
    // 1024 是完整交付的 4 倍：这一档不能被"长边上限"误当成 4096 的目标尺寸算成 1 倍或 4 倍以外的数。
    expect(resolveSuperResolveOutputSize(1024, 1024)).toEqual({ width: 4096, height: 4096, factor: 4, capped: false });
    // 32×32 的原图只会回来 128×128——按"目标长边 4096"去预告，用户会拿到一张尺寸对不上的图。
    expect(resolveSuperResolveOutputSize(32, 32)).toEqual({ width: 128, height: 128, factor: 4, capped: false });
    // 2K 的原图放大到 8192 会超上界，交付尺寸与倍率都跟着收口，界面必须说清楚。
    const capped = resolveSuperResolveOutputSize(2048, 1024);
    expect(capped).toEqual({ width: 4096, height: 2048, factor: 2, capped: true });
});

test("the dialog waits for the quote instead of calling the channel unpriced", () => {
    // 弹窗打开的第一帧还没有报价（试算要防抖 + 一次往返）。那一帧若显示"该通道尚未定价"，
    // 用户看到的是"这个功能坏了"，而事实只是"还没问到"。
    expect(DIALOG).not.toContain("estimate.loading && !estimate.quote");
    expect(DIALOG).toContain("if (!estimate.quote) {");
    expect(DIALOG).toContain("正在试算消耗…");
    // 未定价分支直接读 quote 自己的字段，挂了可选链就会把 quote 为空也归进这一支。
    expect(DIALOG).toContain("if (!estimate.quote.priced) {");
});

test("the confirm dialog quotes the charge through the backend", () => {
    // 价格只能由后端试算：前端写死数字会在运营调价后停在旧值上。
    expect(DIALOG).toContain("useTaskChargeQuote");
    expect(DIALOG).toContain("SUPER_RESOLVE_PROMPT");
    expect(DIALOG).not.toMatch(/积分.*\d{2,}/);
    expect(STATUS_DIALOGS).toContain("CanvasNodeSuperResolveDialog");
    expect(STATUS_DIALOGS).toContain("resolveSuperResolveConfig");
    // 旧的"本地高质量插值"兜底已经拆掉：它既不是超分，也不该出现在一个付费按钮后面。
    expect(STATUS_DIALOGS).not.toContain("使用本地高质量放大");
    expect(PROJECT).toContain("void superResolveImageNode(node)");
});
