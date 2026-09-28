import { Button, Table, Tag, type TableProps } from "antd";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { listAdminAuditEvents, type AdminAuditEvent } from "./api";

/**
 * 操作动作按前缀归类。
 *
 * 不逐个动作写中文名：审计流的动作由画布/账号两侧分别写入，逐个列举注定会漏，
 * 于是原始动作串也会一起显示——归类只负责让人一眼看出这块改的是什么。
 */
const actionGroups: Array<{ prefix: string; label: string; color: string }> = [
    { prefix: "user.status", label: "封禁解封", color: "red" },
    { prefix: "user.role", label: "账号角色", color: "gold" },
    { prefix: "user.password", label: "重置密码", color: "orange" },
    { prefix: "user.logout", label: "强制下线", color: "blue" },
    { prefix: "login-method", label: "登录方式", color: "geekblue" },
    { prefix: "canvas", label: "内容审核", color: "orange" },
    { prefix: "channel", label: "渠道与模型", color: "cyan" },
    { prefix: "logical_model", label: "前台模型", color: "purple" },
    { prefix: "plugin", label: "插件", color: "magenta" },
    { prefix: "appearance", label: "品牌外观", color: "green" },
    { prefix: "feature_availability", label: "功能开放", color: "lime" },
    { prefix: "response_interception", label: "响应拦截", color: "volcano" },
    { prefix: "runtime_policy", label: "运行时策略", color: "default" },
    { prefix: "api_log", label: "调用日志", color: "default" },
];

function actionGroup(action: string) {
    return actionGroups.find((group) => action.startsWith(group.prefix)) ?? { prefix: "", label: action, color: "default" };
}

function formatTime(value: string) {
    const at = new Date(value);
    return Number.isNaN(at.getTime()) ? value : at.toLocaleString("zh-CN", { hour12: false });
}

/**
 * 审计日志。
 *
 * 只读、不可编辑：这条流水的价值在于"谁在什么时候改了什么"能被原样回放，
 * 因此界面不提供任何写入口，也不允许按操作类型删除。
 */
export function AuditPane() {
    const [events, setEvents] = useState<AdminAuditEvent[]>([]);
    const [total, setTotal] = useState(0);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");

    const load = useCallback(async (nextPage: number, nextSize: number) => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminAuditEvents({ page: nextPage, pageSize: nextSize });
            setEvents(payload.events ?? []);
            setTotal(payload.total ?? 0);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载审计日志失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load(page, pageSize);
    }, [load, page, pageSize]);

    const columns: TableProps<AdminAuditEvent>["columns"] = [
        { title: "时间", dataIndex: "createdAt", key: "createdAt", width: 168, render: (value: string) => formatTime(value) },
        {
            title: "操作类型",
            dataIndex: "action",
            key: "action",
            width: 168,
            render: (value: string) => {
                const group = actionGroup(value);
                return (
                    <span className="flex min-w-0 flex-col gap-1">
                        <Tag color={group.color}>{group.label}</Tag>
                        <span className="admin-user-sub">{value}</span>
                    </span>
                );
            },
        },
        { title: "摘要", dataIndex: "summary", key: "summary", ellipsis: true },
        {
            title: "对象",
            key: "target",
            width: 240,
            render: (_, event) => (
                <span className="admin-user-sub">
                    {event.targetType}
                    {event.targetId ? ` · ${event.targetId}` : ""}
                </span>
            ),
        },
        { title: "操作人", dataIndex: "actorUserId", key: "actorUserId", width: 200, render: (value: string) => <span className="admin-user-sub">{value || "—"}</span> },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">审计日志</h2>
                    <p className="admin-section-desc">管理员写操作的时间、对象与摘要；重置密码这类动作只记录"发生过"，不记录凭据本身。</p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load(page, pageSize)}>
                    刷新
                </Button>
            </div>

            {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}

            <div className="admin-card">
                <Table<AdminAuditEvent>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={events}
                    columns={columns}
                    scroll={{ x: 960 }}
                    pagination={{
                        current: page,
                        pageSize,
                        total,
                        size: "small",
                        showSizeChanger: true,
                        showTotal: (value) => `共 ${value} 条记录`,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setPageSize(nextSize);
                        },
                    }}
                />
            </div>
        </div>
    );
}
