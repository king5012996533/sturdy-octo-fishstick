import type { AgentMediaSettings } from "@/services/api/agent";
import { logicalModelIDForConfig, modelOptionName, resolveModelChannel, selectableModelsByCapability, type AiConfig } from "@/stores/use-config-store";

export type AgentImageApproval = AgentMediaSettings & { prompt: string; referenceNodeIds: string[] };

export function agentImageApproval(detail: Record<string, unknown>): AgentImageApproval | null {
    const call = detail.call as { function?: { name?: string; arguments?: unknown } } | undefined;
    if ((call?.function?.name || detail.toolName) !== "generate_media") return null;
    const raw = call?.function?.arguments ?? detail.arguments;
    try {
        const args = typeof raw === "string" ? JSON.parse(raw) : raw;
        if (!args || args.mode !== "image" || typeof args.size !== "string" || typeof args.prompt !== "string") return null;
        return {
            logicalModelId: typeof args.logicalModelId === "string" ? args.logicalModelId : "",
            channelId: typeof args.channelId === "string" ? args.channelId : "",
            channelModelKey: typeof args.channelModelKey === "string" ? args.channelModelKey : "",
            size: args.size,
            quality: typeof args.quality === "string" ? args.quality : "",
            prompt: args.prompt,
            referenceNodeIds: Array.isArray(args.referenceNodeIds) ? args.referenceNodeIds.filter((id: unknown): id is string => typeof id === "string") : [],
        };
    } catch {
        return null;
    }
}

export function agentApprovalModel(config: AiConfig, settings: AgentMediaSettings): string {
    return selectableModelsByCapability(config, "image").find((model) => {
        if (settings.logicalModelId) return logicalModelIDForConfig({ ...config, model }) === settings.logicalModelId;
        return resolveModelChannel(config, model).id === settings.channelId && modelOptionName(model) === settings.channelModelKey;
    }) || "";
}

export function agentApprovalMatchesSettings(argumentsValue: unknown, settings: AgentMediaSettings): boolean {
    const approved = agentImageApproval({ toolName: "generate_media", arguments: argumentsValue });
    return Boolean(approved && (["logicalModelId", "channelId", "channelModelKey", "size", "quality"] as const).every((key) => (approved[key] || "") === (settings[key] || "")));
}

export function agentApprovalModelSelection(config: AiConfig, model: string): Pick<AgentMediaSettings, "logicalModelId" | "channelId" | "channelModelKey"> {
    const logicalModelId = logicalModelIDForConfig({ ...config, model });
    if (logicalModelId) return { logicalModelId };
    const channel = resolveModelChannel(config, model);
    if (channel.scope !== "system") throw new Error("Agent 生成仅支持平台模型");
    return { channelId: channel.id, channelModelKey: modelOptionName(model) };
}

/**
 * 审批卡规格编辑的提交基准。
 *
 * 一次交互可能拆成多次提交：改比例会先提交尺寸、再提交画质。父级是 React 状态，
 * 每次提交都从 props 里读快照，同一个事件里的第二次调用读到的仍是点击前的那份，
 * 于是后一次会把前一次的选择覆盖回去（表现就是"点了比例没反应"）。
 * 这里维护一份同 tick 内同步推进的基准，保证多次提交按顺序落在最新设置上。
 */
export function createAgentApprovalSettingsDraft(initial: AgentMediaSettings) {
    let current = initial;
    return {
        current: () => current,
        // 父级状态是唯一展示来源：重新渲染后把基准拉回父级真值，
        // 避免提交被拒绝时基准和界面不一致。
        sync: (next: AgentMediaSettings) => {
            current = next;
        },
        commit: (patch: Partial<AgentMediaSettings>) => {
            current = { ...current, ...patch };
            return current;
        },
    };
}
