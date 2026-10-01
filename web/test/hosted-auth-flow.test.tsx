import { afterEach, describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { App } from "antd";
import { MemoryRouter } from "react-router";

import { detectHostedAuth, type HostedAuthMethod } from "../src/features/hosted-auth/api";
import { resolveHostedAuthGatePhase } from "../src/features/hosted-auth/gate";
import { HostedAuthLoginPage, isValidPasswordInput, isValidPasswordTargetInput, isValidPhoneInput, normalizePhoneInput, oauthRedirectUri, resolveDevCodeHint, shouldOfferRegistration } from "../src/features/hosted-auth/login-page";
import { HostedAuthAccountPanel, HostedAuthSidebarFooter, hostedAuthIdentityLabel, performHostedAuthLogout } from "../src/features/hosted-auth/sidebar-footer";
import { ApiError, apiClient } from "../src/services/api/request";

const originalAdapter = apiClient.defaults.adapter;

afterEach(() => {
    apiClient.defaults.adapter = originalAdapter;
});

/** 用 axios adapter 桩住 HTTP，避免测试依赖真实后端。 */
function stubApi(handler: (config: { url?: string }) => { status: number; data: unknown }) {
    apiClient.defaults.adapter = async (config) => {
        const { status, data } = handler(config as { url?: string });
        return { data, status, statusText: "", headers: {}, config } as never;
    };
}

const emailMethod: HostedAuthMethod = {
    methodType: "EMAIL_CODE",
    category: "CODE",
    displayName: "邮箱验证码",
    description: "",
    iconType: "",
    sortOrder: 10,
    allowSignUp: true,
};

const githubMethod: HostedAuthMethod = { ...emailMethod, methodType: "GITHUB_OAUTH", category: "OAUTH", displayName: "GitHub 登录", sortOrder: 20 };

const phoneMethod: HostedAuthMethod = { ...emailMethod, methodType: "PHONE_CODE", displayName: "手机号验证码", sortOrder: 11 };

const passwordMethod: HostedAuthMethod = { ...emailMethod, methodType: "PASSWORD", category: "PASSWORD", displayName: "密码登录", sortOrder: 12 };

function renderLogin(methods: HostedAuthMethod[]) {
    return renderToStaticMarkup(
        <App>
            <HostedAuthLoginPage methods={methods} onAuthenticated={() => {}} />
        </App>,
    );
}

describe("hosted auth probe", () => {
    test("后端没有登录路由时判定为本地形态，不启用登录门", async () => {
        stubApi(() => ({ status: 404, data: { code: 404, data: null, msg: "请求不存在" } }));
        expect(await detectHostedAuth()).toBeNull();
    });

    test("后端故障不会被误判成本地形态", async () => {
        stubApi(() => ({ status: 500, data: { code: 500, data: null, msg: "系统处理失败，请稍后重试" } }));
        await expect(detectHostedAuth()).rejects.toThrow();
    });

    test("启用登录时返回登录方式列表", async () => {
        stubApi(() => ({ status: 200, data: { code: 0, data: { methods: [emailMethod] }, msg: "ok" } }));
        expect(await detectHostedAuth()).toEqual([emailMethod]);
    });
});

describe("hosted auth gate phases", () => {
    test("本地形态透传，托管形态未登录则要求登录", () => {
        expect(resolveHostedAuthGatePhase({ methods: null, session: null })).toEqual({ phase: "local" });
        expect(resolveHostedAuthGatePhase({ methods: [emailMethod], session: null })).toEqual({ phase: "anonymous", methods: [emailMethod] });
    });

    test("已有会话时直接放行", () => {
        const user = { id: "u1", name: "用户", email: "u@example.com", avatarUrl: "", role: "USER", status: "ACTIVE" };
        expect(resolveHostedAuthGatePhase({ methods: [emailMethod], session: user })).toEqual({ phase: "authenticated", user });
    });

    test("签署的是旧版本时先要求重新同意，不能直接放行", () => {
        const user = { id: "u1", name: "用户", email: "u@example.com", avatarUrl: "", role: "USER", status: "ACTIVE" };
        // 发布新版本后，所有账号的已签版本都会落后于当前版本；默认放行等于把留痕写成假的。
        expect(resolveHostedAuthGatePhase({
            methods: [emailMethod],
            session: user,
            agreements: { currentVersion: "2026-10-01", acceptedVersion: "2026-09-28", accepted: false },
        })).toEqual({ phase: "reconsent", user, currentVersion: "2026-10-01" });
        // 已签当前版本照常放行。
        expect(resolveHostedAuthGatePhase({
            methods: [emailMethod],
            session: user,
            agreements: { currentVersion: "2026-10-01", acceptedVersion: "2026-10-01", accepted: true },
        })).toEqual({ phase: "authenticated", user });
    });

    test("探测失败时保持加载态，绝不放行", () => {
        expect(resolveHostedAuthGatePhase(null)).toBeNull();
    });
});

describe("hosted auth login page", () => {
    test("启用邮箱验证码时渲染发送按钮，且不渲染任何验证码", () => {
        const markup = renderLogin([emailMethod]);
        expect(markup).toContain("发送验证码");
        expect(markup).not.toMatch(/\b\d{6}\b/);
        expect(markup).not.toContain("使用 GitHub 登录");
    });

    test("启用 GitHub 时渲染第三方登录入口", () => {
        expect(renderLogin([emailMethod, githubMethod])).toContain("使用 GitHub 登录");
    });

    test("没有任何登录方式时给出明确提示", () => {
        expect(renderLogin([])).toContain("当前没有可用的登录方式");
    });

    test("OAuth 回调地址固定在后端回调路径上", () => {
        expect(oauthRedirectUri("https://canvas.example.com")).toBe("https://canvas.example.com/auth/oauth/callback");
        expect(oauthRedirectUri("https://canvas.example.com/")).toBe("https://canvas.example.com/auth/oauth/callback");
    });
});

describe("邮箱与手机号双通道", () => {
    test("两种验证码通道并存时标识框同时接受邮箱与手机号", () => {
        // 形态由输入内容决定：不再让用户在输入之前先声明走哪条通道。
        const markup = renderLogin([emailMethod, phoneMethod]);
        expect(markup).toContain("you@example.com 或 138 0013 8000");
        expect(markup).toContain("邮箱或手机号");
        expect(markup).not.toContain("hosted-auth-factor-switch");
    });

    test("只开启手机号时标识框不接受邮箱", () => {
        const markup = renderLogin([phoneMethod]);
        expect(markup).toContain("手机号验证码登录");
        expect(markup).toContain("138 0013 8000");
        expect(markup).not.toContain("you@example.com");
    });

    test("只有 GitHub 时副标题不再声称支持验证码", () => {
        const markup = renderLogin([githubMethod]);
        expect(markup).toContain("使用 GitHub 账号登录");
        expect(markup).not.toContain("验证码登录");
    });

    test("手机号归一与服务端保持同一套规则", () => {
        expect(normalizePhoneInput("+86 138-0013-8000")).toBe("13800138000");
        expect(normalizePhoneInput("(138) 0013 8000")).toBe("13800138000");
        expect(normalizePhoneInput("8613800138000")).toBe("13800138000");
        expect(normalizePhoneInput("13800138000")).toBe("13800138000");
        expect(normalizePhoneInput("")).toBe("");
    });

    test("手机号校验拒绝短号、异网段与非法字符", () => {
        expect(isValidPhoneInput("138 0013 8000")).toBe(true);
        expect(isValidPhoneInput("+8613800138000")).toBe(true);
        expect(isValidPhoneInput("12800138000")).toBe(false);
        expect(isValidPhoneInput("1380013800")).toBe(false);
        expect(isValidPhoneInput("1380013800a")).toBe(false);
        expect(isValidPhoneInput("")).toBe(false);
    });
});

describe("本地联调验证码回显", () => {
    test("后端回显验证码时给出自动填入提示", () => {
        const hint = resolveDevCodeHint({ devCode: "493113" });
        expect(hint?.code).toBe("493113");
        expect(hint?.message).toContain("493113");
    });

    test("没有回显时保持沉默，不出现任何提示", () => {
        expect(resolveDevCodeHint({})).toBeNull();
        expect(resolveDevCodeHint({ devCode: "   " })).toBeNull();
    });
});

describe("注册与协议", () => {
    test("开放注册时登录页给出注册入口", () => {
        expect(renderLogin([emailMethod])).toContain("立即注册");
    });

    test("关闭注册时不出现注册入口，避免用户撞上 403", () => {
        expect(renderLogin([{ ...emailMethod, allowSignUp: false }])).not.toContain("立即注册");
    });

    test("未注册邮箱登录失败后引导到注册", () => {
        expect(shouldOfferRegistration(new ApiError("该邮箱尚未注册，请先注册", { status: 404, reason: "not_found" }))).toBe(true);
    });

    test("验证码错误等失败不会被误判成需要注册", () => {
        expect(shouldOfferRegistration(new ApiError("验证码无效或已过期", { status: 401, reason: "unauthorized" }))).toBe(false);
        expect(shouldOfferRegistration(new ApiError("系统处理失败", { status: 500, reason: "internal" }))).toBe(false);
        expect(shouldOfferRegistration(new Error("boom"))).toBe(false);
    });
});

describe("hosted auth logout", () => {
    test("先让服务端吊销会话，再清本地状态并整页回入口", async () => {
        const calls: string[] = [];
        await performHostedAuthLogout({
            logout: async () => {
                calls.push("logout");
            },
            clearLocalSession: () => calls.push("clear"),
            redirect: (url) => calls.push(`redirect:${url}`),
        });
        expect(calls).toEqual(["logout", "clear", "redirect:/"]);
    });

    test("吊销失败也必须清掉本地账号状态", async () => {
        const calls: string[] = [];
        await performHostedAuthLogout({
            logout: async () => {
                calls.push("logout");
                throw new Error("网络失败");
            },
            clearLocalSession: () => calls.push("clear"),
            redirect: (url) => calls.push(`redirect:${url}`),
        });
        expect(calls).toEqual(["logout", "clear", "redirect:/"]);
    });

    test("侧边栏账户入口展开态是身份条，收起态只留图标", () => {
        const expanded = renderToStaticMarkup(
            <App>
                <HostedAuthSidebarFooter collapsed={false} />
            </App>,
        );
        // 展开态渲染身份条：退出登录收进浮层，侧栏里不再常驻破坏性按钮。
        expect(expanded).toContain('data-testid="hosted-auth-account-chip"');
        expect(expanded).not.toContain('data-testid="hosted-auth-logout"');

        const collapsed = renderToStaticMarkup(
            <App>
                <HostedAuthSidebarFooter collapsed />
            </App>,
        );
        // 收起态只留图标：宽度只有 40px，除图标外的一切都会被裁掉半截。
        expect(collapsed).not.toContain("<span");
        expect(collapsed).toContain('data-testid="hosted-auth-account-chip"');
    });

    test("账户面板里才有退出登录，且带独立 testid", () => {
        const panel = renderToStaticMarkup(
            <MemoryRouter>
                <App>
                    <HostedAuthAccountPanel onLogout={() => {}} pending={false} />
                </App>
            </MemoryRouter>,
        );
        expect(panel).toContain("退出登录");
        expect(panel).toContain('data-testid="hosted-auth-logout"');
        expect(panel).toContain('data-testid="hosted-auth-account-panel"');
        // 托管形态的导航里没有 /settings（模型配置入口被摇掉后那一页就没有入口了），
        // 用户中心必须从账户菜单进得去，否则整页只能靠手输地址打开。
        expect(panel).toContain('data-testid="hosted-auth-account-center"');
        expect(panel).toContain('href="/settings"');
    });

    test("身份副标题按邮箱、绑定标识、用户名依次回落", () => {
        expect(hostedAuthIdentityLabel(null)).toBe("");
        expect(hostedAuthIdentityLabel({ username: "kino", email: "a@b.co", identityId: "13800138000" })).toBe("a@b.co");
        expect(hostedAuthIdentityLabel({ username: "kino", identityId: "13800138000" })).toBe("13800138000");
        expect(hostedAuthIdentityLabel({ username: "kino" })).toBe("@kino");
        expect(hostedAuthIdentityLabel({})).toBe("");
    });
});

describe("密码通道", () => {
    test("密码与验证码都在时默认走密码，验证码降级为表单里的次要动作", () => {
        const markup = renderLogin([emailMethod, phoneMethod, passwordMethod]);
        // 默认因子是密码：它不依赖任何投递通道，是唯一「一定能用」的那条。
        expect(markup).toContain("hosted-auth-password");
        expect(markup).toContain("hosted-auth-factor-switch");
        // 切换控件是分段控件，选中态只落在密码这一条上：验证码只是候选，不是并列的入口。
        expect(markup).toContain('class="auth-factor-tab is-active" data-testid="hosted-auth-factor-switch"');
        expect(markup).toContain("验证码登录");
        expect(markup).not.toContain("发送验证码");
    });

    test("只开密码通道时直接渲染密码表单", () => {
        const markup = renderLogin([passwordMethod]);
        expect(markup).toContain("使用密码登录");
        expect(markup).toContain("hosted-auth-password");
        expect(markup).not.toContain("hosted-auth-factor-switch");
        // 密码通道没有下发动作，出现发送按钮就是把用户引到一个必然失败的按钮上。
        expect(markup).not.toContain("发送验证码");
        expect(markup).not.toContain("邮箱或手机号验证码登录");
    });

    test("密码通道的标识标签不写死成邮箱", () => {
        const markup = renderLogin([passwordMethod]);
        expect(markup).toContain("邮箱或手机号");
    });

    test("密码通道的最小可用性：验证码通道全关也能登录", () => {
        const markup = renderLogin([passwordMethod, githubMethod]);
        expect(markup).toContain("使用密码或 GitHub 账号登录");
        expect(markup).toContain("hosted-auth-github");
    });

    test("后端关闭注册时密码通道不显示注册入口", () => {
        expect(renderLogin([passwordMethod])).toContain("立即注册");
        expect(renderLogin([{ ...passwordMethod, allowSignUp: false }])).not.toContain("立即注册");
    });

    test("密码校验与服务端 validatePassword 同一套规则", () => {
        for (const valid of ["abc12345", "a1234567", "P@ssw0rd!", "abcdefg1"]) {
            expect(isValidPasswordInput(valid)).toBe(true);
        }
        for (const invalid of ["", "abc123", "12345678", "abcdefgh", "abc 12345", "密码密码密码密码", "a1".repeat(33)]) {
            expect(isValidPasswordInput(invalid)).toBe(false);
        }
    });

    test("密码通道的标识同时接受邮箱与手机号", () => {
        expect(isValidPasswordTargetInput("you@example.com")).toBe(true);
        expect(isValidPasswordTargetInput("13800138000")).toBe(true);
        expect(isValidPasswordTargetInput("+86 138-0013-8000")).toBe(true);
        expect(isValidPasswordTargetInput("nobody")).toBe(false);
        expect(isValidPasswordTargetInput("you@")).toBe(false);
        expect(isValidPasswordTargetInput("")).toBe(false);
    });
});
