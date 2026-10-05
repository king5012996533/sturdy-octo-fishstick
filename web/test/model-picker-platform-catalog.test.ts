import { expect, test } from "bun:test";

import { flattenModelGroupsByDisplayName, isPlatformOnlyCatalog } from "../src/lib/model-selection";
import { createModelChannel, defaultConfig, normalizeConfigSnapshot, resolveModelChannel, selectableModelsByCapability, type ModelChannel } from "../src/stores/use-config-store";

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
