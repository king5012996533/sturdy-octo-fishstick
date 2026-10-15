import { afterAll, beforeEach, describe, expect, it } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const dir = mkdtempSync(join(import.meta.dir, ".canvas-write-queue-"));
const repositorySource = readFileSync(new URL("../src/services/local-workspace-repository.ts", import.meta.url), "utf8");
const syncSource = readFileSync(new URL("../src/services/local-workspace-sync.ts", import.meta.url), "utf8");

const storePath = join(dir, "store.ts");
const historyPath = join(dir, "history.ts");
const requestPath = join(dir, "request.ts");
const assetsPath = join(dir, "assets.ts");
const resourcesPath = join(dir, "resources.ts");
const modePath = join(dir, "mode.ts");
const rebasePath = join(dir, "rebase.ts");
const cloudMediaPath = join(dir, "cloud-media.ts");
const hostedPath = join(dir, "hosted.ts");
const agentPatchPath = join(dir, "agent-patch.ts");
const assetRepairPath = join(dir, "asset-repair.ts");
const repositoryPath = join(dir, "repository.ts");
const syncPath = join(dir, "sync.ts");

writeFileSync(
    storePath,
    `
export type CanvasProject = any;
export let projects: any[] = [];
export const resetProjects = (next: any[]) => { projects = next; };
const getState = () => ({
  projects,
  openProject: (id: string) => projects.find((project) => project.id === id) ?? null,
  updateProject: (id: string, patch: Record<string, unknown>) => {
    projects = projects.map((project) => project.id === id ? { ...project, ...patch } : project);
  },
});
export const useCanvasStore = {
  getState,
  setState: (update: any) => {
    const patch = typeof update === "function" ? update(getState()) : update;
    if (patch.projects) projects = patch.projects;
  },
};
export const flushCanvasStorePersistence = async () => {};
`,
);
writeFileSync(historyPath, "export const useCanvasHistoryStore = { getState: () => ({ recordDeletedProjects: () => {} }) };\n");
writeFileSync(
    requestPath,
    `
export class ApiError extends Error {
  status: number;
  reason?: string;
  constructor(status: number, message: string, reason?: string) { super(message); this.status = status; this.reason = reason; }
}
export let calls: Array<{ path: string; revision: number }> = [];
export let serverRevision = 0;
export let writesInFlight = 0;
export let maxWritesInFlight = 0;
export const resetServer = (revision: number) => { calls = []; serverRevision = revision; writesInFlight = 0; maxWritesInFlight = 0; };
// 服务端乐观锁：请求里的 revision 必须等于库里当前值。并发到达且版本相同的那次必然 409。
export const http = {
  put: async (path: string, body: any) => {
    const incoming = body.project.revision ?? 0;
    writesInFlight += 1;
    if (writesInFlight > maxWritesInFlight) maxWritesInFlight = writesInFlight;
    try {
      if (incoming !== serverRevision) throw new ApiError(409, "画布已有更新，请重新加载后再保存", "canvas_revision_conflict");
      await new Promise((resolve) => setTimeout(resolve, 5));
      calls.push({ path, revision: incoming });
      serverRevision += 1;
      return { project: { ...body.project, revision: serverRevision } };
    } finally {
      writesInFlight -= 1;
    }
  },
  get: async () => { throw new Error("unexpected GET"); },
  delete: async () => {},
};
`,
);
writeFileSync(assetsPath, "export const useAssetStore = { getState: () => ({ assets: [] }) };\nexport const flushAssetStorePersistence = async () => {};\n");
writeFileSync(resourcesPath, 'export const resourceIdFromStorageKey = () => "";\n');
writeFileSync(modePath, "export let localMode = true;\nexport const setLocalMode = (next: boolean) => { localMode = next; };\nexport const isLocalWorkspaceMode = () => localMode;\n");
writeFileSync(
    rebasePath,
    `
export class CanvasCloudAuthoritativeError extends Error {}
export const isCanvasCloudAuthoritativeError = (error: any) => error instanceof CanvasCloudAuthoritativeError;
export const adoptRemoteCanvasProject = async () => ({ adopted: false, draftSaved: false });
export const isCanvasRevisionConflict = (error: any) => error?.status === 409 && error?.reason === "canvas_revision_conflict";
`,
);
writeFileSync(cloudMediaPath, "export const withCloudMediaContentOnProject = (project: any) => project;\n");
writeFileSync(hostedPath, "export const isHostedBuild = () => false;\n");
writeFileSync(agentPatchPath, "export const applyAgentCanvasPatch = (project: any) => project;\n");
writeFileSync(assetRepairPath, "export const rebindInconsistentCanvasAssets = () => ({ project: null, rebound: 0 });\n");

const repositoryRewrites: Array<[string, string]> = [
    ['"@/stores/canvas/use-canvas-store"', JSON.stringify(pathToFileURL(storePath).href)],
    ['"@/stores/canvas/use-canvas-history-store"', JSON.stringify(pathToFileURL(historyPath).href)],
    ['"@/services/api/request"', JSON.stringify(pathToFileURL(requestPath).href)],
    ['"@/services/api/resources"', JSON.stringify(pathToFileURL(resourcesPath).href)],
    ['"@/stores/use-asset-store"', JSON.stringify(pathToFileURL(assetsPath).href)],
    ['"@/services/workspace-mode"', JSON.stringify(pathToFileURL(modePath).href)],
    ['"@/services/canvas-revision-rebase"', JSON.stringify(pathToFileURL(rebasePath).href)],
    ['"@/lib/canvas/canvas-cloud-media"', JSON.stringify(pathToFileURL(cloudMediaPath).href)],
    ['"@/lib/hosted-build"', JSON.stringify(pathToFileURL(hostedPath).href)],
];
const applyRewrites = (source: string, rewrites: Array<[string, string]>) => rewrites.reduce((next, [from, to]) => next.split(from).join(to), source);

writeFileSync(repositoryPath, applyRewrites(repositorySource, repositoryRewrites));
writeFileSync(
    syncPath,
    applyRewrites(syncSource, [
        ['"@/lib/canvas/agent-canvas-patch"', JSON.stringify(pathToFileURL(agentPatchPath).href)],
        ['"@/services/canvas-asset-repair"', JSON.stringify(pathToFileURL(assetRepairPath).href)],
        ['"@/services/local-workspace-repository"', JSON.stringify(pathToFileURL(repositoryPath).href)],
        ['"@/stores/use-asset-store"', JSON.stringify(pathToFileURL(assetsPath).href)],
        ['"@/stores/canvas/use-canvas-store"', JSON.stringify(pathToFileURL(storePath).href)],
        ['"@/services/api/request"', JSON.stringify(pathToFileURL(requestPath).href)],
        ['"@/services/workspace-mode"', JSON.stringify(pathToFileURL(modePath).href)],
    ]),
);

const repository: typeof import("../src/services/local-workspace-repository") = await import(repositoryPath);
const sync: typeof import("../src/services/local-workspace-sync") = await import(syncPath);
const store = await import(storePath);
const request = await import(requestPath);

const BASE_REVISION = 7;
const project = {
    id: "canvas-a",
    title: "并写画布",
    createdAt: "2026-10-16T00:00:00.000Z",
    updatedAt: "2026-10-16T00:00:00.000Z",
    revision: BASE_REVISION,
    nodes: [],
    connections: [],
    chatSessions: [],
    activeChatId: null,
    backgroundMode: "grid",
    showImageInfo: false,
    viewport: { x: 0, y: 0, k: 1 },
    directorScenes: [],
};

beforeEach(() => {
    store.resetProjects([{ ...project }]);
    request.resetServer(BASE_REVISION);
});
afterAll(() => rmSync(dir, { recursive: true, force: true }));

describe("canvas backend write queue", () => {
    // 批量抠图会为每张图各写一次画布：两个 worker 同时收工，各自带着同一个 revision 发出，
    // 后到的那次必然 409，而 409 会被上层翻译成「模型不接受当前参数」的生成失败。
    it("生成结果并发落库时，第二次写入带的是第一次刚推进的版本", async () => {
        const results = await Promise.all([
            sync.syncLocalCanvasSnapshotForAgent("canvas-a", { nodes: [{ id: "node-a" }] as never }),
            sync.syncLocalCanvasSnapshotForAgent("canvas-a", { nodes: [{ id: "node-b" }] as never }),
        ]);

        expect(request.calls.map((call) => call.revision)).toEqual([BASE_REVISION, BASE_REVISION + 1]);
        expect(request.maxWritesInFlight).toBe(1);
        expect(results.map((saved) => saved.revision)).toEqual([BASE_REVISION + 1, BASE_REVISION + 2]);
        expect(store.projects[0].revision).toBe(BASE_REVISION + 2);
    });

    it("自动保存和生成落库共用同一条队列，不会互相把版本写旧", async () => {
        await Promise.all([
            repository.syncLocalCanvasProjectToBackend("canvas-a"),
            sync.syncLocalCanvasSnapshotForAgent("canvas-a", { nodes: [{ id: "node-a" }] as never }),
        ]);

        expect(request.calls.map((call) => call.revision)).toEqual([BASE_REVISION, BASE_REVISION + 1]);
        expect(request.maxWritesInFlight).toBe(1);
        expect(request.serverRevision).toBe(BASE_REVISION + 2);
    });
});
