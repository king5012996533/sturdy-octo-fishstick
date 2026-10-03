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

    test("接口路径与两个筛选口径不能被改掉", () => {
        const api = read(apiPath);
        expect(api).toContain('"/admin/resources"');
        expect(api).toContain("unreferenced");
        expect(api).toContain("untracked");
        const pane = read(panePath);
        expect(pane).toContain("unreferenced: unreferencedOnly");
        expect(pane).toContain("untracked: untrackedOnly");
        expect(pane).toContain("只看用户没拿到的");
        expect(pane).toContain("只看未关联任务");
    });

    test("对账异常与历史回填是独立面板，回填先演练再写库", () => {
        const api = read(apiPath);
        expect(api).toContain("/admin/resources/backfill");
        expect(api).toContain("dryRun");
        const panelPath = "src/features/admin-console/resources-reconciliation.tsx";
        expect(existsSync(resolve(root, panelPath))).toBe(true);
        const panel = read(panelPath);
        expect(panel).toContain("export function ResourcesReconciliation(");
        expect(panel).toContain("产物无扣费");
        expect(panel).toContain("扣费无产物");
        expect(panel).toContain("确认写入");
        // 两个方向的异常读数都要在面板里给出来，缺一个就成了半张账。
        expect(panel).toContain("unchargedResources");
        expect(panel).toContain("chargedTasks");
    });

    test("产物行带扣费状态：未关联 / 计费前 / 已扣费 / 未扣费", () => {
        const pane = read(panePath);
        expect(pane).toContain("chargeStateLabel(resource.chargeState)");
        expect(pane).toContain("resource.taskId");
        expect(pane).toContain("漏扣费");
        expect(pane).toContain("扣费无产物");
        const panel = read("src/features/admin-console/resources-reconciliation.tsx");
        for (const state of ["untracked", "prebilling", "charged", "uncharged"]) {
            expect(panel).toContain(`${state}:`);
        }
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
        // 播放器渲染搬到共用的 media-preview：这里只钉住接线，产物页必须把签名地址
        // 与资源类型一起交给它，不能自己拼一套。
        expect(pane).toContain("kind={resource.kind} src={resource.previewUrl}");
    });
});
