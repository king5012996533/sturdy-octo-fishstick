import { afterAll, beforeEach, describe, expect, it } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { join } from "node:path";

// 复用 agent-api-reliability 的做法：把存储换成内存 Map，只隔离依赖，不改被测逻辑。
const dir = mkdtempSync(join(import.meta.dir, ".agent-history-"));
const root = new URL("../src/", import.meta.url);
const storagePath = join(dir, "storage.ts");
writeFileSync(
    storagePath,
    `
export const data = new Map();
const scoped = (name, scope) => \`\${name}:user:\${scope ?? "test-user"}\`;
export const localForageStorageForScope = (scope) => ({
    getItem: async (name) => data.get(scoped(name, scope)) ?? null,
    setItem: async (name, value) => { data.set(scoped(name, scope), value); },
    removeItem: async (name) => { data.delete(scoped(name, scope)); },
});
export const listScopedStorageNames = async (prefix, scope = "test-user") => {
    const suffix = \`:user:\${scope}\`;
    return Array.from(data.keys()).filter((key) => key.endsWith(suffix)).map((key) => key.slice(0, -suffix.length)).filter((name) => name.startsWith(prefix));
};
`,
);
writeFileSync(join(dir, "scope.ts"), 'export const getActiveUserScope = () => "test-user";');
writeFileSync(
    join(dir, "conversations.ts"),
    readFileSync(new URL("services/cloud-agent-conversations.ts", root), "utf8")
        .replace('"@/lib/localforage-storage"', JSON.stringify(pathToFileURL(storagePath).href))
        .replace('"@/lib/user-scope"', JSON.stringify(pathToFileURL(join(dir, "scope.ts")).href))
        .replace('"@/lib/markdown-plain-text"', JSON.stringify(pathToFileURL(fileURLToPath(new URL("lib/markdown-plain-text.ts", root))).href)),
);
const conversations: typeof import("../src/services/cloud-agent-conversations") = await import(join(dir, "conversations.ts"));
const storage = await import(storagePath);
afterAll(() => rmSync(dir, { recursive: true, force: true }));

const CANVAS = "canvas-1";
const documentKey = `cloud-agent-conversations-v1:${encodeURIComponent(CANVAS)}:user:test-user`;
const pendingKey = (conversationId: string) => `cloud-agent-conversations-v1:pending:${encodeURIComponent(CANVAS)}:${encodeURIComponent(conversationId)}:user:test-user`;
const conversation = (overrides: Record<string, unknown>) => ({
    id: "c1",
    title: "第一轮",
    messages: [{ id: "m1", role: "user", text: "画一只猫" }],
    run: null,
    permissionMode: "request_approval",
    createdAt: "2026-09-29T00:00:00.000Z",
    updatedAt: "2026-09-29T00:00:00.000Z",
    ...overrides,
});

beforeEach(() => storage.data.clear());

describe("cloud Agent conversation history repair", () => {
    it("keeps usable conversations when one record is invalid", async () => {
        storage.data.set(
            documentKey,
            JSON.stringify({
                version: 1,
                activeId: "gone",
                conversations: [conversation({ id: "good" }), conversation({ id: "bad-mode", permissionMode: "full_access" }), { title: "缺 id" }],
            }),
        );

        const document = await conversations.loadCloudAgentConversations(CANVAS);

        expect(document.conversations.map((item) => item.id)).toEqual(["good", "bad-mode"]);
        expect(document.conversations[1].permissionMode).toBe("request_approval");
        // activeId 指向已被丢弃的记录时退回第一条可用记录，避免面板停在空对话。
        expect(document.activeId).toBe("good");
    });

    it("still rejects a document that cannot be parsed at all", async () => {
        storage.data.set(documentKey, "{ 这不是 JSON");
        await expect(conversations.loadCloudAgentConversations(CANVAS)).rejects.toThrow("Agent 对话历史已损坏");
    });

    it("quarantines an unreadable snapshot and rebuilds from pending submissions", async () => {
        storage.data.set(documentKey, "{{{ 坏掉的历史 ");
        storage.data.set(
            pendingKey("c-pending"),
            JSON.stringify({
                fingerprint: "fp",
                key: "3f2a4b6c-1111-2222-3333-444455556666",
                request: { conversationId: "c-pending", canvasId: CANVAS, prompt: "重试这一条", idempotencyKey: "3f2a4b6c-1111-2222-3333-444455556666" },
            }),
        );

        const salvaged = await conversations.salvageCloudAgentConversations(CANVAS);

        expect(salvaged.backupKey).toContain(":corrupt:");
        expect(storage.data.get(documentKey)).toBeDefined();
        expect(storage.data.get(`${salvaged.backupKey!}:user:test-user`)).toBe("{{{ 坏掉的历史 ");
        expect(salvaged.activeId).toBe("c-pending");
        expect(salvaged.recovered.map((item) => item.id)).toEqual(["c-pending"]);
        // 重建后的文档必须能立刻读回来，否则面板会再次落到损坏分支。
        const reloaded = await conversations.loadCloudAgentConversations(CANVAS);
        expect(reloaded.activeId).toBe("c-pending");
        const pending = await conversations.loadCloudAgentPendingSubmission(CANVAS, "c-pending");
        expect(pending?.request?.prompt).toBe("重试这一条");
    });

    it("rebuilds an empty conversation set when nothing is pending", async () => {
        storage.data.set(documentKey, "坏值");
        const salvaged = await conversations.salvageCloudAgentConversations(CANVAS);
        expect(salvaged.recovered).toEqual([]);
        expect(salvaged.activeId).toBeNull();
        expect(await conversations.loadCloudAgentConversations(CANVAS)).toEqual({ version: 1, activeId: null, conversations: [] });
    });
});
