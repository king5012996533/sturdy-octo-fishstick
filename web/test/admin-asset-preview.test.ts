import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

const apiPath = "src/features/admin-console/api-assets.ts";
const panePath = "src/features/admin-console/assets-pane.tsx";
const previewPath = "src/features/admin-console/media-preview.tsx";

/**
 * 素材管理的本体预览：源码级契约。
 *
 * 后台看素材的意义就在于「看得见用户生成了什么」。这一页读的是 assets（客户端回写），
 * 本体在 payload 引用的 resources 里，所以三件事不能被顺手改掉：接口带回签名预览字段、
 * 列表有缩略图列、详情抽屉能放大看/播放。
 */
describe("后台素材预览", () => {
    test("接口带回资源本体与签名预览字段", () => {
        const api = read(apiPath);
        expect(api).toContain("resourceId?: string;");
        expect(api).toContain("mediaKind?: string;");
        expect(api).toContain("previewUrl?: string;");
        // 预览地址是后端现场签发的，前端不自己拼 /api/resources 直链。
        expect(api).not.toContain("/api/resources/");
    });

    test("素材列表有缩略图列，详情抽屉有可放大的本体预览", () => {
        const pane = read(panePath);
        expect(pane).toContain('title: "预览"');
        expect(pane).toContain("<MediaPreview");
        expect(pane).toContain("admin-asset-detail-preview");
        expect(pane).toContain("detail.resourceId");
        // 没有本体时要给出原因，而不是留一个打不开的空白框。
        expect(pane).toContain("没有可预览的本体");
    });

    test("预览组件区分图片/视频/音频，缺地址时给空态", () => {
        expect(existsSync(resolve(root, previewPath))).toBe(true);
        const preview = read(previewPath);
        expect(preview).toContain("export function MediaPreview(");
        expect(preview).toContain("<video");
        expect(preview).toContain("<audio");
        expect(preview).toContain("<img");
        expect(preview).toContain("无预览");
    });

    test("素材管理与产物对账共用同一份预览渲染，不各写一套", () => {
        for (const path of [panePath, "src/features/admin-console/resources-pane.tsx"]) {
            expect(read(path)).toContain('from "./media-preview"');
        }
    });

    test("全局 antd message 在项目里是关闭的，反馈必须落在页面上", () => {
        const pane = read(panePath);
        expect(pane).not.toContain("message.success");
        expect(pane).not.toContain("message.error");
        expect(pane).toContain("admin-notice is-error");
    });
});
