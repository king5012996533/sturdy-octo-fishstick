import { App, Button, Popconfirm, Tag } from "antd";
import { Monitor, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatDateTime } from "@/lib/format-usage";
import { listAccountSessions, revokeAccountSession, revokeOtherAccountSessions, type AccountSession } from "@/services/api/account-security";
import { ApiError } from "@/services/api/request";

/**
 * 「登录设备」：账号现在在哪几台设备上登着，以及把不认识的那台踢下去。
 *
 * 这是账号被盗时唯一有效的自救手段——改密码只影响"下次登录"，攻击者手里已经签发的
 * 会话仍然有效，只有吊销会话才能当场把他请出去。因此这一块不做折叠、不做分页，
 * 打开设置页就能看到。
 */
export function AccountDevicesCard() {
    // 深色主题下来自 ConfigProvider 的 message 实例；静态 message 会掉到默认浅色主题。
    const { message } = App.useApp();
    const [sessions, setSessions] = useState<AccountSession[] | null>(null);
    const [loading, setLoading] = useState(true);
    const [busyId, setBusyId] = useState("");
    const [error, setError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        try {
            setSessions(await listAccountSessions());
            setError("");
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "读取登录设备失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const revoke = async (session: AccountSession) => {
        setBusyId(session.id);
        try {
            setSessions(await revokeAccountSession(session.id));
            message.success("该设备已下线");
        } catch (revokeError) {
            message.error(revokeError instanceof ApiError ? revokeError.message : "下线失败，请稍后重试");
        } finally {
            setBusyId("");
        }
    };

    const revokeOthers = async () => {
        setBusyId("others");
        try {
            const result = await revokeOtherAccountSessions();
            setSessions(result.sessions);
            message.success(result.revoked > 0 ? `已从其他 ${result.revoked} 台设备退出登录` : "没有其他设备在登录");
        } catch (revokeError) {
            message.error(revokeError instanceof ApiError ? revokeError.message : "下线失败，请稍后重试");
        } finally {
            setBusyId("");
        }
    };

    const others = (sessions ?? []).filter((session) => !session.current).length;

    return (
        <div className="settings-pane mx-auto mt-4 w-full max-w-2xl">
            <div className="settings-pane-header">
                <div className="min-w-0">
                    <h2>登录设备</h2>
                    <p>发现不认识的设备时，先改密码，再在这里把它下线——只改密码不会让已经登录的设备掉线。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button icon={<RefreshCw className="size-4" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Popconfirm
                        title="下线其他所有设备？"
                        description="除当前设备外，其余设备都需要重新登录。"
                        okText="全部下线"
                        cancelText="取消"
                        disabled={others === 0}
                        onConfirm={() => void revokeOthers()}
                    >
                        <Button danger disabled={others === 0} loading={busyId === "others"}>
                            下线其他设备{others > 0 ? `（${others}）` : ""}
                        </Button>
                    </Popconfirm>
                </div>
            </div>

            {error ? (
                <div className="account-notice is-error">
                    <span>{error}</span>
                </div>
            ) : null}

            <section className="account-block">
                <h3 className="account-block-title">当前登录</h3>
                {sessions && sessions.length > 0 ? (
                    <ul className="flex flex-col gap-3">
                        {sessions.map((session) => (
                            <li key={session.id} className="flex items-start justify-between gap-3">
                                <div className="flex min-w-0 flex-col gap-1">
                                    <div className="flex items-center gap-2">
                                        <Monitor className="size-3.5" aria-hidden />
                                        <b className="truncate">{session.device}</b>
                                        {session.current ? <Tag color="green">当前设备</Tag> : null}
                                        {!session.current && session.identifier ? <span className="account-sub">{session.identifier}</span> : null}
                                    </div>
                                    <span className="account-sub">
                                        IP {session.ipAddress || "未知"} · 登录于 {formatDateTime(session.createdAt)}
                                        {session.lastActiveAt ? ` · 最近活跃 ${formatDateTime(session.lastActiveAt)}` : ""}
                                    </span>
                                </div>
                                {session.current ? (
                                    <span className="account-sub">正在使用</span>
                                ) : (
                                    <Popconfirm title="下线这台设备？" description="该设备上的会话会立即失效，需要重新登录。" okText="下线" cancelText="取消" onConfirm={() => void revoke(session)}>
                                        <Button size="small" type="text" danger loading={busyId === session.id}>
                                            下线
                                        </Button>
                                    </Popconfirm>
                                )}
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p className="account-sub">{loading ? "正在读取登录设备…" : "没有其他设备在登录。"}</p>
                )}
            </section>
        </div>
    );
}
