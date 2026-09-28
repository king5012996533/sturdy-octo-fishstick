import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dir, "..");

/** 递归收集 src 下的源码文件，用于确认托管登录没有散落到别处。 */
function sourceFiles(directory: string): string[] {
    return readdirSync(directory).flatMap((entry) => {
        const path = join(directory, entry);
        if (statSync(path).isDirectory()) return sourceFiles(path);
        return /\.(ts|tsx)$/.test(entry) ? [path] : [];
    });
}

describe("hosted auth boundary", () => {
    test("登录界面只存在于 feature 目录，其他源码不得直接调用 /api/auth", () => {
        const offenders = sourceFiles(resolve(root, "src"))
            .filter((path) => !path.startsWith(resolve(root, "src/features/hosted-auth")))
            .filter((path) => readFileSync(path, "utf8").includes('"/auth/'));
        expect(offenders.map((path) => path.replace(`${root}/`, ""))).toEqual([]);
    });

    test("根组件只在构建期开关打开时引用登录门", () => {
        const providers = readFileSync(resolve(root, "src/components/layout/app-providers.tsx"), "utf8");
        expect(providers).toContain("__BEEFTV_HOSTED_AUTH__");
        expect(providers).toContain('import("@/features/hosted-auth")');
        // 门必须在工作区水合之前：本地水合在拿不到 bootstrap 时会回落成本地工作区。
        expect(providers.indexOf("<HostedAuthBoundary>")).toBeLessThan(providers.indexOf("<WorkspaceBootstrapHydrator>"));
    });

    test("本地/桌面构建路径不打开托管开关", () => {
        const packageJson = readFileSync(resolve(root, "package.json"), "utf8");
        expect(packageJson).not.toContain('"build:desktop": "BEEFTV_HOSTED_AUTH=1');
        const buildMode = readFileSync(resolve(root, "hosted-auth-build-mode.ts"), "utf8");
        // 默认关闭：只有显式给 1/true 才启用。
        expect(buildMode).toContain('value === "1"');
        expect(buildMode).toContain('value === "true"');
    });

    test("侧边栏退出入口同样挂在构建期开关后面", () => {
        const sidebar = readFileSync(resolve(root, "src/components/layout/workspace-sidebar-nav.tsx"), "utf8");
        expect(sidebar).toContain("__BEEFTV_HOSTED_AUTH__");
        expect(sidebar).toContain('import("@/features/hosted-auth")');
        // 上游守卫断言本地导航没有退出登录项，这里不能把文案写回侧边栏文件。
        expect(sidebar).toContain("footer: []");
        // 文案只能由托管模块提供，侧边栏不得自己内联渲染这个入口。
        expect(sidebar).not.toContain(">退出登录<");
    });

    test("登录响应里的验证码不会被渲染", () => {
        for (const file of ["login-page.tsx", "gate.tsx", "api.ts"]) {
            const source = readFileSync(resolve(root, "src/features/hosted-auth", file), "utf8");
            expect(source).not.toMatch(/debugCode/);
            // 验证码只作为用户输入提交，不允许从后端响应里读取展示。
            expect(source).not.toMatch(/challenge\.code|result\.code|payload\.code/);
        }
    });
});
