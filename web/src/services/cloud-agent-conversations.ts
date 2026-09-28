import { listScopedStorageNames, localForageStorageForScope } from "@/lib/localforage-storage";
import { markdownPlainText } from "@/lib/markdown-plain-text";
import { getActiveUserScope } from "@/lib/user-scope";
import type { AgentPermissionMode, AgentRun } from "@/services/api/agent";

export type CloudAgentConversationMessage = {
    id: string;
    role: "user" | "assistant" | "system" | "tool" | "error";
    title?: string;
    text: string;
    meta?: string;
    detail?: unknown;
    attachments?: Array<{ id: string; name: string; url: string }>;
};

export type CloudAgentConversation = {
    id: string;
    title: string;
    messages: CloudAgentConversationMessage[];
    run: AgentRun | null;
    model?: string;
    permissionMode: AgentPermissionMode;
    skillIds?: string[];
    createdAt: string;
    updatedAt: string;
};

type CloudAgentConversationDocument = {
    version: 1;
    activeId: string | null;
    conversations: CloudAgentConversation[];
};

export type CloudAgentPendingSubmission = {
    fingerprint: string;
    key: string;
    request?: import("@/services/api/agent").CreateAgentRunInput;
    parentRunId?: string;
    messageId?: string;
};

const CLOUD_AGENT_CONVERSATIONS_KEY = "cloud-agent-conversations-v1";

export async function loadCloudAgentConversations(canvasId: string): Promise<CloudAgentConversationDocument> {
    if (!canvasId) throw new Error("缺少画布 ID，无法读取 Agent 对话");
    const value = await localForageStorageForScope(getActiveUserScope()).getItem(storageKey(canvasId));
    if (!value) return emptyDocument();

    let parsed: unknown;
    try {
        parsed = JSON.parse(value);
    } catch {
        throw new Error("Agent 对话历史已损坏");
    }
    if (!parsed || typeof parsed !== "object") throw new Error("Agent 对话历史已损坏");
    const document = parsed as Partial<CloudAgentConversationDocument>;
    if (document.version !== 1 || !Array.isArray(document.conversations)) throw new Error("Agent 对话历史格式无效");
    // 单条对话不合法（典型是旧版本写入的 permissionMode）不能拖垮整份历史：
    // 整份拒收会让输入框永久停在“已暂停发送”，而修一下就能继续用。
    const conversations = document.conversations.map(repairConversation).filter((conversation): conversation is CloudAgentConversation => conversation !== null);
    const activeId = typeof document.activeId === "string" && conversations.some((conversation) => conversation.id === document.activeId) ? document.activeId : (conversations[0]?.id ?? null);
    return { version: 1, activeId, conversations };
}

/** 读不出本地对话快照时，隔离原值并按幂等提交记录重建，避免输入框被永久锁死。 */
export async function salvageCloudAgentConversations(canvasId: string): Promise<{ backupKey: string | null; recovered: CloudAgentConversation[]; activeId: string | null }> {
    const storage = localForageStorageForScope(getActiveUserScope());
    const raw = await storage.getItem(storageKey(canvasId));
    let backupKey: string | null = null;
    if (raw) {
        backupKey = `${storageKey(canvasId)}:corrupt:${Date.now()}`;
        await storage.setItem(backupKey, raw);
        await storage.removeItem(storageKey(canvasId));
    }
    const prefix = pendingStorageKey(canvasId, "");
    const pendingNames = await listScopedStorageNames(prefix);
    const now = new Date().toISOString();
    const recovered = pendingNames
        .map((name) => decodeURIComponent(name.slice(prefix.length)))
        .filter((conversationId) => conversationId.length > 0)
        .map((conversationId): CloudAgentConversation => ({
            id: conversationId,
            title: "新对话",
            messages: [],
            run: null,
            permissionMode: "request_approval",
            createdAt: now,
            updatedAt: now,
        }));
    if (recovered.length) await saveCloudAgentConversations(canvasId, recovered[0].id, recovered);
    return { backupKey, recovered, activeId: recovered[0]?.id ?? null };
}

export async function saveCloudAgentConversations(canvasId: string, activeId: string | null, conversations: CloudAgentConversation[]) {
    if (!canvasId) throw new Error("缺少画布 ID，无法保存 Agent 对话");
    const document: CloudAgentConversationDocument = { version: 1, activeId, conversations };
    await localForageStorageForScope(getActiveUserScope()).setItem(storageKey(canvasId), JSON.stringify(document));
}

// Keep the retry key outside the chat snapshot. If the response is lost, a
// page refresh can safely repeat the same request without creating another
// billed Agent turn.
export async function loadCloudAgentPendingSubmission(canvasId: string, conversationId: string) {
    if (!canvasId || !conversationId) return null;
    const value = await localForageStorageForScope(getActiveUserScope()).getItem(pendingStorageKey(canvasId, conversationId));
    if (value === null || value === undefined) return null;
    // A corrupt recovery record is not proof that the previous POST never ran.
    // Fail closed rather than discarding its identity and risking another charge.
    try {
        const parsed = JSON.parse(value) as Partial<CloudAgentPendingSubmission> | null;
        if (!parsed || typeof parsed.fingerprint !== "string" || typeof parsed.key !== "string" || parsed.key.length < 8) throw new Error("invalid identity");
        if (parsed.request !== undefined && (!parsed.request || parsed.request.idempotencyKey !== parsed.key || parsed.request.canvasId !== canvasId || typeof parsed.request.prompt !== "string")) throw new Error("invalid request");
        if (parsed.parentRunId !== undefined && typeof parsed.parentRunId !== "string") throw new Error("invalid parent");
        if (parsed.messageId !== undefined && typeof parsed.messageId !== "string") throw new Error("invalid message");
        return { fingerprint: parsed.fingerprint, key: parsed.key, request: parsed.request, parentRunId: parsed.parentRunId, messageId: parsed.messageId } satisfies CloudAgentPendingSubmission;
    } catch {
        throw new Error("待确认请求记录损坏，已暂停本对话发送；请先在任务中心核对原运行，不要直接重复生成");
    }
}

export async function saveCloudAgentPendingSubmission(canvasId: string, conversationId: string, pending: CloudAgentPendingSubmission) {
    if (!canvasId || !conversationId) throw new Error("缺少 Agent 对话范围，无法保存幂等提交");
    await localForageStorageForScope(getActiveUserScope()).setItem(pendingStorageKey(canvasId, conversationId), JSON.stringify(pending));
}

export async function clearCloudAgentPendingSubmission(canvasId: string, conversationId: string) {
    if (!canvasId || !conversationId) return;
    await localForageStorageForScope(getActiveUserScope()).removeItem(pendingStorageKey(canvasId, conversationId));
}

export function cloudAgentConversationTitle(messages: CloudAgentConversationMessage[]) {
    const firstPrompt = displayConversationTitle(messages.find((message) => message.role === "user")?.text || "");
    return firstPrompt.length > 28 ? `${firstPrompt.slice(0, 28)}...` : firstPrompt;
}

function displayConversationTitle(value: string) {
    return markdownPlainText(value.replace(/@\[skill:[^\]]+\]/gu, "技能包")) || "新对话";
}

function storageKey(canvasId: string) {
    return `${CLOUD_AGENT_CONVERSATIONS_KEY}:${encodeURIComponent(canvasId)}`;
}

function pendingStorageKey(canvasId: string, conversationId: string) {
    return `${CLOUD_AGENT_CONVERSATIONS_KEY}:pending:${encodeURIComponent(canvasId)}:${encodeURIComponent(conversationId)}`;
}

function emptyDocument(): CloudAgentConversationDocument {
    return { version: 1, activeId: null, conversations: [] };
}

const PERMISSION_MODES: AgentPermissionMode[] = ["read_only", "auto", "request_approval"];

// 只修不丢：缺字段就补默认值，权限档位不认识就退回「请求审批」，
// 只有当一条记录连 id 都没有、无法与幂等提交记录对应时才丢弃。
function repairConversation(value: unknown): CloudAgentConversation | null {
    if (!value || typeof value !== "object") return null;
    const candidate = value as Partial<CloudAgentConversation>;
    if (typeof candidate.id !== "string" || !candidate.id) return null;
    const messages = Array.isArray(candidate.messages) ? (candidate.messages as CloudAgentConversationMessage[]) : [];
    const now = new Date().toISOString();
    return {
        id: candidate.id,
        title: typeof candidate.title === "string" && candidate.title ? candidate.title : cloudAgentConversationTitle(messages),
        messages,
        run: candidate.run ?? null,
        model: typeof candidate.model === "string" && candidate.model ? candidate.model : undefined,
        permissionMode: PERMISSION_MODES.includes(candidate.permissionMode as AgentPermissionMode) ? (candidate.permissionMode as AgentPermissionMode) : "request_approval",
        skillIds: Array.isArray(candidate.skillIds) ? candidate.skillIds.filter((id): id is string => typeof id === "string") : undefined,
        createdAt: typeof candidate.createdAt === "string" ? candidate.createdAt : now,
        updatedAt: typeof candidate.updatedAt === "string" ? candidate.updatedAt : now,
    };
}
