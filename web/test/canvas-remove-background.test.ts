import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { REMOVE_BACKGROUND_MODEL, REMOVE_BACKGROUND_PROMPT, resolveRemoveBackgroundConfig } from "@/lib/canvas/canvas-image-remove-background";
import { createModelChannel, defaultConfig, type AiConfig } from "@/stores/use-config-store";

const DIALOG = readFileSync(new URL("../src/components/canvas/canvas-node-remove-background-dialog.tsx", import.meta.url), "utf8");
const BATCH_DIALOG = readFileSync(new URL("../src/components/canvas/canvas-batch-remove-background-dialog.tsx", import.meta.url), "utf8");
const TOOLBAR = readFileSync(new URL("../src/components/canvas/canvas-image-toolbar-tools.tsx", import.meta.url), "utf8");
const STATUS_DIALOGS = readFileSync(new URL("../src/pages/canvas/canvas-project-status-dialogs.tsx", import.meta.url), "utf8");
const MEDIA_TOOLS = readFileSync(new URL("../src/pages/canvas/use-canvas-media-tools.ts", import.meta.url), "utf8");

/** 抠图那一段源码：单张入口、批量入口与它们的共用提交逻辑都在这里。 */
const REMOVE_BACKGROUND_SECTION = MEDIA_TOOLS.slice(
    MEDIA_TOOLS.indexOf("const runRemoveBackgroundNode = useCallback"),
    MEDIA_TOOLS.indexOf("const generateAngleNode = useCallback"),
);

function configWithRemoveBackgroundChannel(): AiConfig {
    return {
        ...defaultConfig,
        model: "某个别的图片模型",
        imageModel: "某个别的图片模型",
        channels: [
            createModelChannel({ id: "default", name: "默认渠道", baseUrl: "https://example.com", apiKey: "k", models: ["某个别的图片模型"] }),
            createModelChannel({ id: "CHANNEL_000012", name: "fal.ai · 主账号", baseUrl: "https://fal.run", apiKey: "system", scope: "system", models: [REMOVE_BACKGROUND_MODEL] }),
        ],
    };
}

test("background removal targets its own registered model instead of the selected one", () => {
    const config = configWithRemoveBackgroundChannel();
    const resolved = resolveRemoveBackgroundConfig(config);
    expect(resolved?.model).toBe(REMOVE_BACKGROUND_MODEL);
    expect(resolved?.imageModel).toBe(REMOVE_BACKGROUND_MODEL);
    // 固定端点：一张输入、固定出 1 张，面板上的张数/比例/质量都不该漏进去。
    expect(resolved?.count).toBe("1");
    expect(resolved?.size).toBe("auto");
    expect(resolved?.quality).toBe("auto");
});

test("background removal is unavailable rather than redirected when the channel is missing", () => {
    // 目录里没有这条模型时不能回落到用户当前选中的图片模型：那要么报错，要么按别的模型的价格扣钱。
    const config: AiConfig = { ...defaultConfig, channels: [] };
    expect(resolveRemoveBackgroundConfig(config)).toBeNull();
});

test("the delivered PNG is never re-encoded, unlike the upscaler", () => {
    // 抠图的交付物正是一圈边缘：透明边被有损压过一次就会出灰边。这与超分相反，
    // 所以这一段里不能出现重编码，结果必须直接落库。
    expect(REMOVE_BACKGROUND_SECTION).toContain("uploadImage(image.dataUrl)");
    expect(REMOVE_BACKGROUND_SECTION).not.toContain("reencodeImageDataUrl");
});

test("one submission path serves both the single and the batch entry", () => {
    // 批量不是另一套提交逻辑：它只差“要不要抢画布焦点”与“轮次失败该不该退让”。
    // 两条路径各写一遍，很快就会出现“单张能用、批量报错”这类只在批量里复现的毛病。
    expect(REMOVE_BACKGROUND_SECTION).toContain("const runRemoveBackgroundNode = useCallback");
    expect(REMOVE_BACKGROUND_SECTION).toContain("await runRemoveBackgroundNode(node, { focus: true })");
    expect(REMOVE_BACKGROUND_SECTION).toContain("runRemoveBackgroundNode(node, { focus: false, waitForCapacity: true })");
});

test("the batch respects the platform concurrency policy instead of firing everything at once", () => {
    // 抠图一张一个任务：一把全发会把并行槽位占满，用户自己在跑的任务开始收“任务已满”。
    expect(MEDIA_TOOLS).toContain("useUserStore((state) => state.runtimeLimits.activeTaskLimit)");
    expect(REMOVE_BACKGROUND_SECTION).toContain("Math.min(targets.length, activeTaskLimit)");
    // 撞上上限是平台忙，不是这张图的错：退让后重投，不能记成失败。
    expect(REMOVE_BACKGROUND_SECTION).toContain("isGenerationTaskCapacityError(error)");
    expect(REMOVE_BACKGROUND_SECTION).toContain("REMOVE_BACKGROUND_CAPACITY_RETRY_LIMIT");
});

test("the batch dialog quotes the per-image price and labels the multiplication", () => {
    // 价格只能由后端试算：前端写死数字会在运营调价后停在旧值上。张数是用户自己选的，乘一下不是“自己算价”。
    expect(BATCH_DIALOG).toContain("useTaskChargeQuote");
    expect(BATCH_DIALOG).toContain("REMOVE_BACKGROUND_PROMPT");
    // \u5408\u8ba1\u53ea\u80fd\u662f\u201c\u540e\u7aef\u5355\u4ef7 \u00d7 \u5f20\u6570\u201d\uff1a\u5355\u4ef7\u6765\u81ea\u8bd5\u7b97\uff0c\u5f20\u6570\u6765\u81ea\u7528\u6237\u9009\u4e2d\u3002
    expect(BATCH_DIALOG).toMatch(/unitPrice === null \? null : unitPrice \* count/);
    expect(BATCH_DIALOG).not.toMatch(/积分.*\d{2,}/);
    expect(STATUS_DIALOGS).toContain("CanvasBatchRemoveBackgroundDialog");
});

test("both entries are wired end to end", () => {
    expect(TOOLBAR).toContain('id: "removeBackground"');
    expect(STATUS_DIALOGS).toContain("CanvasNodeRemoveBackgroundDialog");
    expect(REMOVE_BACKGROUND_PROMPT).toBe("移除背景");
});
