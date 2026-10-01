import { App, Button, Input, Modal, Tag } from "antd";
import { Link2, RefreshCw, ShieldCheck } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { confirmBinding, getAccountBindings, sendBindingCode, type AccountBindings } from "@/services/api/account-bindings";
import { ApiError } from "@/services/api/request";

/**
 * 「身份绑定」：看得见自己绑了什么，换得掉绑定用的邮箱与手机号。
 *
 * 换绑是两步：给新地址发码 → 带码确认。少了第一步，一次手误就能把账号绑到一个永远
 * 收不到验证码的地址上，而那种状态下用户连注销都做不了（注销也要验证码）。
 *
 * 换绑成功后服务端会给**旧地址**发一条通知，卡片里明说这件事：真正被搬走账号的人
 * 靠的就是这条通知，界面不写出来，用户不会知道自己该去查旧邮箱。
 */
type Channel = "EMAIL" | "PHONE";

const channelCopy: Record<Channel, { noun: string; label: string; placeholder: string; methodType: "EMAIL_CODE" | "PHONE_CODE" }> = {
    EMAIL: { noun: "邮箱", label: "新邮箱地址", placeholder: "name@example.com", methodType: "EMAIL_CODE" },
    PHONE: { noun: "手机号", label: "新手机号", placeholder: "13800138000", methodType: "PHONE_CODE" },
};

export function AccountBindingsCard() {
    const [bindings, setBindings] = useState<AccountBindings | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [editing, setEditing] = useState<Channel | null>(null);

    const load = useCallback(async () => {
        setLoading(true);
        try {
            setBindings(await getAccountBindings());
            setError("");
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "读取绑定关系失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    return (
        <div className="settings-pane mx-auto mt-4 w-full max-w-2xl">
            <div className="settings-pane-header">
                <div className="min-w-0">
                    <h2>身份绑定</h2>
                    <p>绑定的邮箱与手机号既是登录方式，也是安全通知的送达地址；换绑后原地址会收到一条变更提醒。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button icon={<RefreshCw className="size-4" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                </div>
            </div>

            {error ? (
                <div className="account-notice is-error">
                    <span>{error}</span>
                </div>
            ) : null}

            <section className="account-block">
                <h3 className="account-block-title">联系地址</h3>
                <div className="account-kv">
                    <div>
                        <dt>邮箱</dt>
                        <dd className="flex items-center gap-2">
                            <span>{bindings?.email || "未绑定"}</span>
                            {/* 未绑定时要的是「绑定」而不是「换绑」：把首次绑定说成换绑，
                                用户会以为自己漏了一步，也会以为原地址会收到一条不存在的提醒。 */}
                            <Button size="small" type="link" onClick={() => setEditing("EMAIL")}>
                                {bindings?.email ? "换绑" : "绑定"}
                            </Button>
                        </dd>
                    </div>
                    <div>
                        <dt>手机号</dt>
                        <dd className="flex items-center gap-2">
                            <span>{bindings?.phone || "未绑定"}</span>
                            <Button size="small" type="link" onClick={() => setEditing("PHONE")}>
                                {bindings?.phone ? "换绑" : "绑定"}
                            </Button>
                        </dd>
                    </div>
                </div>
            </section>

            <section className="account-block">
                <h3 className="account-block-title">登录方式</h3>
                {bindings && bindings.bindings.length > 0 ? (
                    <ul className="account-agreements">
                        {bindings.bindings.map((binding) => (
                            <li key={`${binding.methodType}-${binding.identifier}`} className="flex items-center gap-2">
                                <Link2 className="size-3.5" aria-hidden />
                                <b className="truncate">{binding.label}</b>
                                <span className="account-sub truncate">{binding.identifier}</span>
                                {binding.verified ? (
                                    <Tag icon={<ShieldCheck className="size-3" />} color="green">
                                        已验证
                                    </Tag>
                                ) : (
                                    <Tag>未验证</Tag>
                                )}
                                {binding.primary ? <Tag color="blue">联系地址</Tag> : null}
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p className="account-sub">{loading ? "正在读取绑定关系…" : "还没有任何登录方式绑定在这个账号上。"}</p>
                )}
            </section>

            {editing ? (
                <BindingChangeModal
                    channel={editing}
                    current={editing === "PHONE" ? bindings?.phone ?? "" : bindings?.email ?? ""}
                    bound={Boolean(editing === "PHONE" ? bindings?.phone : bindings?.email)}
                    onClose={() => setEditing(null)}
                    onDone={async () => {
                        setEditing(null);
                        await load();
                    }}
                />
            ) : null}
        </div>
    );
}

/** 绑定 / 换绑弹窗：新地址 + 验证码两栏，码只能发给即将生效的那个地址。 */
function BindingChangeModal({ channel, current, bound, onClose, onDone }: { channel: Channel; current: string; bound: boolean; onClose: () => void; onDone: () => Promise<void> }) {
    // 换绑提示同样要走 ConfigProvider 内的 message，深色主题才不会弹浅色条。
    const { message } = App.useApp();
    const copy = channelCopy[channel];
    const [target, setTarget] = useState("");
    const [code, setCode] = useState("");
    const [challengeTarget, setChallengeTarget] = useState("");
    const [sending, setSending] = useState(false);
    const [submitting, setSubmitting] = useState(false);
    const [cooldown, setCooldown] = useState(0);

    useEffect(() => {
        if (cooldown <= 0) return;
        const timer = window.setTimeout(() => setCooldown((value) => value - 1), 1000);
        return () => window.clearTimeout(timer);
    }, [cooldown]);

    const normalized = target.trim();
    // 地址改了就必须重新发码：码是发给某个具体地址的，换一个地址还沿用旧码，
    // 等于把"新地址收得到信"这个唯一证明抹掉。
    const codeStale = challengeTarget !== "" && challengeTarget !== normalized;

    const send = async () => {
        setSending(true);
        try {
            const result = await sendBindingCode({ channel, target: normalized });
            setChallengeTarget(result.target);
            setCooldown(result.cooldown > 0 ? result.cooldown : 60);
            message.success(result.devCode ? `验证码已发送（联调回显：${result.devCode}）` : `验证码已发送至 ${result.target}`);
        } catch (sendError) {
            message.error(sendError instanceof ApiError ? sendError.message : "验证码发送失败，请稍后重试");
        } finally {
            setSending(false);
        }
    };

    const submit = async () => {
        setSubmitting(true);
        try {
            await confirmBinding({ channel, target: normalized, code: code.trim() });
            message.success(bound ? `已换绑，原${copy.noun}会收到一条变更通知` : `已绑定${copy.noun}`);
            await onDone();
        } catch (submitError) {
            message.error(submitError instanceof ApiError ? submitError.message : "换绑失败，请稍后重试");
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <Modal open title={`${bound ? "更换" : "绑定"}${copy.noun}`} onCancel={onClose} onOk={() => void submit()} okText={bound ? "确认换绑" : "确认绑定"} cancelText="取消" confirmLoading={submitting} okButtonProps={{ disabled: !code.trim() || codeStale }}>
            <div className="flex flex-col gap-3 py-2">
                <p className="account-sub">
                    {bound ? `当前${copy.noun}：${current}。换绑后原地址会收到一条变更提醒。` : `这个账号还没有绑定${copy.noun}，绑定后可用于登录并接收安全通知。`}
                </p>
                <label className="flex flex-col gap-1">
                    <span className="account-sub">{copy.label}</span>
                    <Input value={target} placeholder={copy.placeholder} onChange={(event) => setTarget(event.target.value)} />
                </label>
                <div className="flex items-end gap-2">
                    <label className="flex flex-1 flex-col gap-1">
                        <span className="account-sub">验证码</span>
                        <Input value={code} maxLength={6} placeholder="6 位数字" onChange={(event) => setCode(event.target.value)} />
                    </label>
                    <Button loading={sending} disabled={!normalized || normalized === current || cooldown > 0} onClick={() => void send()}>
                        {cooldown > 0 ? `${cooldown}s 后重发` : "发送验证码"}
                    </Button>
                </div>
                {codeStale ? <span className="account-sub">地址已修改，请重新获取验证码。</span> : null}
            </div>
        </Modal>
    );
}
