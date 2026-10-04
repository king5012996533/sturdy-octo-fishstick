import { describe, expect, test } from "bun:test";

import { backendProviderConfig, taskChargeQuoteInput } from "../src/services/api/generation-task";
import { defaultConfig, type AiConfig } from "../src/stores/use-config-store";

/**
 * 生成前试算的请求体，要与提交走同一套模型与能力参数。
 *
 * 这里盯的是"报价与实扣同源"：试算多带一个模型不支持的参数，后端会以能力校验拒掉整次
 * 试算，界面就只能显示一句与价格无关的报错；少带一个参数，报出来的价就是另一个价目行的。
 */
function configWith(overrides: Partial<AiConfig>): AiConfig {
    return { ...defaultConfig, ...overrides } as AiConfig;
}

describe("taskChargeQuoteInput", () => {
    test("模型配置直接取自提交用的那一份", () => {
        const config = configWith({ model: "openai/gpt-image-2", count: "3", quality: "high" });
        const quote = taskChargeQuoteInput({ mode: "image", prompt: "一只猫", config });
        expect(quote.type).toBe("canvas_image");
        expect(quote.operation).toBe("image");
        expect((quote.input as { config: unknown }).config).toEqual(backendProviderConfig(config, "image"));
    });

    test("模型不支持的能力参数不发上去", () => {
        const config = configWith({ model: "seedance-2.0", videoGenerateAudio: "true" });
        const providerConfig = backendProviderConfig(config, "video");
        // 能力声明 supported:false 时只能发默认值，否则后端会以"参数超出支持范围"整单拒绝，
        // 而界面上没有这个开关可供用户关掉。
        expect(providerConfig.videoGenerateAudio).toBe("false");
    });

    test("参考图张数要带进报价，超量加收才报得出来", () => {
        const config = configWith({ model: "minimax/minimax-h3" });
        const quote = taskChargeQuoteInput({ mode: "video", prompt: "一条片子", config, inputSummary: { imageCount: 8 } });
        // 张数是报价的一部分：不带它，面板就会显示一个低于实扣的数。
        expect((quote.input as { referenceImageCount?: number }).referenceImageCount).toBe(8);
        // 没有参考图时不发这个字段，避免给后端留一个恒为 0 的噪声入参。
        const plain = taskChargeQuoteInput({ mode: "video", prompt: "一条片子", config });
        expect((plain.input as { referenceImageCount?: number }).referenceImageCount).toBeUndefined();
    });

    test("提示词为空时也要能报出价", () => {
        const config = configWith({ model: "openai/gpt-image-2" });
        const quote = taskChargeQuoteInput({ mode: "image", prompt: "   ", config });
        expect(quote.prompt.length).toBeGreaterThan(0);
    });
});
