import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");
const readBackend = (path: string) => readFileSync(resolve(root, "../backend/internal/hosted", path), "utf8");
const lines = (path: string) => read(path).split("\n").length;

/**
 * 用户中心（托管形态的 /settings）。
 *
 * 这一页返工过一次：第一版把六块信息平铺在一屏里，用户的原话是"密密麻麻""全部信息基本
 * 都没啥用"。现在的口径是首屏只回答三个问题——还剩多少积分、我叫什么、密码怎么改——
 * 其余收进「更多设置」。这一组用例就是把这个口径钉住，而不是钉住某一版的排版。
 */
describe("用户中心", () => {
    const primary = ["account-credits-card", "account-profile-card", "account-password-card"];
    const more = ["account-devices-card", "account-bindings-card", "my-creation-posts-card", "account-usage-card", "account-deletion-card"];

    test("首屏只有积分、资料、密码三张卡，且按这个顺序排", () => {
        const center = read("src/pages/settings/account-center.tsx");
        let cursor = -1;
        for (const card of primary) {
            const at = center.indexOf(`<${pascal(card)} />`);
            expect(at).toBeGreaterThan(cursor);
            cursor = at;
        }
        // 其余能力必须在「更多设置」里面，不能和上面三张并列——平铺就是上一版的老问题。
        const moreAt = center.indexOf("<AccountMoreSection>");
        expect(moreAt).toBeGreaterThan(cursor);
        for (const card of more) {
            expect(center.indexOf(`<${pascal(card)} />`)).toBeGreaterThan(moreAt);
        }
    });

    test("一个功能一个文件，且没有长出巨型文件", () => {
        for (const card of [...primary, ...more, "account-more-section", "account-avatar-uploader"]) {
            const file = `src/pages/settings/${card}.tsx`;
            // 这条是硬约束：一个文件几千行之后，改一处要先读懂半页。
            expect(lines(file)).toBeLessThan(400);
            expect(read(file)).toContain("export function");
        }
    });

    test("更多设置收起时不渲染子树", () => {
        const more = read("src/pages/settings/account-more-section.tsx");
        expect(more).toContain("aria-expanded={open}");
        // 条件渲染而不是 CSS 隐藏：藏起来的五张卡不该在用户没打开时把请求发出去。
        expect(more).toContain("{open ? <div className=\"account-more-body\">{children}</div> : null}");
    });

    test("密码卡的两份数据各自成败，绑定接口失败不能把改密码一起带走", () => {
        const password = read("src/pages/settings/account-password-card.tsx");
        expect(password).toContain("Promise.allSettled");
        expect(password).not.toContain("Promise.all(");
        // 已经有密码的账号走的是"旧密码即证明"，这条路完全不依赖绑定接口。
        expect(password).toContain("ChangePasswordForm");
        expect(password).toContain("setAccountPassword(");
        expect(password).toContain("changeAccountPassword(");
    });

    test("头像走上传而不是填地址", () => {
        const card = read("src/pages/settings/account-profile-card.tsx");
        const uploader = read("src/pages/settings/account-avatar-uploader.tsx");
        // 地址是服务端签发的，用户既看不到也不需要理解它；留一个输入框就是在问用户
        // 一个他答不上来的问题，而且外链迟早会因为对方删图变成破图。
        expect(card).not.toContain("头像地址");
        expect(card).not.toContain('placeholder="https://');
        expect(card).toContain("AccountAvatarUploader");
        // 准入规则与服务端 avatar 包同源，两处不一致时用户会先看到"能选"再收到"不支持"。
        expect(uploader).toContain('type="file"');
        expect(uploader).toContain("AVATAR_ACCEPTED_TYPES");
        expect(uploader).toContain("AVATAR_MAX_BYTES");
        expect(uploader).toContain("uploadAccountAvatar(");
    });

    test("忘记密码在用户中心里也能走通，不只是登录页的入口", () => {
        const password = read("src/pages/settings/account-password-card.tsx");
        // 已经有密码的人点"忘记当前密码"，走的是验证码重置这条路；
        // 旧密码那条路（changeAccountPassword）必须原样留着。
        expect(password).toContain("resetHostedAuthPassword(");
        expect(password).toContain("changeAccountPassword(");
        expect(password).toContain("sendPasswordResetCode(");
    });

    test("积分卡把余额留给自己，档位与订单仍只在积分中心", () => {
        const credits = read("src/pages/settings/account-credits-card.tsx");
        expect(credits).toContain("getCreditWallet(");
        expect(credits).toContain('to="/wallet"');
        // 把充值档位、订单、流水搬进账号页，等于把这一页变回对账单。
        expect(credits).not.toContain("getCreditLedger(");
        expect(credits).not.toContain("createBillingOrder(");
    });

    test("前端调用的路径与后端挂载的路由一一对应", () => {
        const routes: Array<[string, string]> = [
            ["src/services/api/account-profile.ts", '"/finance/account/profile"'],
            ["src/services/api/account-security.ts", '"/finance/account/password"'],
            ["src/services/api/account-security.ts", '"/finance/account/sessions"'],
            ["src/services/api/account-security.ts", '"/finance/account/sessions/revoke-others"'],
            ["src/services/api/account-bindings.ts", '"/finance/account/bindings"'],
            ["src/services/api/account-bindings.ts", '"/finance/account/bindings/code"'],
            ["src/services/api/account-profile.ts", '"/finance/account/avatar"'],
        ];
        const backend =
            readBackend("account_profile.go") +
            readBackend("account_password.go") +
            readBackend("account_sessions.go") +
            readBackend("account_bindings.go") +
            readBackend("account_avatar.go");
        for (const [file, path] of routes) {
            expect(read(file)).toContain(path);
            expect(backend).toContain(path.slice(1, -1));
        }
    });

    test("换绑先验证新地址，再落库；没有一步到位的捷径", () => {
        const bindings = read("src/pages/settings/account-bindings-card.tsx");
        expect(bindings).toContain("sendBindingCode(");
        expect(bindings).toContain("confirmBinding(");
        expect(bindings.indexOf("sendBindingCode(")).toBeLessThan(bindings.indexOf("confirmBinding("));
    });

    test("提示统一走 ConfigProvider 内的 message 实例", () => {
        for (const card of [...primary, ...more]) {
            const source = read(`src/pages/settings/${card}.tsx`);
            // 静态 message 在 ConfigProvider 之外：深色主题下会弹出一条读不清的浅色提示。
            expect(source).not.toMatch(/import \{[^}]*\bmessage\b[^}]*\} from "antd"/);
        }
    });
});

/** card 文件名 → 组件名（account-credits-card → AccountCreditsCard）。 */
function pascal(file: string) {
    return file
        .split("-")
        .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
        .join("");
}
