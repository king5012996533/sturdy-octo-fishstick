import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

describe("托管构建的素材归宿是云端资源库", () => {
    test("托管形态不得把上传写进浏览器 IndexedDB", () => {
        const policy = read("src/services/workspace-resource-storage.ts");
        // 少这一行，托管站会把用户上传只留在当前浏览器里：换台机器素材就消失。
        expect(policy).toContain("isHostedBuild()");
        const guard = policy.indexOf("if (isHostedBuild()) return false;");
        const localCheck = policy.indexOf("return isLocalRuntimeMode() && !isNativeDesktopRuntime();");
        expect(guard).toBeGreaterThan(-1);
        expect(localCheck).toBeGreaterThan(guard);
    });

    test("补传任务只在托管形态运行，且单飞幂等", () => {
        const backfill = read("src/services/canvas-media-cloud-backfill.ts");
        expect(backfill).toContain("if (!isHostedBuild()) return Promise.resolve(");
        expect(backfill).toContain("if (inflight) return inflight;");
        // 读不到的本地字节只能如实计数，不能伪造出一次成功。
        expect(backfill).toContain("result.unreadable += 1;");
        expect(backfill).not.toContain("idempotencyKey: storageKey, file");
    });

    test("落库前收敛媒体节点的展示地址，但只改托管构建提交的那一份", () => {
        const repository = read("src/services/local-workspace-repository.ts");
        expect(repository).toContain("withCloudMediaContentOnProject");
        expect(repository).toContain("const projectForSave = isHostedBuild() ? withCloudMediaContentOnProject(bound) : bound;");
    });

    test("应用启动后按账号作用域触发一次补传", () => {
        const init = read("src/components/layout/client-root-init.tsx");
        expect(init).toContain("backfillCanvasMediaToCloud");
        expect(init).toContain("cloudMediaBackfillScope.current === scope");
    });
});

describe("画布版本冲突以云端为主", () => {
    test("冲突时不再收敛 revision 重试覆盖本地", () => {
        const repository = read("src/services/local-workspace-repository.ts");
        const conflict = repository.slice(repository.indexOf("if (!isCanvasRevisionConflict(error)) throw error;"));
        expect(conflict).toContain("adoptRemoteCanvasProject(id)");
        expect(conflict).toContain("new CanvasCloudAuthoritativeError(id, adoption.draftSaved)");
        // 重试本地写入就是"本地为主"，与本次口径相反。
        expect(conflict.slice(0, conflict.indexOf("}"))).not.toContain("attempt()");
    });

    test("采用云端版本前先把本地那份留成草稿", () => {
        const rebase = read("src/services/canvas-revision-rebase.ts");
        expect(rebase).toContain("saveCanvasConflictDraft(local)");
        expect(rebase).toContain("adopted: true, draftSaved");
        // 读不到远端时必须报失败，不能假装已采用云端版本。
        expect(rebase).toContain("if (!remote) return { adopted: false, draftSaved: false };");
    });

    test("云端为主的那条路径不能触发失败回滚，否则会把云端内容又盖回旧版本", () => {
        const repository = read("src/services/local-workspace-repository.ts");
        expect(repository).toContain("if (previous && !isCanvasCloudAuthoritativeError(error))");
    });

    test("冲突草稿只落在本机，不回写服务端", () => {
        const drafts = read("src/services/canvas-conflict-draft.ts");
        expect(drafts).not.toContain("http.");
        expect(drafts).not.toContain("api/resources");
        // 留档之外还要能找回来，否则"已另存为草稿"只是一句空话。
        expect(drafts).toContain("export async function listCanvasConflictDrafts");
        expect(drafts).toContain("export async function readCanvasConflictDraft");
    });
});
