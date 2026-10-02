import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { ApiError } from "@/services/api/request";
import { isCanvasRevisionConflict } from "@/services/canvas-revision-rebase";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

const repository = read("src/services/local-workspace-repository.ts");
const rebase = read("src/services/canvas-revision-rebase.ts");

/**
 * 画布版本冲突的口径是「云端为主」：服务端的乐观锁守卫拒绝落后版本的写入后，客户端
 * 不再收敛 revision 重试覆盖云端，而是把云端内容接回本地，同时把本地这份留成冲突草稿。
 * 以前这里只写 console，本地 revision 停在旧值上，于是这份画布永久保存不了；
 * 后来改成收敛后重试，又会把另一台设备的改动盖掉。两条路都不能走。
 */
describe("云端画布版本冲突恢复", () => {
    test("认得出服务端的专用 reason", () => {
        expect(isCanvasRevisionConflict(new ApiError("云端画布已有更新", { status: 409, reason: "canvas_revision_conflict" }))).toBe(true);
    });

    test("灰度期间也要认旧后端：只有笼统的 conflict 时按文案兜底", () => {
        expect(isCanvasRevisionConflict(new ApiError("云端画布已有更新，已停止覆盖；请保留本地草稿并加载最新版本", { status: 409, reason: "conflict" }))).toBe(true);
    });

    test("同样返回 409 但重试没用的冲突不能被当成版本冲突", () => {
        // 「画布引用的素材已变化」也走 409：收敛 revision 再重试只会再撞一次。
        expect(isCanvasRevisionConflict(new ApiError("画布引用的素材已变化，当前内容未被覆盖，请保留草稿并重新加载", { status: 409, reason: "conflict" }))).toBe(false);
        expect(isCanvasRevisionConflict(new ApiError("版本冲突", { status: 409 }))).toBe(false);
    });

    test("别的状态码与普通异常一律不算冲突", () => {
        expect(isCanvasRevisionConflict(new ApiError("云端画布已有更新", { status: 400, reason: "canvas_revision_conflict" }))).toBe(false);
        expect(isCanvasRevisionConflict(new Error("云端画布已有更新"))).toBe(false);
        expect(isCanvasRevisionConflict(undefined)).toBe(false);
    });

    test("采用云端内容之前先把本地那份留档，用户才不会两头落空", () => {
        expect(rebase).toContain("saveCanvasConflictDraft(local)");
        expect(rebase).toContain("applyRemoteCanvasProject(remote, local)");
        // 留档失败也必须如实返回，不能谎称草稿已保存。
        expect(rebase).toContain("draftSaved = await saveCanvasConflictDraft(local)");
    });

    test("冲突后不再重试本地写入，把权威版本让给云端", () => {
        expect(repository).toContain("isCanvasRevisionConflict(error)");
        expect(repository).toContain("await adoptRemoteCanvasProject(id)");
        expect(repository).not.toContain("await adoptRemoteCanvasRevision(id)");
        // 重试本地写入就是"本地为主"，与本次口径相反。
        expect(repository).not.toContain("已收敛版本后重试保存");
    });

    test("读不到远端版本时保持原有失败路径，不假装已采用云端版本", () => {
        expect(rebase).toContain("if (!remote) return { adopted: false, draftSaved: false };");
        expect(repository).toContain("if (!adoption.adopted) throw error;");
    });

    test("服务端为版本冲突给出稳定 reason，前端不再靠解析文案", () => {
        const codes = read("../backend/internal/kernel/error_codes.go");
        expect(codes).toContain('ReasonCanvasRevisionConflict ErrorReason = "canvas_revision_conflict"');
        const history = read("../backend/internal/canvas/canvas_history.go");
        expect(history).toContain("Reason:  kernel.ReasonCanvasRevisionConflict");
    });
});
