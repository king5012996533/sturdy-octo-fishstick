import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

describe("后台协议管理", () => {
    test("发布与签署记录都收敛在 feature 的 api 层", () => {
        const api = read("src/features/admin-console/api.ts");
        expect(api).toContain('"/admin/agreements"');
        expect(api).toContain('"/admin/agreements/signatures"');
        expect(api).toMatch(/publishAdminAgreements\(input: \{ termsTitle: string; termsBody: string; privacyTitle: string; privacyBody: string \}\)/);
    });

    test("发布前必须二次确认，并亮出会被强制重签的账号数", () => {
        const pane = read("src/features/admin-console/agreements-pane.tsx");
        expect(pane).toContain("发布为新版本？");
        expect(pane).toContain("pendingUsers");
        // 全局 antd message 在这个项目里是关闭的，反馈必须落在页面上。
        expect(pane).not.toContain("message.success");
    });

    test("未发布过任何一版时提示仍在用内置骨架正文", () => {
        const pane = read("src/features/admin-console/agreements-pane.tsx");
        expect(pane).toContain("agreements.configured");
    });
});

describe("用户端强制重签", () => {
    test("补签接口走服务端下发的版本号", () => {
        const api = read("src/features/hosted-auth/api.ts");
        expect(api).toContain('"/auth/agreements/accept"');
        expect(api).toContain("HostedAuthAgreementState");
    });

    test("重签页不同意就不能进入工作台", () => {
        const page = read("src/features/hosted-auth/reconsent.tsx");
        expect(page).toContain("我已阅读并同意上述协议");
        expect(page).toContain("disabled={!agreed");
        expect(page).toContain("退出登录");
    });
});
