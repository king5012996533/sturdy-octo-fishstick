import { Alert, App, Button, Input, Modal, Radio, Space } from "antd";
import { useCallback, useEffect, useState } from "react";

import { sendHostedAuthCode, type HostedAuthMethodType } from "@/features/hosted-auth/api";
import { formatDateTime } from "@/lib/format-usage";
import { getAccountBindings } from "@/services/api/account-bindings";
import { http } from "@/services/api/request";

type AccountDeletionView = {
    status: string;
    reason?: string;
    requestedAt?: string;
    scheduledAt?: string;
    graceDays: number;
};

export type AccountDeletionChannel = {
    methodType: HostedAuthMethodType;
    label: string;
    target: string;
};

/** 用户必须原样输入这两个字，避免误触把账号注销掉。 */
const CONFIRM_WORD = "注销";

function devCodeHintOf(challenge: { devCode?: string }): string {
    return String(challenge.devCode ?? "").trim();
}

/**
 * 自助注销。
 *
 * 注销是不可逆的账号级动作，所以这里坚持两件事：一是必须用账号自己绑定的邮箱
 * 或手机号收验证码确认身份（服务端只认绑定值，请求里带什么目标都不参与校验），
 * 二是明确写出冷静期与「到期后只匿名化、账务记录依法保留、余额不折现」——
 * 用户按下的是一颗会造成不可逆后果的按钮，代价必须在按下之前写清楚。
 */
export function AccountDeletionCard() {
    // 注销流程里的每条提示都必须是 ConfigProvider 内的实例：这一步的文案最不能
    // 在深色主题下变成一条读不清的白条。
    const { message } = App.useApp();
    const [view, setView] = useState<AccountDeletionView | null>(null);
    // 可用渠道自己取，不让外层转手：这张卡现在挂在「更多设置」里，外层手里并没有
    // 绑定关系，为了传一个 prop 再读一次账号总览是本末倒置。
    const [channels, setChannels] = useState<AccountDeletionChannel[]>([]);
    const [loading, setLoading] = useState(true);
    const [open, setOpen] = useState(false);
    const [methodType, setMethodType] = useState<HostedAuthMethodType>("");
    const [code, setCode] = useState("");
    const [reason, setReason] = useState("");
    const [confirmWord, setConfirmWord] = useState("");
    const [cooldown, setCooldown] = useState(0);
    const [sending, setSending] = useState(false);
    const [submitting, setSubmitting] = useState(false);
    const [cancelling, setCancelling] = useState(false);
    const [error, setError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        try {
            setView(await http.get<AccountDeletionView>("/finance/account/deletion"));
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "读取注销状态失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    useEffect(() => {
        let cancelled = false;
        // 注销只能用账号自己绑定过的渠道确认身份（服务端也只认绑定值），因此这里
        // 只把已绑定的邮箱/手机号做成选项：没绑定的渠道点进去只会得到一句"未绑定"。
        getAccountBindings()
            .then((bindings) => {
                if (cancelled) return;
                const next: AccountDeletionChannel[] = [];
                if (bindings.email) next.push({ methodType: "EMAIL_CODE", label: "邮箱", target: bindings.email });
                if (bindings.phone) next.push({ methodType: "PHONE_CODE", label: "手机号", target: bindings.phone });
                setChannels(next);
                setMethodType((current) => (next.some((channel) => channel.methodType === current) ? current : next[0]?.methodType ?? ""));
            })
            .catch(() => {
                // 读不到绑定关系时按钮保持禁用；这条路径本来就需要一个收得到的地址。
                if (!cancelled) setChannels([]);
            });
        return () => {
            cancelled = true;
        };
    }, []);

    useEffect(() => {
        if (cooldown <= 0) return;
        const timer = window.setTimeout(() => setCooldown((value) => value - 1), 1000);
        return () => window.clearTimeout(timer);
    }, [cooldown]);

    const activeChannel = channels.find((channel) => channel.methodType === methodType) ?? channels[0];
    const pending = view?.status === "PENDING";

    const resetForm = () => {
        setCode("");
        setReason("");
        setConfirmWord("");
        setError("");
    };

    const sendCode = async () => {
        if (!activeChannel || cooldown > 0 || sending) return;
        setSending(true);
        setError("");
        try {
            const challenge = await sendHostedAuthCode(activeChannel.methodType, activeChannel.target);
            setCooldown(challenge.cooldownSeconds || 60);
            const devCode = devCodeHintOf(challenge);
            message.success(devCode ? `联调环境验证码：${devCode}` : `验证码已发送至 ${challenge.target}`);
        } catch (sendError) {
            setError(sendError instanceof Error ? sendError.message : "验证码发送失败");
        } finally {
            setSending(false);
        }
    };

    const submit = async () => {
        if (!activeChannel) return;
        setSubmitting(true);
        setError("");
        try {
            const next = await http.post<AccountDeletionView>("/finance/account/deletion", {
                methodType: activeChannel.methodType,
                code: code.trim(),
                reason: reason.trim(),
            });
            setView(next);
            setOpen(false);
            resetForm();
            message.success(`注销申请已受理，${next.graceDays} 天内可随时撤销`);
        } catch (submitError) {
            setError(submitError instanceof Error ? submitError.message : "提交注销申请失败");
        } finally {
            setSubmitting(false);
        }
    };

    const cancel = async () => {
        setCancelling(true);
        setError("");
        try {
            setView(await http.delete<AccountDeletionView>("/finance/account/deletion"));
            message.success("已撤销注销申请，账号继续可用");
        } catch (cancelError) {
            setError(cancelError instanceof Error ? cancelError.message : "撤销失败");
        } finally {
            setCancelling(false);
        }
    };

    return (
        <section className="account-block">
            <h3 className="account-block-title">注销账号</h3>
            {error && !open ? (
                <div className="account-notice is-error">
                    <span>{error}</span>
                </div>
            ) : null}
            {pending && view?.scheduledAt ? (
                <>
                    <div className="account-notice is-error">
                        <span>
                            注销申请已受理，将于 {formatDateTime(view.scheduledAt)} 执行。到期后邮箱、手机号等个人信息会被删除或匿名化，
                            账号将无法再登录。
                        </span>
                    </div>
                    <Space className="mt-3">
                        <Button loading={cancelling} onClick={() => void cancel()}>
                            撤销注销申请
                        </Button>
                    </Space>
                </>
            ) : (
                <>
                    <p className="account-sub">
                        注销后账号将无法登录，已生成的作品与素材也不再可见。申请后有 {view?.graceDays ?? 7} 天冷静期，期间可随时撤销。
                    </p>
                    <Space className="mt-3">
                        <Button danger disabled={loading || channels.length === 0} onClick={() => setOpen(true)}>
                            注销账号
                        </Button>
                        {channels.length === 0 ? <span className="account-sub">账号未绑定邮箱或手机号，请通过工单联系我们注销。</span> : null}
                    </Space>
                </>
            )}

            <Modal
                open={open}
                title="注销账号"
                okText="提交注销申请"
                okButtonProps={{ danger: true, disabled: !code.trim() || confirmWord.trim() !== CONFIRM_WORD }}
                confirmLoading={submitting}
                onOk={() => void submit()}
                onCancel={() => {
                    setOpen(false);
                    resetForm();
                }}
            >
                <div className="flex flex-col gap-3 pt-2">
                    <Alert
                        type="warning"
                        showIcon
                        message="注销前请确认"
                        description={
                            <ul className="m-0 list-disc pl-4">
                                <li>账号内剩余积分不会折现退还，也不会转移到其他账号。</li>
                                <li>正在生成的视频任务会被中止，注销后无法再查看结果。</li>
                                <li>到期后我们会删除或匿名化你的邮箱、手机号、用户名等个人信息。</li>
                                <li>订单、积分流水等交易记录会按法律法规要求继续保留。</li>
                            </ul>
                        }
                    />
                    {channels.length > 1 ? (
                        <Radio.Group value={activeChannel?.methodType} onChange={(event) => setMethodType(event.target.value as HostedAuthMethodType)}>
                            <Space direction="vertical">
                                {channels.map((channel) => (
                                    <Radio key={channel.methodType} value={channel.methodType}>
                                        {channel.label} {channel.target}
                                    </Radio>
                                ))}
                            </Space>
                        </Radio.Group>
                    ) : activeChannel ? (
                        <p className="account-sub m-0">
                            验证码将发送至你的{activeChannel.label} {activeChannel.target}
                        </p>
                    ) : null}
                    <Space.Compact className="w-full">
                        <Input
                            value={code}
                            maxLength={8}
                            placeholder="请输入验证码"
                            onChange={(event) => setCode(event.target.value)}
                        />
                        <Button loading={sending} disabled={cooldown > 0 || !activeChannel} onClick={() => void sendCode()}>
                            {cooldown > 0 ? `${cooldown}s 后重发` : "发送验证码"}
                        </Button>
                    </Space.Compact>
                    <Input.TextArea
                        value={reason}
                        rows={2}
                        maxLength={500}
                        placeholder="注销原因（可选，便于我们改进）"
                        onChange={(event) => setReason(event.target.value)}
                    />
                    <Input
                        value={confirmWord}
                        maxLength={4}
                        placeholder={`请输入「${CONFIRM_WORD}」确认`}
                        onChange={(event) => setConfirmWord(event.target.value)}
                    />
                    {error ? (
                        <div className="account-notice is-error">
                            <span>{error}</span>
                        </div>
                    ) : null}
                </div>
            </Modal>
        </section>
    );
}
