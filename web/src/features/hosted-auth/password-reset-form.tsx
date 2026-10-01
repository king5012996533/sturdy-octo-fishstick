import { useCallback, useEffect, useMemo, useState } from "react";
import { App, Button, Form, Input } from "antd";
import { ArrowRightOutlined, LockOutlined, SafetyOutlined, UserOutlined } from "@ant-design/icons";

import { ApiError } from "@/services/api/request";

import { resetHostedAuthPassword, sendPasswordResetCode, type HostedAuthMethod } from "./api";
import { identityShapeOf, isValidEmailInput, isValidPasswordInput, isValidPhoneInput, resolveDevCodeHint } from "./credentials";

/**
 * 「忘记密码」：不依赖旧密码、也不依赖会话的一条路。
 *
 * 它单独成一个组件而不是塞进登录页：登录页那张表单的每个字段都在回答"我有什么凭据"，
 * 而这里回答的是"我还能收到验证码"。把它们并排放在同一个 Form 里，两边的校验规则、
 * 错误文案和提交目标都会互相污染。
 *
 * 验证码走独立场景下发（见后端 passwordResetScene），所以用户在登录页点过一次
 * "发送验证码"之后立刻切到这里，不会被同一分钟的冷却挡住。
 */
export function PasswordResetForm({ methods, initialTarget, onCancel, onDone }: {
    methods: HostedAuthMethod[];
    initialTarget: string;
    onCancel: () => void;
    onDone: (target: string) => void;
}) {
    const { message } = App.useApp();
    const [form] = Form.useForm<{ target: string; code: string; password: string; confirmPassword: string }>();
    const [sending, setSending] = useState(false);
    const [submitting, setSubmitting] = useState(false);
    const [cooldown, setCooldown] = useState(0);
    const [error, setError] = useState("");

    const emailMethod = useMemo(() => methods.find((item) => item.methodType === "EMAIL_CODE"), [methods]);
    const phoneMethod = useMemo(() => methods.find((item) => item.methodType === "PHONE_CODE"), [methods]);
    const identityLabel = emailMethod && phoneMethod ? "邮箱或手机号" : phoneMethod ? "手机号" : "邮箱";
    // 占位符跟着真的能收码的那条通道走：只开邮箱时还写着"或 138 0013 8000"，
    // 用户会先填一个手机号、再被判格式不对。
    const identityPlaceholder = emailMethod && phoneMethod ? "you@example.com 或 138 0013 8000" : phoneMethod ? "138 0013 8000" : "you@example.com";

    const watchedTarget = Form.useWatch("target", form);
    const rawTarget = String(watchedTarget ?? "").trim();
    const shape = identityShapeOf(rawTarget);
    const codeMethod = shape === "phone" ? phoneMethod : shape === "email" ? emailMethod : undefined;
    const canSendCode = Boolean(codeMethod) && cooldown <= 0 && !sending;

    useEffect(() => {
        if (cooldown <= 0) return;
        const timer = window.setInterval(() => setCooldown((value) => (value <= 1 ? 0 : value - 1)), 1000);
        return () => window.clearInterval(timer);
    }, [cooldown]);

    const handleSendCode = useCallback(async () => {
        const target = String(form.getFieldValue("target") ?? "").trim();
        if (!codeMethod || !target) {
            message.warning(`请先填写正确的${identityLabel}`);
            return;
        }
        setSending(true);
        try {
            const challenge = await sendPasswordResetCode(codeMethod.methodType, target);
            setCooldown(challenge.cooldownSeconds || 60);
            const devHint = resolveDevCodeHint(challenge);
            if (devHint) {
                form.setFieldValue("code", devHint.code);
                message.info(devHint.message);
            } else {
                message.success(`验证码已发送至 ${challenge.target || target}`);
            }
        } catch (sendError) {
            message.error(sendError instanceof ApiError ? sendError.message : "验证码发送失败，请稍后重试");
        } finally {
            setSending(false);
        }
    }, [codeMethod, form, identityLabel, message]);

    const handleSubmit = useCallback(async (values: { target: string; code: string; password: string }) => {
        const target = String(values.target ?? "").trim();
        const methodType = identityShapeOf(target) === "phone" ? "PHONE_CODE" : "EMAIL_CODE";
        setSubmitting(true);
        try {
            const result = await resetHostedAuthPassword({ methodType, target, code: String(values.code ?? "").trim(), newPassword: values.password });
            setError("");
            // 重置成功不同时签发会话：新密码是用户刚刚设置的，让他用一次自己的新凭据
            // 登录一遍，才能确认这串密码真的记得住、也真的能进得来。
            const signedOut = result.revokedSessions > 0 ? `，其他 ${result.revokedSessions} 台设备已退出登录` : "";
            message.success(`密码已重置${signedOut}，请用新密码登录`);
            onDone(target);
        } catch (submitError) {
            const text = submitError instanceof ApiError ? submitError.message : "重置密码失败，请稍后重试";
            setError(text);
            message.error(text);
        } finally {
            setSubmitting(false);
        }
    }, [message, onDone]);

    const targetRules = useMemo(() => {
        const validate = (_rule: unknown, value: unknown) => {
            const raw = String(value ?? "").trim();
            if (!raw) return Promise.reject(new Error(`请输入${identityLabel}`));
            if (isValidPhoneInput(raw)) {
                return phoneMethod ? Promise.resolve() : Promise.reject(new Error("当前不支持手机号验证码"));
            }
            if (isValidEmailInput(raw)) {
                return emailMethod ? Promise.resolve() : Promise.reject(new Error("当前不支持邮箱验证码"));
            }
            return Promise.reject(new Error(identityLabel === "邮箱" ? "请输入正确的邮箱" : "请输入正确的邮箱或手机号"));
        };
        return [{ required: true, message: `请输入${identityLabel}` }, { validator: validate }];
    }, [emailMethod, identityLabel, phoneMethod]);

    return (
        <Form
            form={form}
            layout="vertical"
            requiredMark={false}
            className="auth-form"
            disabled={submitting}
            initialValues={{ target: initialTarget }}
            onFinish={handleSubmit}
        >
            <Form.Item name="target" label={<span className="auth-field-label">{identityLabel}</span>} rules={targetRules}>
                <Input size="large" data-testid="hosted-auth-reset-identity" autoComplete="username" maxLength={64} prefix={<UserOutlined aria-hidden />} placeholder={identityPlaceholder} />
            </Form.Item>

            <Form.Item name="code" label={<span className="auth-field-label">验证码</span>} rules={[{ required: true, message: "请输入验证码" }]}>
                <Input
                    size="large"
                    autoComplete="one-time-code"
                    maxLength={6}
                    prefix={<SafetyOutlined aria-hidden />}
                    placeholder="6 位验证码"
                    data-testid="hosted-auth-reset-code"
                    suffix={
                        <button type="button" className="auth-inline-send" onClick={() => void handleSendCode()} disabled={!canSendCode} data-testid="hosted-auth-reset-send-code">
                            {cooldown > 0 ? `${cooldown}s 后重发` : sending ? "发送中…" : "发送验证码"}
                        </button>
                    }
                />
            </Form.Item>

            <Form.Item name="password" label={<span className="auth-field-label">新密码</span>} rules={[{ required: true, message: "请设置新密码" }, { validator: (_rule: unknown, value: unknown) => (isValidPasswordInput(String(value ?? "")) ? Promise.resolve() : Promise.reject(new Error("密码需 8-64 位，且同时包含字母和数字"))) }]}>
                <Input.Password size="large" autoComplete="new-password" maxLength={64} prefix={<LockOutlined aria-hidden />} placeholder="8-64 位，含字母和数字" data-testid="hosted-auth-reset-password" />
            </Form.Item>

            <Form.Item
                name="confirmPassword"
                label={<span className="auth-field-label">确认新密码</span>}
                dependencies={["password"]}
                rules={[
                    { required: true, message: "请再次输入新密码" },
                    ({ getFieldValue }) => ({
                        validator: (_rule: unknown, value: unknown) => (!value || value === getFieldValue("password") ? Promise.resolve() : Promise.reject(new Error("两次输入的密码不一致"))),
                    }),
                ]}
            >
                <Input.Password size="large" autoComplete="new-password" maxLength={64} prefix={<LockOutlined aria-hidden />} placeholder="请再次输入新密码" data-testid="hosted-auth-reset-confirm-password" />
            </Form.Item>

            {error ? <p className="auth-form-error">{error}</p> : null}

            <Button type="primary" size="large" htmlType="submit" block loading={submitting} className="auth-submit" icon={<ArrowRightOutlined aria-hidden />} iconPlacement="end" data-testid="hosted-auth-reset-submit">
                重置密码
            </Button>

            <p className="auth-mode-switch">
                想起密码了？
                <Button type="link" size="small" className="!h-auto !p-0" onClick={onCancel} data-testid="hosted-auth-reset-cancel">
                    去登录
                </Button>
            </p>
        </Form>
    );
}
