import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

/**
 * 计费第三阶段的接线测试。
 *
 * 页面与面板各自的测试只覆盖自身，这里盯的是"接进壳"的那几处：路由、侧栏入口、
 * 后台分区。它们分散在四个文件里，任何一处漏改都会让功能静默不可达——页面测试
 * 全绿但用户点不到，正是这类改动最容易出的问题。
 */
describe("计费模块接线", () => {
    test("用户端入口只在托管形态注册：本地构建里不存在必然 404 的 /billing", () => {
        const router = read("src/router.tsx");
        expect(router).toContain('import("@/pages/billing")');
        expect(router).toContain('{ path: "/billing"');
        expect(router).toMatch(/__BEEFTV_HOSTED_AUTH__ \? \[\{ path: "\/billing"/);

        const sidebar = read("src/components/layout/workspace-sidebar-nav.tsx");
        expect(sidebar).toContain('title: "订阅与充值"');
        expect(sidebar).toContain('to: "/billing"');
        expect(sidebar).toMatch(/__BEEFTV_HOSTED_AUTH__ \? \[\{ id: "billing"/);
    });

    test("顶栏标题与路由 slug 对齐", () => {
        expect(read("src/components/layout/workspace-top-bar.tsx")).toContain('billing: "订阅与充值"');
    });

    test("后台三个计费分区都接进 AdminConsole 分区表", () => {
        const console = read("src/features/admin-console/admin-console.tsx");
        expect(console).toContain('import { PlansPane } from "./plans-pane"');
        expect(console).toContain('import { OrdersPane } from "./orders-pane"');
        expect(console).toContain('import { CouponsPane } from "./coupons-pane"');

        // 分区 key 必须同时出现在联合类型与分区表里，否则 ?section=plans 会静默退回仪表盘。
        const union = console.slice(console.indexOf("type ConsoleSectionKey"), console.indexOf("const consoleSections"));
        for (const key of ["plans", "orders", "coupons"]) {
            expect(union).toContain(`"${key}"`);
            expect(console).toContain(`{ key: "${key}"`);
        }
    });
});
