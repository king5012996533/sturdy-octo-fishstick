import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

describe("后台站点设置", () => {
    test("外观接口收敛在 feature 的 api 层，资源只传 ID", () => {
        const api = read("src/features/admin-console/api.ts");
        expect(api).toContain('"/admin/settings/appearance"');
        expect(api).toContain("`/admin/settings/appearance/assets/${slot}`");
        expect(api).toMatch(/export type AdminAppearanceInput = Omit</);
    });

    test("面板把反馈落在页面上，并支持恢复默认", () => {
        const pane = read("src/features/admin-console/settings-pane.tsx");
        // 全局 antd message 在这个项目里是关闭的，反馈必须落在页面上。
        expect(pane).not.toContain("message.success");
        expect(pane).toContain("admin-notice is-ok");
        expect(pane).toContain("恢复为默认品牌？");
        // 保存后要回写本地 appearance store，否则后台还显示旧品牌。
        expect(pane).toContain("commitPublicAppearance");
    });
});

describe("后台验证码网关", () => {
    test("网关接口收敛在 feature 的 api 层", () => {
        const api = read("src/features/admin-console/api.ts");
        expect(api).toContain('"/admin/gateways"');
        expect(api).toContain("`/admin/gateways/${channel}`");
        expect(api).toContain("`/admin/gateways/${channel}/test`");
    });

    test("密钥只写不读：留空表示保持原值", () => {
        const pane = read("src/features/admin-console/gateways-pane.tsx");
        expect(pane).toContain("已设置，留空表示不修改");
        expect(pane).not.toContain("message.success");
        // 运营必须能看到当前配置来源，否则会出现"后台配好了但发不出去"。
        expect(pane).toContain("sourceLabels");
        expect(pane).toContain("仅写日志");
    });
});

describe("后台导航", () => {
    test("站点设置与验证码网关都在配置分区里", () => {
        const console = read("src/features/admin-console/admin-console.tsx");
        expect(console).toContain('key: "settings"');
        expect(console).toContain('key: "gateways"');
    });
});
