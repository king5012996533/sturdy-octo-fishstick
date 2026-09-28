import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dir, "..");

/** 递归收集 src 下的源码文件，用于确认运营后台没有散落到别处。 */
function sourceFiles(directory: string): string[] {
    return readdirSync(directory).flatMap((entry) => {
        const path = join(directory, entry);
        if (statSync(path).isDirectory()) return sourceFiles(path);
        return /\.(ts|tsx)$/.test(entry) ? [path] : [];
    });
}

describe("admin console boundary", () => {
    test("运营后台接口只由 feature 目录调用，其他源码不得直接打 /api/admin", () => {
        const offenders = sourceFiles(resolve(root, "src"))
            .filter((path) => !path.startsWith(resolve(root, "src/features/admin-console")))
            .filter((path) => readFileSync(path, "utf8").includes('"/admin/'));
        expect(offenders.map((path) => path.replace(`${root}/`, ""))).toEqual([]);
    });

    test("路由只在构建期开关打开时引用运营后台", () => {
        const router = readFileSync(resolve(root, "src/router.tsx"), "utf8");
        expect(router).toContain("__BEEFTV_HOSTED_AUTH__");
        // 动态 import 必须写在开关后面：本地/桌面构建里它要能被摇树删掉。
        expect(router).toContain('import("@/features/admin-console")');
        expect(router.indexOf("__BEEFTV_HOSTED_AUTH__")).toBeLessThan(router.indexOf('import("@/features/admin-console")'));
    });

    test("侧边栏入口要求管理员角色，且不直接引用后台模块", () => {
        const sidebar = readFileSync(resolve(root, "src/components/layout/workspace-sidebar-nav.tsx"), "utf8");
        // 本地拥有者角色也是 admin，所以必须同时要求托管开关，否则桌面端会指向不存在的路由。
        expect(sidebar).toContain('__BEEFTV_HOSTED_AUTH__ && user?.role === "admin"');
        // 入口只做跳转：直接 import 会把整个后台打进本地产物。
        expect(sidebar).not.toContain('import("@/features/admin-console")');
    });

    test("用户端不提供渠道配置面：入口与设置页都经过构建期开关", () => {
        for (const file of ["src/components/layout/workspace-sidebar-nav.tsx", "src/components/layout/workspace-top-bar.tsx", "src/components/layout/workspace-command-palette.tsx"]) {
            const source = readFileSync(resolve(root, file), "utf8");
            // 入口必须走 userChannelConfigVisible：托管实例上功能开关只剩接口与计费语义，
            // 直接按它渲染会让 SaaS 前台重新长出"模型配置"。
            expect(source).toContain("userChannelConfigVisible(");
        }
        const gate = readFileSync(resolve(root, "src/lib/user-channel-ui.ts"), "utf8");
        expect(gate).toContain("__BEEFTV_HOSTED_AUTH__");
        const settings = readFileSync(resolve(root, "src/pages/settings/index.tsx"), "utf8");
        // 设置页同样不能只看功能开关：托管产物只有"模型由平台提供"的说明页。
        expect(settings).toContain("__BEEFTV_HOSTED_AUTH__ || !customChannelsEnabled");
    });

    test("模型编辑弹窗打开时回填表单，避免必填项为空导致无法保存", () => {
        const source = readFileSync(resolve(root, "src/features/admin-console/channels-pane.tsx"), "utf8");
        // 弹窗带 destroyOnHidden：每次打开都是新表单实例。历史上编辑入口只 setModelEditor，
        // 结果是"平台模型标识"为空、保存必然被必填校验拦下。
        expect(source).toContain("openModelEditor(record)");
        expect(source).toMatch(/const openModelEditor = \(model\?: AdminChannelModel\) => \{[\s\S]*?modelForm\.setFieldsValue\(/);
    });

    test("后台模块不引用本地工作区仓储，避免两套持久化混用", () => {
        for (const file of ["api.ts", "channels-pane.tsx", "features-pane.tsx", "policy-pane.tsx"]) {
            const source = readFileSync(resolve(root, "src/features/admin-console", file), "utf8");
            expect(source).not.toMatch(/local-workspace-repository|localforage|use-config-store/);
        }
    });
});
