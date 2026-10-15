import { expect, test } from "bun:test";

import { compatibleModelInGroup, flattenModelGroupsByDisplayName, isPlatformOnlyCatalog, resolveCompatibleModel } from "../src/lib/model-selection";
import { createModelChannel, defaultConfig, encodeChannelModel, normalizeConfigSnapshot, resolveModelChannel, selectableModelsByCapability, type ModelChannel } from "../src/stores/use-config-store";

// 复刻线上平台目录：渠道名是上游账号名，模型显示名才是干净的型号。
function systemChannel(id: string, name: string, models: Array<[string, string, "image" | "video" | "audio" | "text"]>): ModelChannel {
    return createModelChannel({
        id,
        name,
        scope: "system",
        baseUrl: `/api/ai/system/${id}`,
        apiKey: "system",
        hasApiKey: true,
        models: models.map(([key]) => key),
        modelProfiles: models.map(([key, displayName, capability]) => ({ model: key, displayName, capability })),
    });
}

function configWith(channels: ModelChannel[]) {
    return normalizeConfigSnapshot({ config: { ...defaultConfig, channels } }).config;
}

const platformChannels = [
    systemChannel("CHANNEL_000003", "Replicate · 主账号", [
        ["openai/gpt-image-2", "GPT Image 2", "image"],
        ["minimax/music-2.5", "MiniMax Music 2.5 配乐", "audio"],
    ]),
    systemChannel("CHANNEL_000007", "Aigen Seedance · 主账号", [["seedance-2.5", "Seedance 2.5", "video"]]),
    systemChannel("CHANNEL_000009", "腾讯 TokenHub · 主账号", [["hy-image-v3.5-preview", "混元生图 3.5", "image"]]),
];

test("平台目录判定：渠道全归平台所有时走扁平型号列表", () => {
    const config = configWith(platformChannels);
    expect(isPlatformOnlyCatalog(config, selectableModelsByCapability(config))).toBe(true);
});

test("用户自己配过渠道时保留渠道分层，不能吞掉渠道名", () => {
    const mine = createModelChannel({ id: "mine", name: "我的中转", baseUrl: "https://example.com", models: ["gpt-image-2"], modelProfiles: [{ model: "gpt-image-2", displayName: "GPT Image 2", capability: "image" }] });
    const config = configWith([...platformChannels, mine]);
    expect(isPlatformOnlyCatalog(config, selectableModelsByCapability(config))).toBe(false);
});

test("空目录不算平台目录，避免在没有模型时误判成扁平形态", () => {
    const config = configWith([]);
    expect(isPlatformOnlyCatalog(config, [])).toBe(false);
});

test("扁平合并：同名型号并成一行，跨渠道的候选一个都不能丢", () => {
    // 同一个型号同时落在两个平台渠道：对用户是同一个型号，但两条路由都要保留。
    const a = systemChannel("CHANNEL_000003", "Replicate · 主账号", [["gpt-image-2", "GPT Image 2", "image"]]);
    const b = systemChannel("CHANNEL_000013", "另一个上游账号", [["gpt-image-2", "GPT Image 2", "image"], ["seedance-2.5", "Seedance 2.5", "video"]]);
    const config = configWith([a, b]);
    const models = selectableModelsByCapability(config);
    const groups = flattenModelGroupsByDisplayName(config, models);

    const gpt = groups.filter((group) => group.label === "GPT Image 2");
    expect(gpt.length).toBe(1);
    expect(gpt[0].models.length).toBe(2);
    // 合并只影响展示：每个候选仍解析回它原本的渠道，路由不受影响。
    expect(gpt[0].models.map((model) => resolveModelChannel(config, model).id).sort()).toEqual(["CHANNEL_000003", "CHANNEL_000013"]);
    expect(groups.reduce((total, group) => total + group.models.length, 0)).toBe(models.length);
});

test("扁平合并后不残留任何渠道名", () => {
    const config = configWith(platformChannels);
    const groups = flattenModelGroupsByDisplayName(config, selectableModelsByCapability(config));
    const labels = groups.map((group) => group.label).join("|");
    expect(labels).toContain("GPT Image 2");
    expect(labels).toContain("Seedance 2.5");
    for (const channelName of ["Replicate", "Aigen", "TokenHub", "主账号"]) {
        expect(labels).not.toContain(channelName);
    }
});

// Popover 只在展开时才挂载内容，SSR 拿不到菜单 DOM；这里沿用仓库里既有的
// 源码契约校验，钉住"平台目录走扁平列表、渠道名只出现在两级目录里"这个结构。
test("选择器在平台目录下渲染扁平型号列表，渠道名只留在两级目录分支", async () => {
    const picker = await Bun.file(new URL("../src/components/model-picker.tsx", import.meta.url)).text();
    const css = await Bun.file(new URL("../src/styles/globals.css", import.meta.url)).text();

    expect(picker).toContain("isPlatformOnlyCatalog(config, options)");
    expect(picker).toContain("flattenModelGroupsByDisplayName(config, options)");
    expect(picker).toContain("platformOnlyCatalog ? (\n                    renderModelOptions(flatModelGroups)");
    // 扁平分支直接铺模型行，不经过带 group.label 的渠道页头。
    expect(picker).toContain('platformOnlyCatalog ? "is-flat-list" : activeGroupKey === null ? "is-brand-list" : "is-model-list"');
    expect(picker).toContain("<strong>{group.label}</strong>");

    expect(css).toContain(".canvas-model-picker-menu.is-flat-list");
    expect(css).toContain(".canvas-model-picker-menu.is-flat-list .canvas-model-picker-options");
});

// 平台把同名型号的默认渠道换到成本更低的那条（fal 提到 Replicate 前面）时，
// 用户上次的渠道落点不能继续生效，否则默认渠道等于没换。
function sunburstChannels(order: "fal-first" | "replicate-first") {
    const replicate = systemChannel("CHANNEL_000003", "Replicate · 主账号", [["openai/gpt-image-2.5-sunburst", "GPT Image 2.5 Sunburst", "image"]]);
    const fal = systemChannel("CHANNEL_000012", "fal.ai · 主账号", [["openai/gpt-image-2.5/sunburst", "GPT Image 2.5 Sunburst", "image"]]);
    return order === "fal-first" ? [fal, replicate] : [replicate, fal];
}

const REPLICATE_SUNBURST = "CHANNEL_000003::openai/gpt-image-2.5-sunburst";
const FAL_SUNBURST = "CHANNEL_000012::openai/gpt-image-2.5/sunburst";

test("平台目录下同名型号按渠道顺序落点，已保存的旧渠道落点不再生效", () => {
    // 线上现在的顺序：fal（成本更低）在前。
    const config = configWith(sunburstChannels("fal-first"));
    expect(resolveCompatibleModel(config, REPLICATE_SUNBURST, { capability: "image" })).toBe(FAL_SUNBURST);
    expect(resolveCompatibleModel(config, FAL_SUNBURST, { capability: "image" })).toBe(FAL_SUNBURST);

    // 选择器点击同一行时的入参就是 preferred，也必须落到渠道顺序里的第一条。
    const group = flattenModelGroupsByDisplayName(config, selectableModelsByCapability(config, "image")).find((item) => item.label === "GPT Image 2.5 Sunburst");
    expect(group?.models.length).toBe(2);
    expect(compatibleModelInGroup(config, group!.models, { capability: "image" }, REPLICATE_SUNBURST)).toBe(FAL_SUNBURST);
});

test("渠道顺序反过来时落点跟着换，前端不额外记忆渠道", () => {
    const config = configWith(sunburstChannels("replicate-first"));
    expect(resolveCompatibleModel(config, FAL_SUNBURST, { capability: "image" })).toBe(REPLICATE_SUNBURST);
    expect(resolveCompatibleModel(config, REPLICATE_SUNBURST, { capability: "image" })).toBe(REPLICATE_SUNBURST);
});

test("用户自己的渠道不受影响，仍保留上次选中的那条", () => {
    const mine = createModelChannel({
        id: "mine",
        name: "我的中转",
        baseUrl: "https://example.com",
        models: ["gpt-image-2", "gpt-image-2-alt"],
        modelProfiles: [
            { model: "gpt-image-2", displayName: "GPT Image 2", capability: "image" },
            { model: "gpt-image-2-alt", displayName: "GPT Image 2", capability: "image" },
        ],
    });
    const config = configWith([mine, ...sunburstChannels("fal-first")]);
    const plain = encodeChannelModel("mine", "gpt-image-2");
    const alt = encodeChannelModel("mine", "gpt-image-2-alt");
    expect(compatibleModelInGroup(config, [plain, alt], { capability: "image" }, alt)).toBe(alt);
    expect(resolveCompatibleModel(config, alt, { capability: "image" })).toBe(alt);
});

// GPT Image 2 在两条渠道上的能力合同必须逐字段一致，否则合并成一行之后，
// 同一个型号会因为"当前渠道不支持这个比例"被从候选里剔掉，落点就悄悄变了。
// 数据来自上架脚本 scripts/seed-fal-image-models.py 的 capability_config()。
const GPT_IMAGE_2_RATIOS = ["1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4"];

function gptImage2Capability(quality: string[]) {
    return {
        version: 1,
        image: {
            references: { promptMaxChars: 32000, maxImages: 4, maxImageBytes: 20 * 1024 * 1024, maskSupported: false },
            size: { parameter: "aspect_ratio", values: GPT_IMAGE_2_RATIOS, default: "1:1", allowCustom: false },
            quality: { supported: true, values: quality, default: "low" },
            transparentBackground: { supported: false, default: false },
            responseFormat: { supported: false },
            outputFormat: { supported: false },
            maxOutputs: 10,
        },
    } as never;
}

function gptImage2Channel(id: string, name: string) {
    return createModelChannel({
        id,
        name,
        scope: "system",
        baseUrl: `/api/ai/system/${id}`,
        apiKey: "system",
        hasApiKey: true,
        models: ["openai/gpt-image-2"],
        modelProfiles: [
            {
                model: "openai/gpt-image-2",
                displayName: "GPT Image 2",
                capability: "image",
                capabilityConfig: gptImage2Capability(["low", "medium", "high"]),
            },
        ],
    });
}

const FAL_GPT_IMAGE_2 = encodeChannelModel("CHANNEL_000012", "openai/gpt-image-2");
const REPLICATE_GPT_IMAGE_2 = encodeChannelModel("CHANNEL_000003", "openai/gpt-image-2");

test("GPT Image 2 跨渠道合并成一行，候选按渠道顺序、默认落在 fal", () => {
    // 线上顺序：fal（成本更低）在前。两个渠道登记的上游模型名恰好相同，差别只在渠道。
    const config = configWith([gptImage2Channel("CHANNEL_000012", "fal.ai · 主账号"), gptImage2Channel("CHANNEL_000003", "Replicate · 主账号")]);
    const group = flattenModelGroupsByDisplayName(config, selectableModelsByCapability(config, "image")).find((item) => item.label === "GPT Image 2");
    expect(group?.models.length).toBe(2);
    expect(group!.models[0]).toBe(FAL_GPT_IMAGE_2);
    expect(resolveCompatibleModel(config, REPLICATE_GPT_IMAGE_2, { capability: "image" })).toBe(FAL_GPT_IMAGE_2);
    // 每个候选仍解析回自己的渠道，路由不受合并影响。
    expect((group!.models).map((model) => resolveModelChannel(config, model).id)).toEqual(["CHANNEL_000012", "CHANNEL_000003"]);
});

test("比例不在候选渠道的能力合同里时，该候选被剔除而不是硬发一个上游不认的值", () => {
    // 极端场景：平台把 fal 那条的比例收窄成一个更小的集合。合并行仍是一行，
    // 但这个比例下只剩 Replicate 能接，落点必须自动换过去。
    const narrowed = gptImage2Channel("CHANNEL_000012", "fal.ai · 主账号");
    narrowed.modelProfiles![0].capabilityConfig = gptImage2Capability(["low", "medium", "high"]);
    (narrowed.modelProfiles![0].capabilityConfig as { image: { size: { values: string[] } } }).image.size.values = ["1:1"];
    const config = configWith([narrowed, gptImage2Channel("CHANNEL_000003", "Replicate · 主账号")]);
    const group = flattenModelGroupsByDisplayName(config, selectableModelsByCapability(config, "image")).find((item) => item.label === "GPT Image 2");
    expect(compatibleModelInGroup(config, group!.models, { capability: "image", imageSize: "16:9" })).toBe(REPLICATE_GPT_IMAGE_2);
    expect(compatibleModelInGroup(config, group!.models, { capability: "image", imageSize: "1:1" })).toBe(FAL_GPT_IMAGE_2);
});
