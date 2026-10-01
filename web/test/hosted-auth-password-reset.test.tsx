import { afterEach, describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { App } from "antd";
import { MemoryRouter } from "react-router";

import { resetHostedAuthPassword, sendPasswordResetCode, type HostedAuthMethod } from "../src/features/hosted-auth/api";
import { HostedAuthLoginPage } from "../src/features/hosted-auth/login-page";
import { PasswordResetForm } from "../src/features/hosted-auth/password-reset-form";
import { apiClient } from "../src/services/api/request";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");
const originalAdapter = apiClient.defaults.adapter;

afterEach(() => {
    apiClient.defaults.adapter = originalAdapter;
});

type Captured = { url?: string; method?: string; data?: unknown };

/** 用 adapter 桩捕请求，确认前端真的打在后端挂载的那两条路径上。 */
function captureRequest(payload: unknown) {
    const captured: Captured[] = [];
    apiClient.defaults.adapter = async (config) => {
        captured.push({ url: config.url, method: config.method, data: config.data });
        return { data: { code: 0, data: payload, msg: "ok" }, status: 200, statusText: "", headers: {}, config } as never;
    };
    return captured;
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

function renderReset(methods: HostedAuthMethod[] = [emailMethod]) {
    return renderToStaticMarkup(
        <MemoryRouter>
            <App>
                <PasswordResetForm methods={methods} initialTarget="you@example.com" onCancel={() => {}} onDone={() => {}} />
            </App>
        </MemoryRouter>,
    );
}

describe("忘记密码", () => {
    test("下发验证码打在 /auth/password/reset/code，与登录验证码不是同一条端点", async () => {
        const captured = captureRequest({ target: "you@example.com", channel: "EMAIL", expiresAt: "2026-10-02T00:00:00Z", cooldownSeconds: 60 });
        await sendPasswordResetCode("EMAIL_CODE", "you@example.com");
        expect(captured[0]?.url).toBe("/auth/password/reset/code");
        expect(captured[0]?.method).toBe("post");
        expect(JSON.parse(String(captured[0]?.data))).toEqual({ methodType: "EMAIL_CODE", target: "you@example.com" });
        // 共用登录那条端点的话，后端按场景计的冷却会把刚点过登录验证码的用户挡在门外。
        expect(captured[0]?.url).not.toBe("/auth/verification-code");
    });

    test("重置密码打在 /auth/password/reset，且带上标识与验证码", async () => {
        const captured = captureRequest({ revokedSessions: 2 });
        const result = await resetHostedAuthPassword({ methodType: "EMAIL_CODE", target: "you@example.com", code: "135790", newPassword: "brandnew9876" });
        expect(captured[0]?.url).toBe("/auth/password/reset");
        expect(captured[0]?.method).toBe("post");
        expect(JSON.parse(String(captured[0]?.data))).toMatchObject({ code: "135790", newPassword: "brandnew9876" });
        // 被踢下线的设备数要透出给用户，否则他会以为改密码没生效。
        expect(result.revokedSessions).toBe(2);
    });

    test("重置表单自带标识、验证码、新密码与确认四段", () => {
        const markup = renderReset();
        // 标识从登录页带过来，重置的人不必再敲一遍自己刚填过的邮箱。
        expect(markup).toContain("you@example.com");
        expect(markup).toContain("hosted-auth-reset-send-code");
        expect(markup).toContain("新密码");
        // 确认框是重置这条路上最容易漏的一段：敲错一位就永久换掉了一串自己也不知道的密码。
        expect(markup).toContain("确认新密码");
        expect(markup).toContain("hosted-auth-reset-submit");
    });

    test("验证码通道一条都没开时不给出忘记密码入口", () => {
        // 只有密码通道时无码可发，"忘记密码"引过去的是一张必然失败的表单。
        const passwordOnly: HostedAuthMethod = { ...emailMethod, methodType: "PASSWORD", category: "PASSWORD", displayName: "密码登录" };
        const render = (methods: HostedAuthMethod[]) =>
            renderToStaticMarkup(
                <MemoryRouter>
                    <App>
                        <HostedAuthLoginPage methods={methods} onAuthenticated={() => {}} />
                    </App>
                </MemoryRouter>,
            );
        expect(render([emailMethod, passwordOnly])).toContain("忘记密码？");
        expect(render([passwordOnly])).not.toContain("忘记密码？");
        // 验证码通道是唯一可用的因子时，主表单本身就是验证码，也没有重置入口。
        expect(render([emailMethod])).not.toContain("忘记密码？");
    });

    test("口令规则与登录页共用同一份，不各写一遍", () => {
        const form = read("src/features/hosted-auth/password-reset-form.tsx");
        const credentials = read("src/features/hosted-auth/credentials.ts");
        expect(form).toContain('from "./credentials"');
        expect(form).toContain("isValidPasswordInput");
        // 规则本体只在 credentials.ts 里出现一次；复制一份就会在服务端改口径后静默漂移。
        expect(form).not.toMatch(/8-64 位[\s\S]*isValidPasswordInput[\s\S]*8-64 位/);
        expect(credentials).toContain("export function isValidPasswordInput");
        expect(credentials).toContain("export function isValidPhoneInput");
    });

    test("登录页把忘记密码接到重置表单上，而不是另开一页", () => {
        const login = read("src/features/hosted-auth/login-page.tsx");
        expect(login).toContain("PasswordResetForm");
        expect(login).toContain('setMode("reset")');
        expect(login).toContain("resetAvailable");
        // 重置成功后回到登录并把标识带回去：这个标识刚刚被证明过，不该让用户再敲一遍。
        expect(login).toContain('form.setFieldValue("target", target)');
    });
});
