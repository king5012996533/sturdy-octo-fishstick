import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

/**
 * 运营后台分区的接线测试（第四阶段 RBAC/素材/模板/工单，第五阶段 模型厂商/模型定价）。
 *
 * 四个模块各自的面板测试只覆盖自身；真正容易漏的是"接进壳"的那几处：分区 key、
 * 面板 import、用户端路由与侧栏入口。漏一处页面测试依然全绿，但功能点不到。
 */
describe("运营后台分区接线", () => {
    const sections: Array<{ key: string; pane: string; importPath: string }> = [
        { key: "roles", pane: "RolesPane", importPath: "./roles-pane" },
        { key: "assets", pane: "AssetsPane", importPath: "./assets-pane" },
        { key: "resources", pane: "ResourcesPane", importPath: "./resources-pane" },
        { key: "templates", pane: "TemplatesPane", importPath: "./templates-pane" },
        { key: "tickets", pane: "TicketsPane", importPath: "./tickets-pane" },
        { key: "vendors", pane: "VendorsPane", importPath: "./vendors-pane" },
        { key: "pricing", pane: "PricingPane", importPath: "./pricing-pane" },
        { key: "posts", pane: "PostsPane", importPath: "./posts-pane" },
    ];

    test("每个新分区都进了 AdminConsole（import + 联合类型 + 分区表）", () => {
        const console = read("src/features/admin-console/admin-console.tsx");
        const union = console.slice(console.indexOf("type ConsoleSectionKey"), console.indexOf("const consoleSections"));
        for (const section of sections) {
            expect(console).toContain(`import { ${section.pane} } from "${section.importPath}"`);
            expect(union).toContain(`"${section.key}"`);
            expect(console).toContain(`{ key: "${section.key}"`);
            expect(console).toContain(`<${section.pane} />`);
        }
    });

    test("每个分区面板都真实导出对应的组件", () => {
        for (const section of sections) {
            const pane = read(`src/features/admin-console/${section.importPath.replace("./", "")}.tsx`);
            expect(pane).toContain(`export function ${section.pane}()`);
            // 全局 antd message 在本项目被关闭：反馈必须落在页面上。
            expect(pane).toContain("admin-notice");
        }
    });

    test("侧栏可滚动，模型与计费入口不会被高度裁到点不到", () => {
        // 分区有二十多个，矮屏放不下。侧栏一度是 overflow: visible，靠 .admin-console 的
        // overflow: hidden 兜底 → 超出的分区被裁掉，鼠标够不到，界面上等于没有入口。
        const css = read("src/features/admin-console/admin-console.css");
        const railStart = css.indexOf(".admin-console-rail {");
        const rail = css.slice(railStart, css.indexOf("}", railStart));
        expect(rail).toContain("overflow-y: auto");

        const consoleSrc = read("src/features/admin-console/admin-console.tsx");
        const order = [...consoleSrc.matchAll(/key: "([a-z-]+)"/g)].map((match) => match[1]);
        expect(order.length).toBe(24);
        // 常用的模型/计费入口排在最前，否则默认视口下根本看不到。
        expect(order.slice(0, 4)).toEqual(["dashboard", "vendors", "channels", "pricing"]);
    });

    test("用户端「帮助与反馈」只在托管形态注册，并出现在侧栏与顶栏标题", () => {
        const router = read("src/router.tsx");
        expect(router).toContain('import("@/pages/support")');
        expect(router).toMatch(/__BEEFTV_HOSTED_AUTH__ \? \[\{ path: "\/support"/);

        const sidebar = read("src/components/layout/workspace-sidebar-nav.tsx");
        expect(sidebar).toContain('title: "帮助与反馈"');
        expect(sidebar).toMatch(/__BEEFTV_HOSTED_AUTH__ \? \[\{ id: "support"/);

        expect(read("src/components/layout/workspace-top-bar.tsx")).toContain('support: "帮助与反馈"');
    });
});
