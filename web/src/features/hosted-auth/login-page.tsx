import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App, Button, Checkbox, ConfigProvider, Divider, Form, Input, Typography, theme as antdTheme } from "antd";
import { GithubOutlined } from "@ant-design/icons";

import { BrandLogoFrame } from "@/components/brand/brand-logo";
import { SiteComplianceFooter } from "@/components/layout/site-compliance-footer";
import { ApiError } from "@/services/api/request";
import { brandStudioLabel, useAppearanceStore } from "@/stores/use-appearance-store";

import "./login-page.css";

import { HostedAuthAgreementDialog } from "./agreement-dialog";
import { completeHostedOAuthCallback, fetchHostedAuthAgreements, loginHostedAuth, registerHostedAuth, requestHostedOAuthAuthorize, sendHostedAuthCode, type HostedAuthAgreements, type HostedAuthMethod, type HostedAuthUser } from "./api";

const GITHUB_METHOD = "GITHUB_OAUTH";
const EMAIL_METHOD = "EMAIL_CODE";
const PHONE_METHOD = "PHONE_CODE";
const PASSWORD_METHOD = "PASSWORD";
const CALLBACK_PATH = "/auth/oauth/callback";

/** 回调地址必须与 GitHub OAuth App 里登记的地址一致，因此由前端计算并复用。 */
export function oauthRedirectUri(origin: string) {
    return `${origin.replace(/\/$/, "")}${CALLBACK_PATH}`;
}

/** 派发浏览器跳转；抽成参数便于测试，不在测试里真的离开页面。 */
export type LocationAssigner = (url: string) => void;

/**
 * 登录因子：同一个标识，用密码还是验证码证明归属。
 *
 * 渠道不是用户要做的选择题。用户手里只有「邮箱/手机号」和「口令」，把
 * 邮箱 / 手机号 / 密码 摆成一排让他在输入之前先声明走哪条通道，等于把后端的
 * 通道模型摊到界面上：点错一次要重填，改主意要重填——这正是「为了登录而登录」。
 * 标识形态由输入内容判断，因子是表单里一个可撤回的次要动作。
 */
export type HostedAuthFactor = "password" | "code";

const EMAIL_INPUT_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function isValidEmailInput(value: string): boolean {
    const raw = String(value ?? "").trim();
    return EMAIL_INPUT_PATTERN.test(raw) && !raw.includes(" ");
}

/**
 * 与服务端 normalizePhone 对齐：先去掉分隔符和 +86 前缀再判断。
 *
 * 前端只做即时提示，最终以服务端为准；两边规则不一致会让用户看到「格式正确却
 * 被拒绝」，所以这里的归一逻辑必须和服务端保持一致。
 */
export function normalizePhoneInput(value: string): string {
    const compact = String(value ?? "").replace(/[\s\-()\t]/g, "");
    const withoutPrefix = compact.startsWith("+") ? compact.slice(1) : compact;
    return withoutPrefix.startsWith("86") && withoutPrefix.length > 11 ? withoutPrefix.slice(2) : withoutPrefix;
}

export function isValidPhoneInput(value: string): boolean {
    return /^1[3-9]\d{9}$/.test(normalizePhoneInput(value));
}

/**
 * 密码通道的标识既可以是邮箱也可以是手机号，服务端按形态分列存储。
 *
 * 规则必须和服务端 classifyPasswordTarget 对齐：只在一边放宽，用户就会看到
 * 「前端说格式没问题、提交后被拒」。
 */
export function isValidPasswordTargetInput(value: string): boolean {
    const raw = String(value ?? "").trim();
    if (!raw) return false;
    return isValidPhoneInput(raw) || isValidEmailInput(raw);
}

/**
 * 与服务端 validatePassword 对齐：8-64 位、不含空白、字母与数字都要有。
 *
 * 只做长度和字符类别，不强制大小写与符号 —— 服务端也是这个口径。
 */
export function isValidPasswordInput(value: string): boolean {
    const raw = String(value ?? "");
    if (raw.length < 8 || raw.length > 64) return false;
    if (/\s/.test(raw)) return false;
    return /[A-Za-z]/.test(raw) && /\d/.test(raw);
}

/**
 * 本地联调时后端会回显验证码（没有真实投递通道才可能发生）。
 *
 * 抽成纯函数是为了让「回显」这件事在测试里可断言，而不是藏在点击回调内部。
 */
export function resolveDevCodeHint(challenge: { devCode?: string }): { code: string; message: string } | null {
    const code = String(challenge.devCode ?? "").trim();
    if (!code) return null;
    return { code, message: `本地投递：验证码 ${code} 已自动填入（未真实发送）` };
}

/**
 * 登录失败后是否应该把用户引导到注册页。
 *
 * 后端对「标识未注册」返回 404 + reason=not_found（reason 是稳定契约，msg 会变），
 * 据此直接把用户送到注册表单，省掉他自己猜「是不是要注册」。
 */
export function shouldOfferRegistration(error: unknown): boolean {
    return error instanceof ApiError && (error.reason === "not_found" || error.status === 404);
}

/** 标识形态：验证码通道按形态寻址，密码通道两种都收。 */
type IdentityShape = "email" | "phone";

const defaultAssign: LocationAssigner = (url) => window.location.assign(url);

export function HostedAuthLoginPage({ methods, onAuthenticated }: { methods: HostedAuthMethod[]; onAuthenticated: (user: HostedAuthUser) => void }) {
    const { message } = App.useApp();
    const appearance = useAppearanceStore((state) => state.appearance);
    const [form] = Form.useForm<{ target: string; code: string; password: string; confirmPassword: string }>();
    const [mode, setMode] = useState<"login" | "register">("login");
    const [sending, setSending] = useState(false);
    const [submitting, setSubmitting] = useState(false);
    const [cooldown, setCooldown] = useState(0);
    const [oauthPending, setOauthPending] = useState(false);
    const [agreed, setAgreed] = useState(false);
    const [agreements, setAgreements] = useState<HostedAuthAgreements | null>(null);
    const [agreementView, setAgreementView] = useState<string | null>(null);
    const callbackHandled = useRef(false);

    const emailMethod = useMemo(() => methods.find((item) => item.methodType === EMAIL_METHOD), [methods]);
    const phoneMethod = useMemo(() => methods.find((item) => item.methodType === PHONE_METHOD), [methods]);
    const passwordMethod = useMemo(() => methods.find((item) => item.methodType === PASSWORD_METHOD), [methods]);
    const githubMethod = useMemo(() => methods.find((item) => item.methodType === GITHUB_METHOD), [methods]);

    const factors = useMemo<HostedAuthFactor[]>(() => {
        const items: HostedAuthFactor[] = [];
        // 密码优先：它不依赖任何投递通道，是唯一「一定能用」的因子。
        if (passwordMethod) items.push("password");
        if (emailMethod || phoneMethod) items.push("code");
        return items;
    }, [emailMethod, passwordMethod, phoneMethod]);

    const [factor, setFactor] = useState<HostedAuthFactor>("password");
    // 选中态要跟着可用因子走：后台关掉某个通道后若不作废选中态，表单会因为找不到
    // 因子而整块空白，用户只看到「没有可用登录方式」。
    const activeFactor: HostedAuthFactor = factors.includes(factor) ? factor : (factors[0] ?? "password");
    const isPasswordFactor = activeFactor === "password";

    const supportsSignUp = useCallback((value: HostedAuthFactor) => (value === "password" ? Boolean(passwordMethod?.allowSignUp) : Boolean(emailMethod?.allowSignUp || phoneMethod?.allowSignUp)), [emailMethod, passwordMethod, phoneMethod]);

    // 注册模式下因子被换成不允许注册的那条时，退回到还能注册的因子。
    useEffect(() => {
        if (mode !== "register" || supportsSignUp(activeFactor)) return;
        const fallback = factors.find(supportsSignUp);
        if (fallback) setFactor(fallback);
        else setMode("login");
    }, [activeFactor, factors, mode, supportsSignUp]);

    // 密码通道两种形态都收；验证码通道一种形态一条，所以标识能收什么由通道决定。
    const emailAccepted = Boolean(emailMethod) || Boolean(passwordMethod);
    const phoneAccepted = Boolean(phoneMethod) || Boolean(passwordMethod);
    const identityLabel = emailAccepted && phoneAccepted ? "邮箱或手机号" : phoneAccepted ? "手机号" : "邮箱";

    const watchedTarget = Form.useWatch("target", form);
    const rawTarget = String(watchedTarget ?? "").trim();

    /**
     * 验证码发到哪：手机号走短信，其余走邮箱。
     *
     * 只有在标识形态明确、且该形态的通道确实开启时才返回通道——返回 null 就让
     * 「发送验证码」保持禁用。比起让用户点一下再弹「格式不对」，禁用态本身就是说明。
     */
    const codeShape = useMemo<IdentityShape | null>(() => {
        if (isValidPhoneInput(rawTarget)) return phoneMethod ? "phone" : null;
        if (isValidEmailInput(rawTarget)) return emailMethod ? "email" : null;
        return null;
    }, [emailMethod, phoneMethod, rawTarget]);

    const codeMethod = codeShape === "phone" ? phoneMethod : codeShape === "email" ? emailMethod : undefined;
    const activeMethod = isPasswordFactor ? passwordMethod : codeMethod;
    const canSendCode = Boolean(codeMethod) && cooldown <= 0 && !sending;

    useEffect(() => {
        if (cooldown <= 0) return;
        const timer = window.setInterval(() => setCooldown((value) => (value <= 1 ? 0 : value - 1)), 1000);
        return () => window.clearInterval(timer);
    }, [cooldown]);

    // GitHub 回调带回 code/state：这里换会话，成功后再交给门放行。
    useEffect(() => {
        if (callbackHandled.current || typeof window === "undefined") return;
        const params = new URLSearchParams(window.location.search);
        const code = params.get("code");
        const state = params.get("state");
        if (!code || !state) return;
        callbackHandled.current = true;
        void (async () => {
            try {
                const result = await completeHostedOAuthCallback({
                    methodType: GITHUB_METHOD,
                    code,
                    state,
                    redirectUri: oauthRedirectUri(window.location.origin),
                });
                window.history.replaceState(null, "", window.location.pathname);
                onAuthenticated(result.user);
            } catch (error) {
                message.error(error instanceof ApiError ? error.message : "GitHub 登录失败，请重试");
            }
        })();
    }, [message, onAuthenticated]);

    /**
     * 换因子。
     *
     * 只清掉另一个因子的字段（验证码 / 密码），**不动标识**：用户填好的邮箱或手机号
     * 在两个因子里都是同一个东西，切换时抹掉它是最没道理的打断。
     */
    const switchFactor = useCallback(
        (next: HostedAuthFactor) => {
            if (next === activeFactor) return;
            setFactor(next);
            if (next === "password") form.setFieldValue("code", "");
            else form.setFieldValue("password", "");
            setCooldown(0);
        },
        [activeFactor, form],
    );

    const handleSendCode = useCallback(async () => {
        const target = String(form.getFieldValue("target") ?? "").trim();
        if (!codeMethod || !target) {
            message.warning(`请先填写正确的${identityLabel}`);
            return;
        }
        setSending(true);
        try {
            const challenge = await sendHostedAuthCode(codeMethod.methodType, target);
            setCooldown(challenge.cooldownSeconds || 60);
            const devHint = resolveDevCodeHint(challenge);
            if (devHint) {
                // 本地没有投递通道，后端把码回显了：直接填进去，省掉去日志里捞。
                form.setFieldValue("code", devHint.code);
                message.info(devHint.message);
            } else {
                message.success(`验证码已发送至 ${challenge.target}，${Math.round(challenge.cooldownSeconds / 60) || 1} 分钟内有效`);
            }
        } catch (error) {
            message.error(error instanceof ApiError ? error.message : "验证码发送失败，请稍后重试");
        } finally {
            setSending(false);
        }
    }, [codeMethod, form, identityLabel, message]);

    const handleSubmit = useCallback(
        async (values: { target: string; code: string; password: string }) => {
            const methodType = isPasswordFactor ? passwordMethod?.methodType : codeMethod?.methodType;
            if (!methodType) return;
            setSubmitting(true);
            try {
                const result = await loginHostedAuth({
                    methodType,
                    target: String(values.target ?? "").trim(),
                    code: String(values.code ?? "").trim(),
                    password: String(values.password ?? ""),
                });
                onAuthenticated(result.user);
            } catch (error) {
                if (shouldOfferRegistration(error)) {
                    if (!supportsSignUp(activeFactor)) {
                        message.error(error instanceof ApiError ? error.message : "该账号不存在，且当前未开放注册");
                        return;
                    }
                    // 未注册不算操作失败：切到注册页并保留已填标识，省掉用户重新输入。
                    setMode("register");
                    message.info(`该${identityLabel}还没有账号，已切到注册。设置密码即可开始`);
                    return;
                }
                message.error(error instanceof ApiError ? error.message : "登录失败，请稍后重试");
            } finally {
                setSubmitting(false);
            }
        },
        [activeFactor, codeMethod, identityLabel, isPasswordFactor, message, onAuthenticated, passwordMethod, supportsSignUp],
    );

    // 进入注册页时才拉协议：登录页不需要为它多打一次请求。
    useEffect(() => {
        if (mode !== "register" || agreements) return;
        let cancelled = false;
        void (async () => {
            try {
                const payload = await fetchHostedAuthAgreements();
                if (!cancelled) setAgreements(payload);
            } catch (error) {
                if (!cancelled) message.error(error instanceof ApiError ? error.message : "协议加载失败，请稍后重试");
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [agreements, message, mode]);

    const handleRegister = useCallback(
        async (values: { target: string; code: string; password: string }) => {
            const methodType = isPasswordFactor ? passwordMethod?.methodType : codeMethod?.methodType;
            if (!methodType) return;
            if (!agreed) {
                message.warning("请先阅读并勾选同意《用户协议》与《隐私政策》");
                return;
            }
            if (!agreements) {
                message.warning("协议还没加载完成，请稍后重试");
                return;
            }
            setSubmitting(true);
            try {
                const result = await registerHostedAuth({
                    methodType,
                    target: String(values.target ?? "").trim(),
                    code: String(values.code ?? "").trim(),
                    password: String(values.password ?? ""),
                    agreementVersion: agreements.version,
                });
                onAuthenticated(result.user);
            } catch (error) {
                message.error(error instanceof ApiError ? error.message : "注册失败，请稍后重试");
            } finally {
                setSubmitting(false);
            }
        },
        [agreed, agreements, codeMethod, isPasswordFactor, message, onAuthenticated, passwordMethod],
    );

    const handleGithubLogin = useCallback(
        async (assign: LocationAssigner = defaultAssign) => {
            setOauthPending(true);
            try {
                const authorized = await requestHostedOAuthAuthorize({
                    methodType: GITHUB_METHOD,
                    redirectUri: oauthRedirectUri(window.location.origin),
                });
                assign(authorized.authUrl);
            } catch (error) {
                setOauthPending(false);
                message.error(error instanceof ApiError ? error.message : "GitHub 登录暂不可用");
            }
        },
        [message],
    );

    /** 标识字段的三种口径：邮箱、手机号、两者皆可（密码通道都收）。 */
    const targetFieldRules = useMemo(() => {
        const validate = (_rule: unknown, value: unknown) => {
            const raw = String(value ?? "").trim();
            if (!raw) return Promise.reject(new Error(`请输入${identityLabel}`));
            const phone = isValidPhoneInput(raw);
            const email = !phone && isValidEmailInput(raw);
            if (!phone && !email) {
                return Promise.reject(new Error(emailAccepted && phoneAccepted ? "请输入正确的邮箱或手机号" : phoneAccepted ? "请输入 11 位手机号" : "请输入正确的邮箱"));
            }
            if (phone && !phoneAccepted) return Promise.reject(new Error("当前不支持手机号登录"));
            if (email && !emailAccepted) return Promise.reject(new Error("当前不支持邮箱登录"));
            // 验证码通道按形态寻址：形态对应的通道没开时，别放行到一次必然失败的提交。
            if (!isPasswordFactor) {
                if (phone && !phoneMethod) return Promise.reject(new Error("当前不支持手机号验证码登录"));
                if (email && !emailMethod) return Promise.reject(new Error("当前不支持邮箱验证码登录"));
            }
            return Promise.resolve();
        };
        return [{ required: true, message: `请输入${identityLabel}` }, { validator: validate }];
    }, [emailAccepted, emailMethod, identityLabel, isPasswordFactor, phoneAccepted, phoneMethod]);

    const identityProps =
        phoneAccepted && !emailAccepted
            ? { autoComplete: "tel", inputMode: "tel" as const, maxLength: 20, placeholder: "138 0013 8000" }
            : emailAccepted && !phoneAccepted
              ? { autoComplete: "username", inputMode: "email" as const, maxLength: 64, placeholder: "you@example.com" }
              : { autoComplete: "username", inputMode: "text" as const, maxLength: 64, placeholder: "you@example.com 或 138 0013 8000" };

    const passwordFieldRules = [
        { required: true, message: "请输入密码" },
        {
            validator: (_rule: unknown, value: unknown) => (isValidPasswordInput(String(value ?? "")) ? Promise.resolve() : Promise.reject(new Error("密码需 8-64 位，且同时包含字母和数字"))),
        },
    ];

    // 注册模式下只暴露能注册的因子：切到一个必然被退回的按钮比不出现更差。
    const canSignUp = supportsSignUp("password") || supportsSignUp("code");
    const canSwitchFactor = useCallback((target: HostedAuthFactor) => factors.includes(target) && (mode === "login" || supportsSignUp(target)), [factors, mode, supportsSignUp]);
    const showGithub = Boolean(githubMethod) && mode === "login";
    const hasAnyMethod = factors.length > 0 || showGithub;

    // 副标题只描述「现在真的能用的方式」：通道被后台关掉后，这里不能还留着密码或验证码的字样。
    const subtitle = (() => {
        if (!hasAnyMethod) return "当前没有可用的登录方式，请联系管理员在后台开启。";
        if (mode === "register") {
            return isPasswordFactor && passwordMethod ? "设置密码即可开始，注册即代表同意下方协议" : `验证码将发送到你的${identityLabel}`;
        }
        const codeText = `${identityLabel}验证码`;
        if (isPasswordFactor && passwordMethod) return showGithub ? "使用密码或 GitHub 账号登录" : "使用密码登录";
        if (!isPasswordFactor && (emailMethod || phoneMethod)) return showGithub ? `使用${codeText}或 GitHub 账号登录` : `使用${codeText}登录`;
        return showGithub ? "使用 GitHub 账号登录" : "使用账号登录";
    })();

    return (
        // 场景底色恒为暗色，所以这里必须换到 antd 的暗色算法；否则浅色主题下
        // 输入框、下拉、分割线会以白底渲染在深色底上，整块控件像没加载出来。
        // 主按钮刻意走「白底深字」：整块界面只有它一个高对比动作，登录入口一眼可点，
        // 也就不会退化成满屏蓝紫色控件的默认后台脸。
        <ConfigProvider
            theme={{
                algorithm: antdTheme.darkAlgorithm,
                token: {
                    fontFamily: "var(--font-sans)",
                    fontSize: 13,
                    borderRadius: 12,
                    controlHeightLG: 46,
                    colorPrimary: "#ffffff",
                    colorPrimaryHover: "#e6e9ee",
                    colorPrimaryActive: "#d5d9e0",
                    colorBgContainer: "rgba(255,255,255,0.035)",
                    colorBorder: "rgba(255,255,255,0.13)",
                    colorText: "#f4f5f7",
                    colorTextPlaceholder: "rgba(255,255,255,0.28)",
                    colorSplit: "rgba(255,255,255,0.1)",
                },
                components: {
                    Button: {
                        primaryColor: "#0b0c10",
                        fontWeight: 600,
                        defaultBg: "rgba(255,255,255,0.04)",
                        defaultBorderColor: "rgba(255,255,255,0.14)",
                        defaultColor: "#f4f5f7",
                    },
                    Input: {
                        activeBorderColor: "#93c5fd",
                        hoverBorderColor: "rgba(255,255,255,0.22)",
                        activeShadow: "0 0 0 3px rgba(147,197,253,0.14)",
                    },
                    // 主色是白色，勾选框得单独给强调色，否则白底白勾看不见。
                    Checkbox: { colorPrimary: "#93c5fd" },
                },
            }}
        >
            <div className="auth-scene grid min-h-screen grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,462px)]">
                {/* 品牌区只在 lg 以上出现：移动端不放宣传视频，省流量也少一种加载失败。 */}
                <aside className="auth-scene-hero hidden flex-col justify-between gap-8 px-12 py-10 lg:flex">
                    <header className="auth-brand-row flex items-center gap-[11px]">
                        <BrandLogoFrame
                            className="grid size-8 shrink-0 place-items-center"
                            logoClassName="size-8"
                            alt=""
                            theme="dark"
                            fallback={<span className="grid size-8 place-items-center rounded-[var(--r-sm)] bg-white/10 text-[13px] font-semibold text-white/80">K</span>}
                        />
                        <span className="auth-brand-text">
                            <strong className="auth-wordmark">{appearance.brandName}</strong>
                            <span className="auth-mono auth-mono--dim">{brandStudioLabel(appearance)}</span>
                        </span>
                    </header>

                    <div className="auth-hero-body">
                        <p className="auth-mono auth-mono--accent">AI 影视创作工作台</p>
                        <h1 className="auth-hero-title">{appearance.authHeroTitle}</h1>
                        {appearance.authHeroDescription ? <p className="auth-hero-desc">{appearance.authHeroDescription}</p> : null}

                        {/* 取景框：有宣传片就放宣传片，没配就放一张真实的画布界面。
                            空着这一栏比放任何东西都更像坏了——这是登录页唯一的说服力来源。 */}
                        <figure className="auth-monitor">
                            <figcaption className="auth-monitor-bar">
                                <span className="auth-mono">{appearance.authVideoConfigured ? "SHOWREEL" : "CANVAS"}</span>
                                <span className="auth-monitor-rule" aria-hidden />
                                <span className="auth-mono auth-mono--dim">{appearance.authVideoConfigured ? "宣传片" : "自由画布"}</span>
                                <span className="auth-monitor-signal" aria-hidden />
                            </figcaption>
                            <div className="auth-monitor-frame">
                                {appearance.authVideoConfigured ? (
                                    <video
                                        className="auth-monitor-media"
                                        src={appearance.authVideoUrl}
                                        poster={appearance.authVideoPosterUrl || undefined}
                                        autoPlay={appearance.authVideoAutoplay}
                                        muted
                                        loop
                                        playsInline
                                        aria-label={`${appearance.brandName} 宣传片`}
                                    />
                                ) : (
                                    <img className="auth-monitor-media" src="/welcome/workbench-canvas.webp" alt="画布工作台" />
                                )}
                                <span className="auth-bracket is-tl" aria-hidden />
                                <span className="auth-bracket is-tr" aria-hidden />
                                <span className="auth-bracket is-bl" aria-hidden />
                                <span className="auth-bracket is-br" aria-hidden />
                            </div>
                        </figure>
                    </div>

                    {/* 备案号必须出现在登录页上：它是「本站已备案」的对外声明。 */}
                    <SiteComplianceFooter variant="auth" className="auth-compliance !p-0" />
                </aside>

                <div className="auth-scene-form-pane relative flex flex-col items-center justify-center px-5 py-10 sm:px-10">
                    {/* 移动端没有左栏，品牌行收进表单上方：登录页必须自证是哪家的。 */}
                    <header className="auth-brand-row mb-6 flex items-center gap-[11px] lg:hidden">
                        <BrandLogoFrame
                            className="grid size-7 shrink-0 place-items-center"
                            logoClassName="size-7"
                            alt=""
                            theme="dark"
                            fallback={<span className="grid size-7 place-items-center rounded-[var(--r-sm)] bg-white/10 text-[12px] font-semibold text-white/80">K</span>}
                        />
                        <span className="auth-brand-text">
                            <strong className="auth-wordmark">{appearance.brandName}</strong>
                            <span className="auth-mono auth-mono--dim">{brandStudioLabel(appearance)}</span>
                        </span>
                    </header>

                    <div className="auth-card">
                        <header className="auth-card-head">
                            <span className="auth-mono auth-mono--accent">{mode === "login" ? "SIGN IN" : "CREATE ACCOUNT"}</span>
                            <h2 className="auth-title">{mode === "login" ? `登录 ${appearance.brandName}` : "创建账号"}</h2>
                            <p className="auth-subtitle">{subtitle}</p>
                        </header>

                        {hasAnyMethod ? (
                            <Form form={form} layout="vertical" onFinish={mode === "register" ? handleRegister : handleSubmit} requiredMark={false} disabled={submitting}>
                                <Form.Item name="target" label={<span className="auth-field-label">{identityLabel}</span>} rules={targetFieldRules}>
                                    <Input size="large" data-testid="hosted-auth-identity" {...identityProps} />
                                </Form.Item>

                                {isPasswordFactor ? (
                                    <>
                                        <Form.Item
                                            name="password"
                                            label={
                                                <span className="auth-field-label-row">
                                                    <span className="auth-field-label">密码</span>
                                                    {canSwitchFactor("code") ? (
                                                        <button
                                                            type="button"
                                                            className="auth-field-action"
                                                            data-testid="hosted-auth-factor-switch"
                                                            onClick={(event) => {
                                                                // 标签内的按钮默认会连带聚焦输入框，这里只做切换。
                                                                event.preventDefault();
                                                                event.stopPropagation();
                                                                switchFactor("code");
                                                            }}
                                                        >
                                                            用验证码登录
                                                        </button>
                                                    ) : null}
                                                </span>
                                            }
                                            rules={passwordFieldRules}
                                        >
                                            <Input.Password
                                                size="large"
                                                autoComplete={mode === "register" ? "new-password" : "current-password"}
                                                maxLength={64}
                                                placeholder={mode === "register" ? "8-64 位，含字母和数字" : "请输入密码"}
                                                data-testid="hosted-auth-password"
                                            />
                                        </Form.Item>
                                        {mode === "register" ? (
                                            <Form.Item
                                                name="confirmPassword"
                                                label={<span className="auth-field-label">确认密码</span>}
                                                dependencies={["password"]}
                                                rules={[
                                                    { required: true, message: "请再次输入密码" },
                                                    ({ getFieldValue }) => ({
                                                        validator: (_rule: unknown, value: unknown) => (!value || value === getFieldValue("password") ? Promise.resolve() : Promise.reject(new Error("两次输入的密码不一致"))),
                                                    }),
                                                ]}
                                            >
                                                <Input.Password size="large" autoComplete="new-password" maxLength={64} placeholder="请再次输入密码" data-testid="hosted-auth-confirm-password" />
                                            </Form.Item>
                                        ) : null}
                                    </>
                                ) : (
                                    <Form.Item
                                        name="code"
                                        label={
                                            <span className="auth-field-label-row">
                                                <span className="auth-field-label">验证码</span>
                                                {canSwitchFactor("password") ? (
                                                    <button
                                                        type="button"
                                                        className="auth-field-action"
                                                        data-testid="hosted-auth-factor-switch"
                                                        onClick={(event) => {
                                                            event.preventDefault();
                                                            event.stopPropagation();
                                                            switchFactor("password");
                                                        }}
                                                    >
                                                        用密码登录
                                                    </button>
                                                ) : null}
                                            </span>
                                        }
                                        rules={[{ required: true, message: "请输入验证码" }]}
                                    >
                                        <Input
                                            size="large"
                                            autoComplete="one-time-code"
                                            maxLength={6}
                                            placeholder="6 位验证码"
                                            suffix={
                                                <button type="button" className="auth-inline-send" onClick={() => void handleSendCode()} disabled={!canSendCode} data-testid="hosted-auth-send-code">
                                                    {cooldown > 0 ? `${cooldown}s 后重发` : sending ? "发送中…" : "发送验证码"}
                                                </button>
                                            }
                                        />
                                    </Form.Item>
                                )}

                                {mode === "register" ? (
                                    <>
                                        <Form.Item>
                                            <Checkbox checked={agreed} onChange={(event) => setAgreed(event.target.checked)} data-testid="hosted-auth-agreement-checkbox">
                                                我已阅读并同意
                                                <Button
                                                    type="link"
                                                    size="small"
                                                    className="!h-auto !p-0"
                                                    onClick={(event) => {
                                                        // 点协议链接不应顺带勾选/取消勾选：这里只负责打开正文。
                                                        event.preventDefault();
                                                        event.stopPropagation();
                                                        setAgreementView("TERMS");
                                                    }}
                                                >
                                                    《用户协议》
                                                </Button>
                                                与
                                                <Button
                                                    type="link"
                                                    size="small"
                                                    className="!h-auto !p-0"
                                                    onClick={(event) => {
                                                        event.preventDefault();
                                                        event.stopPropagation();
                                                        setAgreementView("PRIVACY");
                                                    }}
                                                >
                                                    《隐私政策》
                                                </Button>
                                            </Checkbox>
                                        </Form.Item>
                                        <Button type="primary" size="large" htmlType="submit" block loading={submitting}>
                                            注册并进入
                                        </Button>
                                    </>
                                ) : (
                                    <Button type="primary" size="large" htmlType="submit" block loading={submitting} className="auth-submit">
                                        登录
                                    </Button>
                                )}
                            </Form>
                        ) : null}

                        {hasAnyMethod && showGithub ? (
                            <>
                                <Divider plain className="auth-divider">
                                    或
                                </Divider>
                                <Button size="large" block icon={<GithubOutlined aria-hidden />} loading={oauthPending} onClick={() => void handleGithubLogin()} data-testid="hosted-auth-github">
                                    使用 GitHub 登录
                                </Button>
                            </>
                        ) : null}

                        {/* 注册入口挂在后端配置上：allow_sign_up 关掉时这里不出现，用户就不会撞上 403。 */}
                        {hasAnyMethod && canSignUp ? (
                            <Typography.Paragraph className="auth-mode-switch">
                                {mode === "register" ? "已有账号？" : "还没有账号？"}
                                <Button type="link" size="small" className="!h-auto !p-0" onClick={() => setMode(mode === "register" ? "login" : "register")} data-testid="hosted-auth-mode-switch">
                                    {mode === "register" ? "去登录" : "立即注册"}
                                </Button>
                            </Typography.Paragraph>
                        ) : null}
                    </div>
                </div>
            </div>
            <HostedAuthAgreementDialog open={agreementView !== null} agreements={agreements} initialType={agreementView} onClose={() => setAgreementView(null)} />
        </ConfigProvider>
    );
}
