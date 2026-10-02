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
 * 画布版本冲突的恢复：服务端的乐观锁守卫会拒绝落后版本的写入，客户端必须把版本收回来
 * 再重试。以前这里只写 console，本地 revision 停在旧值上，于是这份画布永久保存不了。
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

    test("收敛的是版本而不是内容", () => {
        expect(rebase).toContain("revision");
        // 只写 revision，不能把远端内容搬进本地草稿。
        expect(rebase).toContain("? { ...project, revision } : project");
        expect(rebase).not.toContain("nodes: remote");
    });

    test("保存路径在冲突后重试一次，并重新读一遍本地画布", () => {
        expect(repository).toContain("isCanvasRevisionConflict(error)");
        expect(repository).toContain("await adoptRemoteCanvasRevision(id)");
        // 等待远端版本的这段时间用户可能又编辑过，重试必须重新取本地画布。
        expect(repository).toContain("const attempt = async () => {");
        expect(repository).toContain("const project = openLocalCanvasProject(id);");
    });

    test("服务端为版本冲突给出稳定 reason，前端不再靠解析文案", () => {
        const codes = read("../backend/internal/kernel/error_codes.go");
        expect(codes).toContain('ReasonCanvasRevisionConflict ErrorReason = "canvas_revision_conflict"');
        const history = read("../backend/internal/canvas/canvas_history.go");
        expect(history).toContain("Reason:  kernel.ReasonCanvasRevisionConflict");
    });
});
