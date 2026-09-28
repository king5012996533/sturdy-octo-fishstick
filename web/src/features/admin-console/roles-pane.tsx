import { Button, Checkbox, Input, Modal, Select, Table, Tag, type TableProps } from "antd";
import { KeyRound, Pencil, Plus, RefreshCw, ShieldCheck, Trash2, UserCog } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import { listAdminUsers, type AdminUser } from "./api";
import {
    assignAdminUserRoles,
    createAdminRole,
    deleteAdminRole,
    getAdminUserRoles,
    listAdminPermissions,
    listAdminRoles,
    updateAdminRole,
    type AdminPermission,
    type AdminRole,
} from "./api-rbac";

/** 角色编辑草稿：code 是它对外唯一的稳定标识，因此改名与改标识分开处理。 */
type RoleDraft = {
    id: string;
    code: string;
    name: string;
    description: string;
    permissions: string[];
};

const emptyDraft: RoleDraft = { id: "", code: "", name: "", description: "", permissions: [] };

/** 角色标识的形态限制必须与后端一致，前端先拦一次只是为了少一次往返。 */
const roleCodePattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$/;

function draftOf(role: AdminRole): RoleDraft {
    return {
        id: role.id,
        code: role.code,
        name: role.name,
        description: role.description,
        permissions: [...role.permissions],
    };
}

function accountLabel(user: AdminUser) {
    return user.name || user.email || user.username || user.phone || user.id;
}

/** 权限点按分组展示：一组权限对应后台的一块功能区。 */
function groupPermissions(permissions: AdminPermission[]) {
    const groups: Array<{ name: string; items: AdminPermission[] }> = [];
    for (const permission of permissions) {
        const existing = groups.find((group) => group.name === permission.group);
        if (existing) {
            existing.items.push(permission);
            continue;
        }
        groups.push({ name: permission.group, items: [permission] });
    }
    return groups;
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
    return (
        <label className="admin-settings-field">
            <span className="admin-settings-field-label">{label}</span>
            {children}
            {hint ? <span className="admin-settings-field-hint">{hint}</span> : null}
        </label>
    );
}

/**
 * 角色与权限。
 *
 * 两件事放在一屏：角色本身的定义（有哪些权限点），以及"谁被授予了哪些角色"。它们
 * 必须一起看——把账号的角色改掉，效果要等下一次请求才生效，而运营往往在同一屏里
 * 先改角色、再给账号分配，分开两页会让"刚配的权限怎么没生效"变成一次排查。
 *
 * 全局的 antd message 在本项目里是关闭的，所有反馈都走页面内的 .admin-notice。
 */
export function RolesPane() {
    const [roles, setRoles] = useState<AdminRole[]>([]);
    const [permissions, setPermissions] = useState<AdminPermission[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");

    const [editorOpen, setEditorOpen] = useState(false);
    const [draft, setDraft] = useState<RoleDraft>(emptyDraft);
    const [editorError, setEditorError] = useState("");
    const [saving, setSaving] = useState(false);

    const [deleteTarget, setDeleteTarget] = useState<AdminRole | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState("");

    const [assignKeyword, setAssignKeyword] = useState("");
    const [assignCandidates, setAssignCandidates] = useState<AdminUser[]>([]);
    const [assignTarget, setAssignTarget] = useState<AdminUser | null>(null);
    const [assignRoles, setAssignRoles] = useState<string[]>([]);
    const [assignQuerying, setAssignQuerying] = useState(false);
    const [assignSaving, setAssignSaving] = useState(false);
    const [assignError, setAssignError] = useState("");
    const [assignNotice, setAssignNotice] = useState("");

    const permissionGroups = useMemo(() => groupPermissions(permissions), [permissions]);
    const assignedTotal = useMemo(() => roles.reduce((total, role) => total + role.userCount, 0), [roles]);

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const [rolePayload, permissionPayload] = await Promise.all([listAdminRoles(), listAdminPermissions()]);
            setRoles(rolePayload.roles ?? []);
            setPermissions(permissionPayload.permissions ?? []);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载角色失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const openEditor = (role: AdminRole | null) => {
        setDraft(role ? draftOf(role) : emptyDraft);
        setEditorError("");
        setError("");
        setNotice("");
        setEditorOpen(true);
    };

    const submitDraft = async () => {
        const code = draft.code.trim();
        const name = draft.name.trim();
        if (!roleCodePattern.test(code)) {
            setEditorError("角色标识只能由字母、数字、下划线和连字符组成，需以字母或数字开头且不超过 32 位。");
            return;
        }
        if (!name) {
            setEditorError("请填写角色名称。");
            return;
        }
        setSaving(true);
        setEditorError("");
        try {
            const input = { code, name, description: draft.description.trim(), permissions: draft.permissions };
            if (draft.id) {
                await updateAdminRole(draft.id, input);
                setNotice(`角色「${name}」已更新。`);
            } else {
                await createAdminRole(input);
                setNotice(`角色「${name}」已创建。`);
            }
            setEditorOpen(false);
            await load();
        } catch (saveError) {
            // 标识冲突、权限点非法这类原因只有服务端知道，原样留在弹窗里。
            setEditorError(saveError instanceof Error ? saveError.message : "保存角色失败");
        } finally {
            setSaving(false);
        }
    };

    const confirmDelete = async () => {
        if (!deleteTarget) return;
        setDeleting(true);
        setDeleteError("");
        setError("");
        setNotice("");
        try {
            await deleteAdminRole(deleteTarget.id);
            setNotice(`角色「${deleteTarget.name}」已删除。`);
            setDeleteTarget(null);
            await load();
        } catch (deleteFailure) {
            setDeleteError(deleteFailure instanceof Error ? deleteFailure.message : "删除角色失败");
        } finally {
            setDeleting(false);
        }
    };

    const selectAssignTarget = useCallback(async (user: AdminUser) => {
        setAssignTarget(user);
        setAssignError("");
        setAssignNotice("");
        setAssignQuerying(true);
        try {
            const payload = await getAdminUserRoles(user.id);
            setAssignRoles(payload.roleCodes ?? []);
        } catch (loadError) {
            setAssignRoles([]);
            setAssignError(loadError instanceof Error ? loadError.message : "读取账号角色失败");
        } finally {
            setAssignQuerying(false);
        }
    }, []);

    // 输入账号 ID 或邮箱都走同一个用户列表接口：后端用 keyword 同时命中 id/邮箱/手机号/昵称，
    // 前端不需要为此再造一条按邮箱查询的路径。
    const searchAccount = async () => {
        const keyword = assignKeyword.trim();
        if (!keyword) {
            setAssignError("请输入账号 ID 或邮箱。");
            return;
        }
        setAssignQuerying(true);
        setAssignError("");
        setAssignNotice("");
        setAssignCandidates([]);
        setAssignTarget(null);
        try {
            const payload = await listAdminUsers({ keyword, pageSize: 10 });
            const users = payload.users ?? [];
            if (users.length === 0) {
                setAssignError("没有找到匹配的账号。");
                return;
            }
            setAssignCandidates(users);
            await selectAssignTarget(users[0]);
        } catch (searchError) {
            setAssignError(searchError instanceof Error ? searchError.message : "查询账号失败");
        } finally {
            setAssignQuerying(false);
        }
    };

    const saveAssignment = async () => {
        if (!assignTarget) return;
        setAssignSaving(true);
        setAssignError("");
        setAssignNotice("");
        try {
            const payload = await assignAdminUserRoles(assignTarget.id, assignRoles);
            setAssignRoles(payload.roleCodes ?? []);
            setAssignNotice(`已更新 ${accountLabel(assignTarget)} 的角色。`);
            // 引用数变了，列表要重新拉一次，否则删除提示里的数字会对不上。
            await load();
        } catch (saveError) {
            setAssignError(saveError instanceof Error ? saveError.message : "保存账号角色失败");
        } finally {
            setAssignSaving(false);
        }
    };

    const columns: TableProps<AdminRole>["columns"] = [
        {
            title: "角色",
            key: "role",
            width: 260,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{row.name}</span>
                    <span className="admin-user-sub">{row.code}</span>
                </span>
            ),
        },
        {
            title: "权限数",
            key: "permissions",
            width: 110,
            render: (_, row) => <b>{formatCount(row.permissions.length)} 项</b>,
        },
        {
            title: "授予范围",
            key: "scope",
            render: (_, row) => (
                <span className="admin-user-sub">
                    {row.permissions.length > 0
                        ? row.permissions.map((code) => permissions.find((item) => item.code === code)?.name ?? code).join(" · ")
                        : "未授予任何权限点"}
                </span>
            ),
        },
        {
            title: "类型",
            dataIndex: "builtin",
            key: "builtin",
            width: 100,
            render: (builtin: boolean) => (builtin ? <Tag color="gold">内置</Tag> : <Tag>自定义</Tag>),
        },
        {
            title: "引用账号",
            dataIndex: "userCount",
            key: "userCount",
            width: 100,
            render: (value: number) => formatCount(value),
        },
        {
            title: "更新时间",
            dataIndex: "updatedAt",
            key: "updatedAt",
            width: 180,
            render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span>,
        },
        {
            title: "操作",
            key: "actions",
            width: 150,
            render: (_, row) => (
                <div className="admin-settings-inline">
                    <Button size="small" type="text" icon={<Pencil className="size-3.5" />} onClick={() => openEditor(row)}>
                        编辑
                    </Button>
                    <Button
                        size="small"
                        type="text"
                        danger
                        icon={<Trash2 className="size-3.5" />}
                        onClick={() => {
                            setDeleteError("");
                            setDeleteTarget(row);
                        }}
                    >
                        删除
                    </Button>
                </div>
            ),
        },
    ];

    const editingBuiltin = Boolean(draft.id) && roles.some((role) => role.id === draft.id && role.builtin);

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">角色与权限</h2>
                    <p className="admin-section-desc">
                        角色是一组权限点的集合，账号通过角色获得后台能力；账号本身的 ADMIN 角色始终放行全部功能。内置角色不可删除、不可改标识，但可以按需调整名称与权限。
                    </p>
                </div>
                <div className="admin-settings-inline">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button type="primary" icon={<Plus className="size-3.5" />} onClick={() => openEditor(null)}>
                        新建角色
                    </Button>
                </div>
            </div>

            {error ? (
                <div className="admin-notice is-error">
                    <span>{error}</span>
                </div>
            ) : null}
            {notice ? (
                <div className="admin-notice is-ok">
                    <span>{notice}</span>
                </div>
            ) : null}

            <div className="admin-metric-grid">
                <div className="admin-metric">
                    <span className="admin-metric-label">角色总数</span>
                    <span className="admin-metric-value">{formatCount(roles.length)}</span>
                    <span className="admin-metric-note">含内置与自定义角色</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">内置角色</span>
                    <span className="admin-metric-value">{formatCount(roles.filter((role) => role.builtin).length)}</span>
                    <span className="admin-metric-note">不可删除，不可改标识</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">可选权限点</span>
                    <span className="admin-metric-value">{formatCount(permissions.length)}</span>
                    <span className="admin-metric-note">由服务端目录冻结</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">角色绑定数</span>
                    <span className="admin-metric-value">{formatCount(assignedTotal)}</span>
                    <span className="admin-metric-note">同一账号可拥有多个角色</span>
                </div>
            </div>

            <div className="admin-card">
                <div className="admin-card-head">
                    <span className="flex min-w-0 items-center gap-2">
                        <ShieldCheck className="size-4" />
                        <b style={{ fontSize: "var(--fs-body)" }}>全部角色</b>
                    </span>
                    <span className="admin-user-sub">共 {formatCount(roles.length)} 个</span>
                </div>
                <Table<AdminRole>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={roles}
                    columns={columns}
                    scroll={{ x: 1080 }}
                    pagination={false}
                />
            </div>

            <div className="admin-card admin-card-pad">
                <div className="admin-card-head">
                    <span className="flex min-w-0 items-center gap-2">
                        <UserCog className="size-4" />
                        <b style={{ fontSize: "var(--fs-body)" }}>账号角色分配</b>
                    </span>
                    <span className="admin-user-sub">按账号 ID 或邮箱查询</span>
                </div>
                <div className="admin-toolbar">
                    <Input
                        allowClear
                        value={assignKeyword}
                        placeholder="账号 ID 或邮箱"
                        style={{ width: 280 }}
                        onChange={(event) => setAssignKeyword(event.target.value)}
                        onPressEnter={() => void searchAccount()}
                    />
                    <Button icon={<KeyRound className="size-3.5" />} loading={assignQuerying} onClick={() => void searchAccount()}>
                        查询账号
                    </Button>
                    {assignCandidates.length > 1 ? (
                        <Select
                            value={assignTarget?.id}
                            style={{ width: 320 }}
                            placeholder="匹配到多个账号，请选择"
                            options={assignCandidates.map((user) => ({ value: user.id, label: `${accountLabel(user)} · ${user.id}` }))}
                            onChange={(value) => {
                                const next = assignCandidates.find((user) => user.id === value);
                                if (next) void selectAssignTarget(next);
                            }}
                        />
                    ) : null}
                </div>

                {assignError ? (
                    <div className="admin-notice is-error">
                        <span>{assignError}</span>
                    </div>
                ) : null}
                {assignNotice ? (
                    <div className="admin-notice is-ok">
                        <span>{assignNotice}</span>
                    </div>
                ) : null}

                {assignTarget ? (
                    <div className="flex flex-col gap-3" style={{ marginTop: 12 }}>
                        <span className="admin-user-sub">
                            当前账号：{accountLabel(assignTarget)}（{assignTarget.id}）
                        </span>
                        <Checkbox.Group
                            value={assignRoles}
                            onChange={(values) => setAssignRoles(values as string[])}
                            options={roles.map((role) => ({
                                value: role.code,
                                label: (
                                    <span>
                                        {role.name} <span className="admin-user-sub">{role.code}</span>
                                    </span>
                                ),
                            }))}
                        />
                        <div className="admin-settings-inline">
                            <Button type="primary" loading={assignSaving} onClick={() => void saveAssignment()}>
                                保存角色
                            </Button>
                            <Button
                                onClick={() => {
                                    setAssignRoles([]);
                                    setAssignNotice("");
                                    setAssignError("");
                                }}
                            >
                                清空选择
                            </Button>
                        </div>
                        <span className="admin-settings-field-hint">
                            保存后立即生效：清空勾选等于收回该账号的全部角色权限。
                        </span>
                    </div>
                ) : null}
            </div>

            <Modal
                open={editorOpen}
                width={720}
                title={draft.id ? `编辑角色 · ${draft.name || draft.code}` : "新建角色"}
                okText={draft.id ? "保存" : "创建"}
                cancelText="取消"
                confirmLoading={saving}
                onOk={() => void submitDraft()}
                onCancel={() => {
                    setEditorOpen(false);
                    setEditorError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    {editorError ? (
                        <div className="admin-notice is-error">
                            <span>{editorError}</span>
                        </div>
                    ) : null}
                    <Field label="角色标识" hint="字母、数字、下划线与连字符，最多 32 位；内置角色不可修改。">
                        <Input
                            value={draft.code}
                            disabled={editingBuiltin}
                            placeholder="CONTENT_OPERATOR"
                            onChange={(event) => setDraft((current) => ({ ...current, code: event.target.value }))}
                        />
                    </Field>
                    <Field label="角色名称">
                        <Input value={draft.name} placeholder="内容运营" onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} />
                    </Field>
                    <Field label="说明">
                        <Input.TextArea
                            rows={2}
                            value={draft.description}
                            placeholder="这个角色负责什么，方便其他管理员理解"
                            onChange={(event) => setDraft((current) => ({ ...current, description: event.target.value }))}
                        />
                    </Field>
                    <div className="flex flex-col gap-3">
                        <span className="admin-settings-field-label">权限点</span>
                        {permissionGroups.map((group) => (
                            <div key={group.name} className="flex flex-col gap-2">
                                <span className="admin-user-sub">{group.name}</span>
                                <Checkbox.Group
                                    value={draft.permissions}
                                    onChange={(values) => setDraft((current) => ({ ...current, permissions: values as string[] }))}
                                    options={group.items.map((permission) => ({ value: permission.code, label: permission.name }))}
                                />
                            </div>
                        ))}
                    </div>
                </div>
            </Modal>

            <Modal
                open={deleteTarget !== null}
                title="删除角色？"
                okText="确认删除"
                okButtonProps={{ danger: true }}
                cancelText="取消"
                confirmLoading={deleting}
                onOk={() => void confirmDelete()}
                onCancel={() => {
                    setDeleteTarget(null);
                    setDeleteError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.8 }}>
                        即将删除角色「{deleteTarget?.name}」（{deleteTarget?.code}）。
                        {deleteTarget && deleteTarget.builtin
                            ? "该角色是内置角色，服务端会拒绝删除。"
                            : deleteTarget && deleteTarget.userCount > 0
                              ? `当前仍有 ${formatCount(deleteTarget.userCount)} 个账号引用它，请先在下方解除分配。`
                              : "删除后不可恢复。"}
                    </p>
                    {deleteError ? (
                        <div className="admin-notice is-error">
                            <span>{deleteError}</span>
                        </div>
                    ) : null}
                </div>
            </Modal>
        </div>
    );
}
