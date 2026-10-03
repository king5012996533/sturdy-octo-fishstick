import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

const apiPath = "src/features/admin-console/api-resources.ts";
const panePath = "src/features/admin-console/resources-pane.tsx";

/**
 * 「生成产物」对账分区：源码级契约。
 *
 * 这一页存在的意义只有一个——用户端回写失败、上游却已产出的产物必须看得见。所以除了
 * 导出名与接口路径，这里钉住两件事：读的是 /admin/resources（resources 全量），以及
 * 「只看用户没拿到」的筛选入口不能被人顺手删掉。
 */
describe("后台生成产物面板", () => {
    test("文件存在，导出名与接线一致", () => {
        expect(existsSync(resolve(root, apiPath))).toBe(true);
        expect(existsSync(resolve(root, panePath))).toBe(true);
        expect(read(panePath)).toContain("export function ResourcesPane()");
        expect(read(apiPath)).toContain("export function listAdminResources(");
    });

    test("接口路径与「未被引用」筛选不能被改掉", () => {
        const api = read(apiPath);
        expect(api).toContain('"/admin/resources"');
        expect(api).toContain("unreferenced");
        const pane = read(panePath);
        expect(pane).toContain("unreferenced: unreferencedOnly");
        expect(pane).toContain("只看用户没拿到的");
    });

    test("全局 antd message 在项目里是关闭的，反馈必须落在页面上", () => {
        const pane = read(panePath);
        expect(pane).not.toContain("message.success");
        expect(pane).not.toContain("message.error");
        expect(pane).not.toContain("notification.");
        expect(pane).toContain("admin-notice is-error");
        expect(pane).toContain("重试");
        expect(pane).toContain("admin-empty");
    });

    test("预览与筛选：签名地址、类型、时间范围都在页面上", () => {
        const pane = read(panePath);
        expect(pane).toContain("resource.previewUrl");
        expect(pane).toContain("DatePicker.RangePicker");
        expect(pane).toContain("startOf(\"day\").toISOString()");
        expect(pane).toContain('kind === "video"');
        expect(pane).toContain('kind === "audio"');
    });
});
