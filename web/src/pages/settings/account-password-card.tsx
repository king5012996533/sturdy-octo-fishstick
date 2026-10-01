import { App, Button, Input } from "antd";
import { useCallback, useEffect, useState } from "react";

import { resetHostedAuthPassword, sendHostedAuthCode, sendPasswordResetCode } from "@/features/hosted-auth/api";
import { getAccountBindings, type AccountBindings } from "@/services/api/account-bindings";
import { changeAccountPassword, getPasswordState, setAccountPassword, type PasswordState } from "@/services/api/account-security";
import { ApiError } from "@/services/api/request";

/**
 * 「密码」：没有密码的设一个，有密码的改一个，忘了的用验证码重来一个。
 *
 * 三条分支的证明方式不同，界面照实分开：设置要验证码（有没有密码是账号的既有状态，
 * 不能被一次会话改掉），修改要旧密码，重置要验证码——旧密码已经不在用户手里了。
 *
 * 密码状态与绑定关系分成两次请求、各自成败：上一版用 Promise.all 把两者绑在一起，
 * 绑定接口一旦失败（例如后端还没上那组路由），整张卡片就退化成一句"暂时读不到密码状态"
 * ——明明改密码根本不需要绑定接口。能改的那条路不能因为读另外一份数据失败而消失。
 */
export function AccountPasswordCard() {
    const [state, setState] = useState<PasswordState | null>(null);
    const [bindings, setBindings] = useState<AccountBindings | null>(null);
    const [bindingsError, setBindingsError] = useState("");
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    // 忘记密码是"修改"这条路上的一个岔口，不是第四个平级状态：用户先有密码、
    // 再想不起它，顺序不能反过来。
    const [forgot, setForgot] = useState(false);

    const load = useCallback(async () => {
        setLoading(true);
        const [passwordResult, bindingResult] = await Promise.allSettled([getPasswordState(), getAccountBindings()]);
        if (passwordResult.status === "fulfilled") {
            setState(passwordResult.value);
            setError("");
        } else {
            setState(null);
            setError(passwordResult.reason instanceof Error ? passwordResult.reason.message : "读取密码状态失败");
        }
        if (bindingResult.status === "fulfilled") {
            setBindings(bindingResult.value);
            setBindingsError("");
        } else {
            setBindings(null);
            setBindingsError(bindingResult.reason instanceof Error ? bindingResult.reason.message : "读取绑定关系失败");
        }
        setLoading(false);
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const reload = useCallback(() => {
        setForgot(false);
        void load();
    }, [load]);

    const formProps = {
        bindings,
        bindingsError,
        minLength: state?.minLength ?? 8,
        maxLength: state?.maxLength ?? 64,
        onRetryBindings: () => void load(),
        onDone: reload,
    };

    return (
        <section className="account-card" aria-labelledby="account-password-title">
            <div className="account-card-head">
                <h2 id="account-password-title" className="account-card-title">密码</h2>
                <span className="account-card-aside">
                    {state ? (state.hasPassword ? "已设置" : "尚未设置") : loading ? "读取中…" : "读取失败"}
                </span>
            </div>

            {state ? (
                state.hasPassword ? (
                    forgot ? (
                        <CodePasswordForm mode="reset" onCancel={() => setForgot(false)} {...formProps} />
                    ) : (
                        <ChangePasswordForm minLength={state.minLength} maxLength={state.maxLength} onDone={reload} onForgot={() => setForgot(true)} />
                    )
                ) : (
                    <CodePasswordForm mode="set" {...formProps} />
                )
            ) : (
                <>
                    <p className="account-inline-note">{loading ? "正在读取密码状态…" : "暂时读不到密码状态。"}</p>
                    {error ? <p className="account-error">{error}</p> : null}
                    {!loading && error ? <Button size="small" onClick={() => void load()}>重试</Button> : null}
                </>
            )}
        </section>
    );
}

type CodeFormMode = "set" | "reset";

/** 验证码换密码：首次设置（set）与忘了旧密码的重置（reset）走同一张表单。 */
function CodePasswordForm({ mode, bindings, bindingsError, minLength, maxLength, onRetryBindings, onDone, onCancel }: {
    mode: CodeFormMode;
    bindings: AccountBindings | null;
    bindingsError: string;
    minLength: number;
    maxLength: number;
    onRetryBindings: () => void;
    onDone: () => void;
    onCancel?: () => void;
}) {
    const { message } = App.useApp();
    const [code, setCode] = useState("");
    const [password, setPassword] = useState("");
    const [sending, setSending] = useState(false);
    const [submitting, setSubmitting] = useState(false);
    const [cooldown, setCooldown] = useState(0);

    useEffect(() => {
        if (cooldown <= 0) return;
        const timer = window.setTimeout(() => setCooldown((value) => value - 1), 1000);
        return () => window.clearTimeout(timer);
    }, [cooldown]);

    const channel = bindings?.email
        ? { methodType: "EMAIL_CODE" as const, label: "邮箱", target: bindings.email }
        : bindings?.phone
          ? { methodType: "PHONE_CODE" as const, label: "手机号", target: bindings.phone }
          : null;

    const sendCode = async () => {
        if (!channel) return;
        setSending(true);
        try {
            // 重置走独立场景下发：与登录验证码共用一条端点的话，用户刚在别处点过发送，
            // 这里会被同一分钟的冷却挡住，而他能看到的只有"发送过于频繁"。
            const challenge = mode === "reset"
                ? await sendPasswordResetCode(channel.methodType, channel.target)
                : await sendHostedAuthCode(channel.methodType, channel.target);
            setCooldown(challenge?.cooldownSeconds && challenge.cooldownSeconds > 0 ? challenge.cooldownSeconds : 60);
            message.success(challenge?.devCode ? `验证码已发送（联调回显：${challenge.devCode}）` : `验证码已发送至 ${channel.target}`);
        } catch (sendError) {
            message.error(sendError instanceof ApiError ? sendError.message : "验证码发送失败，请稍后重试");
        } finally {
            setSending(false);
        }
    };

    const submit = async () => {
        if (!channel) return;
        setSubmitting(true);
        try {
            const input = { methodType: channel.methodType, target: channel.target, code: code.trim(), newPassword: password };
            const result = mode === "reset" ? await resetHostedAuthPassword(input) : await setAccountPassword(input);
            const done = mode === "reset" ? "密码已重置" : "密码已设置";
            message.success(result.revokedSessions > 0 ? `${done}，并从其他 ${result.revokedSessions} 台设备退出了登录` : done);
            setCode("");
            setPassword("");
            onDone();
        } catch (submitError) {
            message.error(submitError instanceof ApiError ? submitError.message : "保存新密码失败，请稍后重试");
        } finally {
            setSubmitting(false);
        }
    };

    // 读不到绑定关系时给的是重试，不是结论：这条失败原因和"我没绑过任何联系方式"完全是两回事。
    if (!channel) {
        return (
            <>
                <p className="account-inline-note">
                    {bindingsError
                        ? `暂时读不到这个账号绑定的邮箱与手机号，${mode === "reset" ? "重置" : "设置"}密码需要验证码。`
                        : `这个账号还没有绑定邮箱或手机号，先在「更多设置 → 身份绑定」里补一个，再回来${mode === "reset" ? "重置" : "设置"}密码。`}
                </p>
                {bindingsError ? <p className="account-error">{bindingsError}</p> : null}
                {bindingsError ? <Button size="small" onClick={onRetryBindings}>重试</Button> : null}
            </>
        );
    }

    return (
        <div className="account-form-narrow flex flex-col">
            <p className="account-inline-note" style={{ marginTop: 12 }}>
                {mode === "reset"
                    ? `验证码会发到 ${channel.label} ${channel.target}；改完当前设备保持登录，其他设备会被退出。`
                    : `设置密码后就能在收不到验证码的设备上登录。验证码会发到 ${channel.label} ${channel.target}，设置成功后其他设备会被退出登录。`}
            </p>
            <div className="account-field">
                <span className="account-field-label">验证码</span>
                <div className="flex items-center gap-2">
                    <Input value={code} maxLength={6} placeholder="6 位数字" onChange={(event) => setCode(event.target.value)} />
                    <Button loading={sending} disabled={cooldown > 0} onClick={() => void sendCode()}>
                        {cooldown > 0 ? `${cooldown}s` : "发送验证码"}
                    </Button>
                </div>
            </div>
            <label className="account-field">
                <span className="account-field-label">新密码（至少 {minLength} 位）</span>
                <Input.Password value={password} maxLength={maxLength} onChange={(event) => setPassword(event.target.value)} />
            </label>
            <div className="account-credits-actions">
                <Button type="primary" loading={submitting} disabled={!code.trim() || !password} onClick={() => void submit()}>
                    {mode === "reset" ? "重置密码" : "设置密码"}
                </Button>
                {onCancel ? (
                    <Button className="account-quiet-button" type="link" size="small" onClick={onCancel}>
                        返回
                    </Button>
                ) : null}
            </div>
        </div>
    );
}

/** 已有密码时改密：旧密码即证明，不再要验证码。 */
function ChangePasswordForm({ minLength, maxLength, onDone, onForgot }: {
    minLength: number;
    maxLength: number;
    onDone: () => void;
    onForgot: () => void;
}) {
    const { message } = App.useApp();
    const [current, setCurrent] = useState("");
    const [next, setNext] = useState("");
    const [submitting, setSubmitting] = useState(false);

    const submit = async () => {
        setSubmitting(true);
        try {
            const result = await changeAccountPassword({ currentPassword: current, newPassword: next });
            message.success(result.revokedSessions > 0 ? `密码已更新，并从其他 ${result.revokedSessions} 台设备退出了登录` : "密码已更新");
            setCurrent("");
            setNext("");
            onDone();
        } catch (submitError) {
            message.error(submitError instanceof ApiError ? submitError.message : "修改密码失败，请稍后重试");
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <div className="account-form-narrow flex flex-col">
            <p className="account-inline-note" style={{ marginTop: 12 }}>
                改完当前设备保持登录，其他设备会被退出。
            </p>
            <label className="account-field">
                <span className="account-field-label">当前密码</span>
                <Input.Password value={current} onChange={(event) => setCurrent(event.target.value)} />
            </label>
            <label className="account-field">
                <span className="account-field-label">新密码（至少 {minLength} 位）</span>
                <Input.Password value={next} maxLength={maxLength} onChange={(event) => setNext(event.target.value)} />
            </label>
            <div className="account-credits-actions">
                <Button type="primary" loading={submitting} disabled={!current || !next} onClick={() => void submit()}>
                    更新密码
                </Button>
                {/* 忘记旧密码就从这里走验证码：这条岔路必须贴着"当前密码"输入框，
                    否则用户只会去登录页找它——而他在用户中心里本来就是登录状态。 */}
                <Button className="account-quiet-button" type="link" size="small" onClick={onForgot} data-testid="account-password-forgot">
                    忘记当前密码
                </Button>
            </div>
        </div>
    );
}
