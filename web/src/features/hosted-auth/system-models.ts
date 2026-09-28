import { useEffect } from "react";

import type { ModelCapabilityConfig } from "@/lib/model-capabilities";
import { normalizeModelProtocol } from "@/lib/model-protocols";
import { http } from "@/services/api/request";
import { createModelChannel, useConfigStore, type ModelCapability, type ModelChannel } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";

/**
 * 托管模型目录（后端 `/model-catalog`，`source=system`）。
 *
 * 这是脱敏读模型：只有用户可见的渠道名、模型名与能力，没有 Base URL 与密钥。
 * 平台渠道的执行凭证留在服务端，前端拿到的只是"可以选什么"。
 */
export type SystemCatalogModel = {
    id: string;
    modelKey: string;
    displayName: string;
    sortOrder: number;
    icon: string;
    capability: string;
    protocol: string;
    capabilityConfig?: ModelCapabilityConfig;
    available: boolean;
};

export type SystemCatalogChannel = {
    id: string;
    name: string;
    displayName: string;
    sortOrder: number;
    models: SystemCatalogModel[];
};

export type SystemCatalogPayload = {
    source: string;
    models: unknown[];
    channels: SystemCatalogChannel[];
};

const SYSTEM_CHANNEL_CAPABILITIES: ModelCapability[] = ["image", "video", "text", "audio"];

export function getSystemModelCatalog() {
    return http.get<SystemCatalogPayload>("/model-catalog");
}

/**
 * 系统渠道在浏览器侧的 Base URL 是平台转发地址，不是上游地址。
 *
 * 请求经 `/api/ai/system/<id>/*` 交给后端注入平台凭证：前端全程不接触密钥，
 * 因此这里既不能填真实上游地址，也不能带 apiKey。
 */
export function systemChannelBaseUrl(channelId: string) {
    return `/api/ai/system/${channelId}`;
}

/**
 * 把托管目录投影成渠道快照。
 *
 * 系统渠道的模型必须有 `modelProfiles` 条目才会出现在模型下拉里
 * （见 use-config-store 的 hasSystemModelProfile），所以能力与协议必须一起带上；
 * 缺模型键或标记 unavailable 的条目直接丢弃，避免选完才知道模型不可用。
 */
export function mapSystemCatalogToChannels(payload: SystemCatalogPayload | null | undefined): ModelChannel[] {
    if (!payload || payload.source !== "system" || !Array.isArray(payload.channels)) return [];
    const channels: ModelChannel[] = [];
    payload.channels.forEach((entry, index) => {
        const id = typeof entry?.id === "string" ? entry.id.trim() : "";
        if (!id) return;
        const models = (Array.isArray(entry.models) ? entry.models : []).filter(
            (model) => typeof model?.modelKey === "string" && model.modelKey.trim() !== "" && model.available !== false,
        );
        if (!models.length) return;
        channels.push(
            createModelChannel({
                id,
                name: (entry.displayName || entry.name || `系统渠道 ${index + 1}`).trim(),
                sortOrder: Number.isFinite(entry.sortOrder) ? entry.sortOrder : index,
                baseUrl: systemChannelBaseUrl(id),
                apiKey: "system",
                hasApiKey: true,
                scope: "system",
                enabled: true,
                models: models.map((model) => model.modelKey),
                interfaceType: normalizeModelProtocol(models[0].protocol),
                modelProfiles: models.map((model) => ({
                    model: model.modelKey,
                    displayName: model.displayName,
                    icon: model.icon,
                    capability: SYSTEM_CHANNEL_CAPABILITIES.includes(model.capability as ModelCapability)
                        ? (model.capability as ModelCapability)
                        : "text",
                    protocol: normalizeModelProtocol(model.protocol),
                    capabilityConfig: model.capabilityConfig,
                })),
            }),
        );
    });
    return channels;
}

let catalogSnapshot: ModelChannel[] = [];

function systemChannelSignature(channels: ModelChannel[]) {
    return channels
        .filter((channel) => channel.scope === "system")
        .map((channel) => {
            // 签名必须带上能力合同：平台调整模型的参数合同（比例、数量、档位）时，
            // 模型清单没变，只按 id+模型名比较会让浏览器一直用旧的合同渲染设置面板。
            const profiles = (channel.modelProfiles || []).map((profile) => [profile.model, profile.capability, profile.protocol, profile.capabilityConfig ?? null]);
            return `${channel.id}::${channel.models.join(",")}::${JSON.stringify(profiles)}`;
        })
        .sort()
        .join("|");
}

/**
 * 合并系统渠道快照。
 *
 * 本地水合契约会清掉 scope=system 的渠道，托管水合又可能晚于目录到达，
 * 因此这里只在签名不一致时写入：既补回被清掉的渠道，也不会自激成写循环。
 */
function applyCatalogSnapshot() {
    if (!catalogSnapshot.length) return;
    const current = useConfigStore.getState().config.channels;
    if (systemChannelSignature(current) === systemChannelSignature(catalogSnapshot)) return;
    useConfigStore.getState().mergeSystemChannels(catalogSnapshot);
}

export function resetSystemModelCatalog() {
    catalogSnapshot = [];
}

/**
 * 托管形态下的平台模型同步。
 *
 * 只在「已登录 + 已水合 + 平台关闭自建渠道」时生效。本地/桌面形态的
 * customChannelsEnabled 默认是 true，这个 hook 完全空转；目录拉取失败时保持
 * 空目录而不是回落成自建渠道，否则托管部署会静默退化成用户直连上游。
 */
export function useHostedSystemModels() {
    const authenticated = useUserStore((state) => Boolean(state.user));
    const hydrated = useUserStore((state) => state.hydrated);
    const customChannelsEnabled = useUserStore((state) => state.features.customChannelsEnabled);
    const active = authenticated && hydrated && !customChannelsEnabled;

    useEffect(() => {
        if (!active) return;
        let cancelled = false;
        void getSystemModelCatalog()
            .then((payload) => {
                if (cancelled) return;
                catalogSnapshot = mapSystemCatalogToChannels(payload);
                applyCatalogSnapshot();
            })
            .catch((error) => {
                if (!cancelled) console.warn("平台模型目录加载失败", error);
            });
        return () => {
            cancelled = true;
            resetSystemModelCatalog();
        };
    }, [active]);

    useEffect(() => {
        if (!active) return;
        return useConfigStore.subscribe(() => applyCatalogSnapshot());
    }, [active]);
}
