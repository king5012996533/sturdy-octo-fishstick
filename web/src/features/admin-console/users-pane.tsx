import { Button, Form, Input, Modal, Popconfirm, Select, Table, Tag, type TableProps } from "antd";
import { KeyRound, LogOut, RefreshCw, Search, ShieldCheck, ShieldOff, UserX, UserCheck } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { useUserStore } from "@/stores/use-user-store";

import { forceAdminUserLogout, listAdminUsers, resetAdminUserPassword, updateAdminUserRole, updateAdminUserStatus, type AdminUser } from "./api";

const statusOptions = [
    { value: "", label: "全部状态" },
    { value: "ACTIVE", label: "正常" },
    { value: "DISABLED", label: "已封禁" },
];

const roleOptions = [
    { value: "", label: "全部角色" },
    { value: "USER", label: "普通用户" },
    { value: "ADMIN", label: "管理员" },
];

function formatTime(value?: string) {
    if (!value) return "—";
    const at = new Date(value);
    if (Number.isNaN(at.getTime())) return "—";
    return at.toLocaleString("zh-CN", { hour12: false });
}

function displayNameOf(user: AdminUser) {
    return user.name || user.username || user.email || user.phone || user.id;
}

function identifierOf(user: AdminUser) {
    return user.email || user.phone || user.username || user.id;
}

/**
 * 用户管理。
 *
 * 每个写操作都会改变账号的可用状态（封禁即踢下线、重置密码即强制下线），因此
 * 破坏性动作一律先确认，并且在操作完成后用页面内的反馈条回显结果——全局的 antd
 * message 在本项目里是关闭的，只弹 toast 等于没有反馈。
 */
export function UsersPane() {
    const currentUser = useUserStore((state) => state.user);
    const [keyword, setKeyword] = useState("");
    const [keywordInput, setKeywordInput] = useState("");
    const [status, setStatus] = useState("");
    const [role, setRole] = useState("");
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [users, setUsers] = useState<AdminUser[]>([]);
    const [total, setTotal] = useState(0);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [busyId, setBusyId] = useState("");
    const [passwordTarget, setPasswordTarget] = useState<AdminUser | null>(null);
    const [passwordForm] = Form.useForm<{ password: string }>();
    const [resetting, setResetting] = useState(false);

    const load = useCallback(async (options: { keyword: string; status: string; role: string; page: number; pageSize: number }) => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminUsers({
                keyword: options.keyword,
                status: options.status,
                role: options.role,
                page: options.page,
                pageSize: options.pageSize,
            });
            setUsers(payload.users ?? []);
            setTotal(payload.total ?? 0);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载用户列表失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load({ keyword, status, role, page, pageSize });
    }, [load, keyword, status, role, page, pageSize]);

    // 输入即查询会把每一次按键都变成一次列表请求，这里做 300ms 防抖。
    useEffect(() => {
        const timer = setTimeout(() => {
            setPage(1);
            setKeyword(keywordInput.trim());
        }, 300);
        return () => clearTimeout(timer);
    }, [keywordInput]);

    const runAction = async (user: AdminUser, action: () => Promise<string>) => {
        setBusyId(user.id);
        setNotice("");
        setError("");
        try {
            setNotice(await action());
            await load({ keyword, status, role, page, pageSize });
        } catch (actionError) {
            setError(actionError instanceof Error ? actionError.message : "操作失败");
        } finally {
            setBusyId("");
        }
    };

    const toggleStatus = (user: AdminUser) =>
        runAction(user, async () => {
            const next = user.status === "DISABLED" ? "ACTIVE" : "DISABLED";
            await updateAdminUserStatus(user.id, next);
            return next === "DISABLED" ? `已封禁 ${displayNameOf(user)}，其全部会话已失效` : `已解封 ${displayNameOf(user)}`;
        });

    const toggleRole = (user: AdminUser) =>
        runAction(user, async () => {
            const next = user.role === "ADMIN" ? "USER" : "ADMIN";
            await updateAdminUserRole(user.id, next);
            return next === "ADMIN" ? `已将 ${displayNameOf(user)} 设为管理员` : `已取消 ${displayNameOf(user)} 的管理员权限`;
        });

    const forceLogout = (user: AdminUser) =>
        runAction(user, async () => {
            const result = await forceAdminUserLogout(user.id);
            return `已强制 ${displayNameOf(user)} 下线，吊销 ${result.revokedSessions} 个会话`;
        });

    const submitPassword = async () => {
        if (!passwordTarget) return;
        const values = await passwordForm.validateFields();
        setResetting(true);
        setError("");
        try {
            await resetAdminUserPassword(passwordTarget.id, values.password);
            setNotice(`已重置 ${displayNameOf(passwordTarget)} 的密码，旧会话已全部失效`);
            setPasswordTarget(null);
            passwordForm.resetFields();
            await load({ keyword, status, role, page, pageSize });
        } catch (resetError) {
            setError(resetError instanceof Error ? resetError.message : "重置密码失败");
        } finally {
            setResetting(false);
        }
    };

    const columns: TableProps<AdminUser>["columns"] = [
        {
            title: "账号",
            key: "account",
            render: (_, user) => (
                <div className="admin-user-cell">
                    <span className="admin-user-avatar" aria-hidden>
                        {(displayNameOf(user) || "U").slice(0, 1).toUpperCase()}
                    </span>
                    <span className="flex min-w-0 flex-col">
                        <span className="admin-user-name">{displayNameOf(user)}</span>
                        <span className="admin-user-sub">{identifierOf(user)}</span>
                    </span>
                </div>
            ),
        },
        {
            title: "角色",
            dataIndex: "role",
            key: "role",
            width: 96,
            render: (value: AdminUser["role"], user) => (
                <Tag color={value === "ADMIN" ? "gold" : "default"}>
                    {value === "ADMIN" ? "管理员" : "用户"}
                    {user.id === currentUser?.id ? "（我）" : ""}
                </Tag>
            ),
        },
        {
            title: "状态",
            dataIndex: "status",
            key: "status",
            width: 92,
            render: (value: AdminUser["status"]) => (
                <span className="flex items-center gap-2">
                    <span className={`admin-dot ${value === "ACTIVE" ? "is-on" : "is-off"}`} aria-hidden />
                    <span>{value === "ACTIVE" ? "正常" : "已封禁"}</span>
                </span>
            ),
        },
        { title: "作品", dataIndex: "canvases", key: "canvases", width: 76, render: (value: number) => value?.toLocaleString("zh-CN") ?? "0" },
        { title: "最后活跃", dataIndex: "lastActiveAt", key: "lastActiveAt", width: 168, render: (value?: string) => formatTime(value) },
        { title: "注册时间", dataIndex: "createdAt", key: "createdAt", width: 168, render: (value?: string) => formatTime(value) },
        {
            title: "操作",
            key: "actions",
            width: 300,
            render: (_, user) => {
                const isSelf = user.id === currentUser?.id;
                const busy = busyId === user.id;
                return (
                    <div className="flex flex-wrap items-center gap-1">
                        <Popconfirm
                            title={user.status === "DISABLED" ? "解封这个账号？" : "封禁这个账号？"}
                            description={user.status === "DISABLED" ? "解封后用户可重新登录。" : "封禁会同时吊销该账号的全部会话。"}
                            okText="确定"
                            cancelText="取消"
                            disabled={isSelf}
                            onConfirm={() => void toggleStatus(user)}
                        >
                            <Button size="small" type="text" loading={busy} disabled={isSelf} icon={user.status === "DISABLED" ? <UserCheck className="size-3.5" /> : <UserX className="size-3.5" />}>
                                {user.status === "DISABLED" ? "解封" : "封禁"}
                            </Button>
                        </Popconfirm>
                        <Popconfirm
                            title={user.role === "ADMIN" ? "取消管理员？" : "设为管理员？"}
                            description={user.role === "ADMIN" ? "该账号将失去管理后台的访问权限。" : "管理员可修改渠道、模型与全部用户。"}
                            okText="确定"
                            cancelText="取消"
                            disabled={isSelf}
                            onConfirm={() => void toggleRole(user)}
                        >
                            <Button size="small" type="text" loading={busy} disabled={isSelf} icon={user.role === "ADMIN" ? <ShieldOff className="size-3.5" /> : <ShieldCheck className="size-3.5" />}>
                                {user.role === "ADMIN" ? "取消管理员" : "设为管理员"}
                            </Button>
                        </Popconfirm>
                        <Button size="small" type="text" loading={busy} icon={<KeyRound className="size-3.5" />} onClick={() => setPasswordTarget(user)}>
                            重置密码
                        </Button>
                        <Popconfirm
                            title="强制下线？"
                            description="该账号的全部设备需要重新登录，密码不变。"
                            okText="确定"
                            cancelText="取消"
                            onConfirm={() => void forceLogout(user)}
                        >
                            <Button size="small" type="text" loading={busy} icon={<LogOut className="size-3.5" />}>
                                下线
                            </Button>
                        </Popconfirm>
                    </div>
                );
            },
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">用户管理</h2>
                    <p className="admin-section-desc">账号、角色与在线状态；封禁和重置密码都会立刻吊销该账号的全部会话。</p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load({ keyword, status, role, page, pageSize })}>
                    刷新
                </Button>
            </div>

            <div className="admin-toolbar">
                <Input
                    allowClear
                    value={keywordInput}
                    prefix={<Search className="size-3.5" />}
                    placeholder="搜索邮箱 / 手机号 / 用户名 / ID"
                    style={{ width: 280 }}
                    onChange={(event) => setKeywordInput(event.target.value)}
                />
                <Select
                    value={status}
                    options={statusOptions}
                    style={{ width: 130 }}
                    onChange={(value) => {
                        setPage(1);
                        setStatus(value);
                    }}
                />
                <Select
                    value={role}
                    options={roleOptions}
                    style={{ width: 130 }}
                    onChange={(value) => {
                        setPage(1);
                        setRole(value);
                    }}
                />
                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {total.toLocaleString("zh-CN")} 个账号</span>
            </div>

            {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}
            {notice ? <div className="admin-notice is-ok"><span>{notice}</span></div> : null}

            <div className="admin-card">
                <Table<AdminUser>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={users}
                    columns={columns}
                    scroll={{ x: 1080 }}
                    pagination={{
                        current: page,
                        pageSize,
                        total,
                        size: "small",
                        showSizeChanger: true,
                        showTotal: (value) => `共 ${value} 个账号`,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setPageSize(nextSize);
                        },
                    }}
                />
            </div>

            <Modal
                open={passwordTarget !== null}
                title={`重置密码 · ${passwordTarget ? displayNameOf(passwordTarget) : ""}`}
                okText="确认重置"
                cancelText="取消"
                confirmLoading={resetting}
                onOk={() => void submitPassword()}
                onCancel={() => {
                    setPasswordTarget(null);
                    passwordForm.resetFields();
                }}
            >
                <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.7 }}>
                    重置后该账号的旧会话会立即失效，用户需要用新密码重新登录。
                </p>
                <Form form={passwordForm} layout="vertical" preserve={false}>
                    <Form.Item
                        name="password"
                        label="新密码"
                        rules={[
                            { required: true, message: "请输入新密码" },
                            { min: 8, max: 64, message: "密码长度需为 8-64 位" },
                        ]}
                    >
                        <Input.Password autoComplete="new-password" placeholder="8-64 位" />
                    </Form.Item>
                </Form>
            </Modal>
        </div>
    );
}
