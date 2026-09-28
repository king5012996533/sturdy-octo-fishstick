import { Button, Input, Switch, Tag } from "antd";
import { Mail, RefreshCw, Save, Send, Smartphone } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import {
    getAdminGateways,
    testAdminGateway,
    updateAdminGateway,
    type AdminGatewayChannel,
    type AdminGatewayChannelKey,
    type AdminGateways,
    type AdminSMSGateway,
    type AdminSMTPGateway,
} from "./api";

const sourceLabels: Record<string, string> = {
    database: "后台配置",
    environment: "环境变量",
    console: "仅写日志",
};

/** 空表单的默认值：端口与区域是阿里云/主流 SMTP 的惯例值，减少手填出错。 */
const emptySMTP: AdminSMTPGateway = { host: "", port: 587, username: "", from: "", fromName: "" };
const emptySMS: AdminSMSGateway = { accessKeyId: "", signName: "", templateCode: "SMS_", templateParamKey: "code", regionId: "cn-hangzhou", endpoint: "dysmsapi.aliyuncs.com" };

type TestResult = { ok: boolean; message: string } | null;

function StatusRow({ view }: { view: AdminGatewayChannel }) {
    return (
        <div className="admin-gateway-status">
            <Tag color={view.source === "database" ? "geekblue" : view.source === "environment" ? "gold" : "default"}>
                {sourceLabels[view.source] ?? view.source}
            </Tag>
            <Tag color={view.ready ? "green" : view.enabled ? "orange" : "default"}>
                {view.ready ? "可投递" : view.enabled ? "已启用但配置不完整" : "未启用"}
            </Tag>
            <span className="admin-gateway-detail">{view.detail || "—"}</span>
        </div>
    );
}

function TestRow({
    channel,
    placeholder,
    result,
    onTest,
}: {
    channel: AdminGatewayChannelKey;
    placeholder: string;
    result: TestResult;
    onTest: (target: string) => Promise<void>;
}) {
    const [target, setTarget] = useState("");
    const [sending, setSending] = useState(false);

    const run = async () => {
        setSending(true);
        try {
            await onTest(target);
        } finally {
            setSending(false);
        }
    };

    return (
        <div className="admin-gateway-test">
            <Input value={target} placeholder={placeholder} onChange={(event) => setTarget(event.target.value)} onPressEnter={() => void run()} />
            <Button icon={<Send className="size-3.5" />} loading={sending} onClick={() => void run()} data-testid={`gateway-test-${channel}`}>
                测试发送
            </Button>
            {result ? (
                <span className={`admin-gateway-test-result${result.ok ? " is-ok" : " is-error"}`}>{result.message}</span>
            ) : null}
        </div>
    );
}

/**
 * 验证码投递网关。
 *
 * 这一页解决的是"验证码到底发不发得出去"：`source` 会明确告诉运营当前走的是后台配置、
 * 环境变量还是只写日志，否则最容易出现的故障是后台看起来配好了、用户却收不到码。
 * 密钥只写不读，留空保存即保持原值。
 */
export function GatewaysPane() {
    const [gateways, setGateways] = useState<AdminGateways | null>(null);
    const [smtp, setSmtp] = useState<AdminSMTPGateway>(emptySMTP);
    const [sms, setSms] = useState<AdminSMSGateway>(emptySMS);
    const [smtpSecret, setSmtpSecret] = useState("");
    const [smsSecret, setSmsSecret] = useState("");
    const [smtpEnabled, setSmtpEnabled] = useState(false);
    const [smsEnabled, setSmsEnabled] = useState(false);
    const [loading, setLoading] = useState(true);
    const [busy, setBusy] = useState<AdminGatewayChannelKey | "">("");
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [smtpResult, setSmtpResult] = useState<TestResult>(null);
    const [smsResult, setSmsResult] = useState<TestResult>(null);

    const apply = useCallback((payload: AdminGateways) => {
        setGateways(payload);
        // 密钥永不回显：表单里这两个字段始终从空开始，避免把占位串当成真密钥提交。
        setSmtp({ ...emptySMTP, ...(payload.smtp.smtp ?? {}) });
        setSms({ ...emptySMS, ...(payload.sms.sms ?? {}) });
        setSmtpSecret("");
        setSmsSecret("");
        setSmtpEnabled(payload.smtp.enabled);
        setSmsEnabled(payload.sms.enabled);
    }, []);

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            apply(await getAdminGateways());
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载网关配置失败");
        } finally {
            setLoading(false);
        }
    }, [apply]);

    useEffect(() => {
        void load();
    }, [load]);

    const save = async (channel: AdminGatewayChannelKey) => {
        setBusy(channel);
        setError("");
        setNotice("");
        try {
            const payload =
                channel === "SMTP"
                    ? await updateAdminGateway("SMTP", { enabled: smtpEnabled, smtp: { ...smtp, password: smtpSecret.trim() || undefined } })
                    : await updateAdminGateway("SMS", { enabled: smsEnabled, sms: { ...sms, accessKeySecret: smsSecret.trim() || undefined } });
            apply(payload);
            setNotice(channel === "SMTP" ? "邮件通道配置已保存。" : "短信通道配置已保存。");
        } catch (saveError) {
            setError(saveError instanceof Error ? saveError.message : "保存网关配置失败");
        } finally {
            setBusy("");
        }
    };

    const test = async (channel: AdminGatewayChannelKey, target: string) => {
        const setResult = channel === "SMTP" ? setSmtpResult : setSmsResult;
        setResult(null);
        try {
            const payload = await testAdminGateway(channel, target);
            setResult({ ok: true, message: payload.message || "测试验证码已发出" });
        } catch (testError) {
            setResult({ ok: false, message: testError instanceof Error ? testError.message : "测试发送失败" });
        }
    };

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">验证码网关</h2>
                    <p className="admin-section-desc">邮箱与短信验证码的投递通道。密钥只写不读：留空保存表示保持原值；保存后下一次发送验证码即按新配置投递，无需重启服务。</p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>刷新</Button>
            </div>

            {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}
            {notice ? <div className="admin-notice is-ok"><span>{notice}</span></div> : null}

            <section className="admin-card admin-settings-section">
                <div className="admin-section-head">
                    <h3 className="admin-settings-title"><Mail className="size-3.5" />邮件通道（SMTP）</h3>
                    <div className="admin-settings-inline">
                        <span>启用</span>
                        <Switch checked={smtpEnabled} onChange={setSmtpEnabled} />
                    </div>
                </div>
                {gateways ? <StatusRow view={gateways.smtp} /> : null}
                <div className="admin-gateway-grid">
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">SMTP 服务器</span>
                        <Input placeholder="smtp.example.com" value={smtp.host} onChange={(event) => setSmtp({ ...smtp, host: event.target.value })} />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">端口</span>
                        <Input
                            inputMode="numeric"
                            value={String(smtp.port || "")}
                            onChange={(event) => setSmtp({ ...smtp, port: Number(event.target.value.replace(/\D/g, "")) || 0 })}
                        />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">账号</span>
                        <Input value={smtp.username} onChange={(event) => setSmtp({ ...smtp, username: event.target.value })} />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">密码 / 授权码</span>
                        <Input.Password
                            value={smtpSecret}
                            placeholder={gateways?.smtp.smtp?.hasPassword ? "已设置，留空表示不修改" : "未设置"}
                            onChange={(event) => setSmtpSecret(event.target.value)}
                        />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">发件人地址</span>
                        <Input placeholder="noreply@example.com" value={smtp.from} onChange={(event) => setSmtp({ ...smtp, from: event.target.value })} />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">发件人名称</span>
                        <Input value={smtp.fromName} onChange={(event) => setSmtp({ ...smtp, fromName: event.target.value })} />
                    </label>
                </div>
                <div className="admin-settings-inline">
                    <Button type="primary" icon={<Save className="size-3.5" />} loading={busy === "SMTP"} onClick={() => void save("SMTP")}>保存邮件通道</Button>
                    <TestRow channel="SMTP" placeholder="接收测试码的邮箱" result={smtpResult} onTest={(target) => test("SMTP", target)} />
                </div>
            </section>

            <section className="admin-card admin-settings-section">
                <div className="admin-section-head">
                    <h3 className="admin-settings-title"><Smartphone className="size-3.5" />短信通道（阿里云）</h3>
                    <div className="admin-settings-inline">
                        <span>启用</span>
                        <Switch checked={smsEnabled} onChange={setSmsEnabled} />
                    </div>
                </div>
                {gateways ? <StatusRow view={gateways.sms} /> : null}
                <div className="admin-gateway-grid">
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">AccessKey ID</span>
                        <Input value={sms.accessKeyId} onChange={(event) => setSms({ ...sms, accessKeyId: event.target.value })} />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">AccessKey Secret</span>
                        <Input.Password
                            value={smsSecret}
                            placeholder={gateways?.sms.sms?.hasAccessKeySecret ? "已设置，留空表示不修改" : "未设置"}
                            onChange={(event) => setSmsSecret(event.target.value)}
                        />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">短信签名</span>
                        <Input value={sms.signName} onChange={(event) => setSms({ ...sms, signName: event.target.value })} />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">模板编号</span>
                        <Input placeholder="SMS_123456789" value={sms.templateCode} onChange={(event) => setSms({ ...sms, templateCode: event.target.value })} />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">验证码参数名</span>
                        <Input placeholder="code" value={sms.templateParamKey} onChange={(event) => setSms({ ...sms, templateParamKey: event.target.value })} />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">区域</span>
                        <Input value={sms.regionId} onChange={(event) => setSms({ ...sms, regionId: event.target.value })} />
                    </label>
                    <label className="admin-settings-field">
                        <span className="admin-settings-field-label">接入地址</span>
                        <Input value={sms.endpoint} onChange={(event) => setSms({ ...sms, endpoint: event.target.value })} />
                    </label>
                </div>
                <div className="admin-settings-inline">
                    <Button type="primary" icon={<Save className="size-3.5" />} loading={busy === "SMS"} onClick={() => void save("SMS")}>保存短信通道</Button>
                    <TestRow channel="SMS" placeholder="接收测试码的手机号" result={smsResult} onTest={(target) => test("SMS", target)} />
                </div>
            </section>
        </div>
    );
}
