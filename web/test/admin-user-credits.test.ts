import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

const usersPanePath = "src/features/admin-console/users-pane.tsx";
const drawerPath = "src/features/admin-console/user-credits-drawer.tsx";
const apiPath = "src/features/admin-console/api.ts";
const presentationPath = "src/features/admin-console/credit-presentation.ts";

/**
 * 「用户管理里的积分」源码级契约。
 *
 * 运营在这条链路上要连着做三件事：找到人、看余额与消耗、需要时补一笔。这三步原本
 * 分散在用户管理和积分管理两个页面，中间要靠手抄账号 ID 衔接——一旦有人把入口摘掉，
 * 流程就退回成"先复制 ID 再去另一个页面搜"。所以这里钉住：入口在、抽屉是独立文件、
 * 读的是单人接口而不是全量列表、原因必填。
 */
describe("后台用户管理积分入口", () => {
    test("用户管理带出余额并保留充值入口", () => {
        expect(existsSync(resolve(root, usersPanePath))).toBe(true);
        const pane = read(usersPanePath);
        expect(pane).toContain("UserCreditsDrawer");
        // 列表列直接给余额，运营不必点进抽屉才知道这个人还有多少分。
        expect(pane).toContain("credit?.balance");
        expect(pane).toContain("credit?.lifetimeOut");
        expect(pane).toContain('title: "积分"');
    });

    test("积分抽屉是独立文件，不是一个文件几千行的写法", () => {
        expect(existsSync(resolve(root, drawerPath))).toBe(true);
        const drawer = read(drawerPath);
        expect(drawer).toContain("export function UserCreditsDrawer(");
        // 展示口径集中在 credit-presentation，两个面板共用一份文案与配色。
        expect(existsSync(resolve(root, presentationPath))).toBe(true);
        expect(drawer).toContain('from "./credit-presentation"');
        expect(read("src/features/admin-console/credits-pane.tsx")).toContain('from "./credit-presentation"');
    });

    test("抽屉读单人余额，不靠翻全量列表", () => {
        const api = read(apiPath);
        expect(api).toContain("export function getAdminCreditAccount(");
        expect(api).toContain("/admin/credits/accounts/${encodeURIComponent(userId)}");
        const drawer = read(drawerPath);
        expect(drawer).toContain("getAdminCreditAccount(targetId)");
    });

    test("充值方向由界面决定符号，原因必填", () => {
        const drawer = read(drawerPath);
        // 界面只收正数，正负由「充值/扣减」决定：让运营手输负号迟早会多打一个。
        expect(drawer).toContain('direction === "out" ? -magnitude : magnitude');
        expect(drawer).toContain("adjustAdminCredits({ userId, amount: signed, note })");
        expect(drawer).toContain('message: "请填写原因"');
        // 会扣成负数时先在界面上说清，而不是等 402 回来再解释。
        expect(drawer).toContain("扣减后余额会变成负数");
        expect(drawer).toContain("isInsufficientCreditsError");
    });
});

/**
 * 浮层令牌：抽屉与弹窗被 antd 挂到 body 下，脱离 .admin-console 的变量作用域。
 * 令牌丢了不会报错，只会让卡片描边、次要文字颜色静默退回 antd 默认值——
 * 界面上看不出"坏了"，只是层次感没了，所以用一条测试钉住这个挂载标记。
 */
describe("后台浮层设计令牌", () => {
    test("令牌同时挂在后台根节点与浮层宿主上", () => {
        const css = read("src/features/admin-console/admin-console.css");
        expect(css).toContain("body.admin-overlay-host {");
        expect(css).toContain("--admin-hairline-soft:");
        expect(css).toContain("body.admin-overlay-host .ant-table-wrapper .ant-table");
    });

    test("后台挂载期间才给 body 打标记，卸载即摘掉", () => {
        const console_ = read("src/features/admin-console/admin-console.tsx");
        expect(console_).toContain('document.body.classList.add("admin-overlay-host")');
        expect(console_).toContain('document.body.classList.remove("admin-overlay-host")');
    });
});
