import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

describe("后台内容审核", () => {
    test("画布处置接口只走 feature 的 api 层，并且以理由为必填参数", () => {
        const api = read("src/features/admin-console/api.ts");
        expect(api).toContain('"/admin/canvases"');
        expect(api).toMatch(/updateAdminCanvasModeration\(id: string, status: AdminCanvasModerationStatus, reason: string\)/);
    });

    test("面板在提交处置前拦下空理由，避免用户拿到一句无法解释的 403", () => {
        const pane = read("src/features/admin-console/canvas-pane.tsx");
        expect(pane).toContain("必须填写理由");
        expect(pane).toContain("updateAdminCanvasModeration");
        // 反馈必须落在页面上：全局 antd message 在这个项目里是关闭的。
        expect(pane).not.toContain("message.success");
    });

    test("后台导航包含内容审核分区", () => {
        const console = read("src/features/admin-console/admin-console.tsx");
        expect(console).toContain('key: "canvases"');
        expect(console).toContain("<CanvasPane />");
    });
});

describe("用户端下架提示", () => {
    test("处置清单只在托管形态请求", () => {
        const hook = read("src/lib/use-own-canvas-moderation.ts");
        expect(hook).toContain("__BEEFTV_HOSTED_AUTH__");
        expect(read("src/services/api/canvas-moderation.ts")).toContain("/canvas-moderation/mine");
    });

    test("画布页命中下架时停在落地页，并且不抢先读取正文", () => {
        const lifecycle = read("src/pages/canvas/use-canvas-project-lifecycle.ts");
        // 下架或审核状态未知都必须原地等待：读取失败会把用户悄悄踢回画布库。
        expect(lifecycle).toContain("if (moderationBlocked || moderationPending)");
        const project = read("src/pages/canvas/project.tsx");
        expect(project).toContain("<CanvasModerationBlocked notice={moderationBlock} />");
        expect(project).toContain("moderationPending: canvasModeration.pending");
    });
});
