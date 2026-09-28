import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { mapSystemCatalogToChannels, systemChannelBaseUrl, type SystemCatalogPayload } from "../src/features/hosted-auth/system-models";
import {
    createModelChannel,
    defaultConfig,
    effectiveConfigForCustomChannels,
    modelOptionsFromChannels,
    normalizeConfigSnapshot,
    useConfigStore,
} from "../src/stores/use-config-store";

const payload: SystemCatalogPayload = {
    source: "system",
    models: [],
    channels: [
        {
            id: "platform-openai",
            name: "platform-openai",
            displayName: "平台图片",
            sortOrder: 3,
            models: [
                {
                    id: "cm-1",
                    modelKey: "gpt-image-1",
                    displayName: "GPT Image 1",
                    sortOrder: 1,
                    icon: "openai",
                    capability: "image",
                    protocol: "openai-image",
                    available: true,
                },
                {
                    id: "cm-2",
                    modelKey: "retired-model",
                    displayName: "已下线模型",
                    sortOrder: 2,
                    icon: "",
                    capability: "image",
                    protocol: "openai-image",
                    available: false,
                },
                {
                    id: "cm-3",
                    modelKey: "",
                    displayName: "缺模型键",
                    sortOrder: 3,
                    icon: "",
                    capability: "image",
                    protocol: "openai-image",
                    available: true,
                },
            ],
        },
        { id: "", name: "无 ID 渠道", displayName: "无 ID 渠道", sortOrder: 9, models: [] },
        { id: "empty-channel", name: "空渠道", displayName: "空渠道", sortOrder: 10, models: [] },
    ],
};

describe("hosted system model catalog", () => {
    test("系统渠道被投影成带转发地址与模型能力的渠道快照", () => {
        const channels = mapSystemCatalogToChannels(payload);
        expect(channels).toHaveLength(1);
        const [channel] = channels;
        expect(channel.id).toBe("platform-openai");
        expect(channel.name).toBe("平台图片");
        expect(channel.scope).toBe("system");
        // 浏览器侧 Base URL 必须是平台转发地址，绝不能是上游地址。
        expect(channel.baseUrl).toBe(systemChannelBaseUrl("platform-openai"));
        expect(channel.models).toEqual(["gpt-image-1"]);
        expect(channel.modelProfiles).toEqual([
            { model: "gpt-image-1", displayName: "GPT Image 1", icon: "openai", capability: "image", protocol: "openai-image" },
        ]);
    });

    test("前台目录与畸形条目都不产出渠道", () => {
        expect(mapSystemCatalogToChannels({ source: "frontend", models: [], channels: payload.channels })).toEqual([]);
        expect(mapSystemCatalogToChannels(null)).toEqual([]);
        expect(mapSystemCatalogToChannels({ source: "system", models: [], channels: [] })).toEqual([]);
    });

    test("未知能力回落到文本，避免模型从下拉里消失", () => {
        const channels = mapSystemCatalogToChannels({
            source: "system",
            models: [],
            channels: [
                {
                    id: "c1",
                    name: "c1",
                    displayName: "c1",
                    sortOrder: 0,
                    models: [{ id: "m", modelKey: "m", displayName: "m", sortOrder: 0, icon: "", capability: "unknown", protocol: "chat-completion", available: true }],
                },
            ],
        });
        expect(channels[0].modelProfiles?.[0].capability).toBe("text");
    });
});

describe("托管渠道与自建渠道的共存规则", () => {
    test("关闭自建渠道时只保留系统渠道，并且平台模型可选", () => {
        const systemChannels = mapSystemCatalogToChannels(payload);
        const userChannel = createModelChannel({ id: "mine", name: "我的渠道", apiKey: "sk-user", models: ["my-image"] });
        const merged = { ...defaultConfig, channels: [...systemChannels, userChannel] };

        const effective = effectiveConfigForCustomChannels(merged, false);
        expect(effective.channels.map((channel) => channel.id)).toEqual(["platform-openai"]);
        expect(modelOptionsFromChannels(effective.channels)).toContain("platform-openai::gpt-image-1");
    });

    test("打开自建渠道时系统渠道仍在，用户渠道不被吞掉", () => {
        const systemChannels = mapSystemCatalogToChannels(payload);
        const userChannel = createModelChannel({ id: "mine", name: "我的渠道", apiKey: "sk-user", models: ["my-image"] });
        const merged = { ...defaultConfig, channels: [...systemChannels, userChannel] };

        const effective = effectiveConfigForCustomChannels(merged, true);
        expect(effective.channels.map((channel) => channel.id)).toEqual(["platform-openai", "mine"]);
    });

    test("用户没做任何配置时默认模型自动落到平台模型", () => {
        // 撤掉前端模型配置页之后，默认模型必须由目录自动补齐，否则用户进画布无模型可选。
        const merged = normalizeConfigSnapshot({
            config: { ...defaultConfig, imageModel: "", videoModel: "", textModel: "", channels: mapSystemCatalogToChannels(payload) },
        }).config;
        expect(merged.imageModel).toBe("platform-openai::gpt-image-1");
        expect(merged.imageModels).toContain("platform-openai::gpt-image-1");
    });

    test("mergeSystemChannels 幂等：重复合并不会堆叠系统渠道", () => {
        const systemChannels = mapSystemCatalogToChannels(payload);
        const userChannel = createModelChannel({ id: "mine", name: "我的渠道", apiKey: "sk-user", models: ["my-image"] });
        useConfigStore.getState().replaceConfig({ ...defaultConfig, channels: [userChannel] });
        useConfigStore.getState().mergeSystemChannels(systemChannels);
        useConfigStore.getState().mergeSystemChannels(systemChannels);

        expect(useConfigStore.getState().config.channels.map((channel) => channel.id)).toEqual(["platform-openai", "mine"]);
    });
});

describe("托管形态下模型配置入口一律不出现", () => {
    const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

    test("侧栏入口挂在开关上，本地构建仍保留", () => {
        const nav = read("../src/components/layout/workspace-sidebar-nav.tsx");
        expect(nav).toContain("features.customChannelsEnabled");
        expect(nav).toContain('title: "模型配置"');
    });

    test("顶栏与命令面板同样按开关过滤", () => {
        // 入口必须走 userChannelConfigVisible：托管产物里它恒为 false，
        // 只看功能开关会让 SaaS 前台在开关被打开后重新长出"模型配置"。
        expect(read("../src/components/layout/workspace-top-bar.tsx")).toContain("userChannelConfigVisible(customChannelsEnabled)");
        expect(read("../src/components/layout/workspace-command-palette.tsx")).toContain("userChannelConfigVisible(");
    });

    test("平台没有该能力的模型时不把用户送去设置页，而是就地禁用入口", () => {
        const nav = read("../src/lib/settings-navigation.ts");
        // 托管形态下设置页只剩一页说明，任何"去配置模型"的引导跳过去都是死胡同。
        expect(nav).toContain("if (!canOpenChannelSettings()) return;");
        const prompt = read("../src/components/canvas/canvas-node-prompt-panel.tsx");
        expect(prompt).toContain("const hasUsableModel = selectableModelsByCapability(config, mode).length > 0;");
        expect(prompt).toContain("disabled={isRunning || isSubmitDisabled}");
        // 没有可选模型时选择器自己也不该能点开，否则又变成点了没反应。
        expect(read("../src/components/model-picker.tsx")).toContain("disabled={!options.length}");
    });

    test("托管形态的设置页换成账户页，不再渲染渠道表单", () => {
        const page = read("../src/pages/settings/index.tsx");
        // 构建开关与功能开关都要算进来，托管实例上功能开关只剩接口与计费语义。
        expect(page).toContain("__BEEFTV_HOSTED_AUTH__ || !customChannelsEnabled");
        // 托管分支必须落在账户页而不是渠道表单：用户在这里能看到自己的账号、用量与
        // 协议留痕，"去配置模型"的引导才不会跳进一个空配置页。
        expect(page).toContain("<AccountOverviewPane />");
        // 账户分支必须出现在渠道面板之前，否则说明页会被渲染成配置表单。
        expect(page.indexOf("<AccountOverviewPane />")).toBeLessThan(page.indexOf("const panes: Record<ConfigSectionKey, ReactNode>"));
    });
});

describe("托管模型同步的接缝", () => {
    test("登录门挂载平台模型同步，目录走托管专属路径", () => {
        const gate = readFileSync(new URL("../src/features/hosted-auth/gate.tsx", import.meta.url), "utf8");
        expect(gate).toContain("useHostedSystemModels");
        const module = readFileSync(new URL("../src/features/hosted-auth/system-models.ts", import.meta.url), "utf8");
        expect(module).toContain('http.get<SystemCatalogPayload>("/model-catalog")');
    });
});
