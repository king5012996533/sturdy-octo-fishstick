import { Button, Switch, Tag } from "antd";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { listAdminLoginMethods, updateAdminLoginMethod, type AdminLoginMethod } from "./api";

const categoryLabels: Record<string, string> = {
    CODE: "验证码",
    PASSWORD: "密码",
    OAUTH: "第三方",
};

/** 三条可独立的开关：能不能用、用户能不能看见、能不能用它注册。 */
type MethodFlag = "isEnabled" | "isVisible" | "allowSignUp";

const flagLabels: Array<{ key: MethodFlag; label: string; note: string }> = [
    { key: "isEnabled", label: "启用", note: "关闭后接口一律拒绝该登录方式。" },
    { key: "isVisible", label: "对用户可见", note: "只影响登录页是否展示入口。" },
    { key: "allowSignUp", label: "允许注册", note: "关闭后该通道只能登录，不能建新账号。" },
];

/**
 * 登录方式管理。
 *
 * 服务端会拒绝"关掉最后一种登录方式"：全部关掉等于把所有人（包括管理员自己）
 * 挡在门外，因此这里把失败原因原样回显在卡片上，而不是弹一个提示就消失。
 */
export function LoginMethodsPane() {
    const [methods, setMethods] = useState<AdminLoginMethod[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [busyType, setBusyType] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminLoginMethods();
            setMethods(payload.methods ?? []);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载登录方式失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const toggle = async (method: AdminLoginMethod, flag: MethodFlag, checked: boolean) => {
        setBusyType(method.methodType);
        setError("");
        setNotice("");
        try {
            const updated = await updateAdminLoginMethod(method.methodType, { [flag]: checked });
            setMethods((current) => current.map((item) => (item.methodType === updated.methodType ? updated : item)));
            setNotice(
                updated.unavailableReason
                    ? `${updated.displayName}：${updated.unavailableReason}`
                    : `${updated.displayName} 已更新`,
            );
            if (updated.unavailableReason) setError(updated.unavailableReason);
        } catch (toggleError) {
            setError(toggleError instanceof Error ? toggleError.message : "更新登录方式失败");
        } finally {
            setBusyType("");
        }
    };

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">登录方式</h2>
                    <p className="admin-section-desc">控制登录页对外提供哪些通道，以及哪些通道允许注册。至少需要保留一种可用的登录方式。</p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                    刷新
                </Button>
            </div>

            {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}
            {notice && !error ? <div className="admin-notice is-ok"><span>{notice}</span></div> : null}

            {methods.length === 0 && !loading ? (
                <div className="admin-card">
                    <div className="admin-empty">没有读到登录方式配置</div>
                </div>
            ) : null}

            {methods.map((method) => (
                <div key={method.methodType} className="admin-card">
                    <div className="admin-card-head">
                        <span className="flex min-w-0 flex-col">
                            <b style={{ fontSize: "var(--fs-body)" }}>{method.displayName}</b>
                            <span className="admin-user-sub">
                                {categoryLabels[method.category] ?? method.category} · {method.methodType}
                            </span>
                        </span>
                        <span className="flex items-center gap-2">
                            {method.ready ? null : <Tag color="warning">凭据未配置</Tag>}
                            <span className="flex items-center gap-2">
                                <span className={`admin-dot ${method.isEnabled ? "is-on" : "is-off"}`} aria-hidden />
                                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-dim)" }}>{method.isEnabled ? "启用中" : "已关闭"}</span>
                            </span>
                        </span>
                    </div>
                    <div className="admin-card-pad flex flex-col gap-3">
                        {method.description ? (
                            <p style={{ margin: 0, fontSize: "var(--fs-label)", lineHeight: 1.7, color: "var(--admin-ink-faint)" }}>{method.description}</p>
                        ) : null}
                        {method.unavailableReason ? (
                            <div className="admin-notice is-error">
                                <span>{method.unavailableReason}</span>
                            </div>
                        ) : null}
                        {flagLabels.map((flag) => (
                            <div key={flag.key} className="flex items-start justify-between gap-4">
                                <span className="flex min-w-0 flex-col gap-0.5">
                                    <b style={{ fontSize: "var(--fs-caption)", fontWeight: 500 }}>{flag.label}</b>
                                    <span style={{ fontSize: "var(--fs-label)", lineHeight: 1.7, color: "var(--admin-ink-faint)" }}>{flag.note}</span>
                                </span>
                                <Switch
                                    checked={method[flag.key]}
                                    disabled={loading || busyType === method.methodType}
                                    aria-label={`${method.displayName} - ${flag.label}`}
                                    onChange={(checked) => void toggle(method, flag.key, checked)}
                                />
                            </div>
                        ))}
                    </div>
                </div>
            ))}
        </div>
    );
}
