import { Button, Drawer, Input, Select, Table, Tag, type TableProps } from "antd";
import { MessageSquare, RefreshCw, Search, Send } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import { getAdminTicket, listAdminTickets, replyAdminTicket, updateAdminTicketStatus, type AdminTicket, type AdminTicketStatus } from "./api-tickets";

const statusViews: Record<AdminTicketStatus, { label: string; color: string }> = {
    OPEN: { label: "待处理", color: "gold" },
    PROCESSING: { label: "处理中", color: "blue" },
    RESOLVED: { label: "已解决", color: "green" },
    CLOSED: { label: "已关闭", color: "default" },
};

const statusOptions: Array<{ value: AdminTicketStatus | ""; label: string }> = [
    { value: "", label: "全部状态" },
    { value: "OPEN", label: "待处理" },
    { value: "PROCESSING", label: "处理中" },
    { value: "RESOLVED", label: "已解决" },
    { value: "CLOSED", label: "已关闭" },
];

const categoryLabels: Record<string, string> = {
    BUG: "功能异常",
    BILLING: "计费与订单",
    FEATURE: "功能建议",
    OTHER: "其他",
};

/** 账号展示名：昵称缺失时依次回落到邮箱、手机号、账号 ID。 */
function userLabel(ticket: AdminTicket) {
    return ticket.userName || ticket.userEmail || ticket.userPhone || ticket.userId;
}

/**
 * 状态流转按钮。
 *
 * 只暴露真实服务端允许的路径：已关闭的工单不再提示"关闭"，而是给出"重新打开"，
 * 避免运营在一个已经结束的工单上反复点同一个动作。
 */
function statusActions(status: AdminTicketStatus): Array<{ label: string; status: AdminTicketStatus; danger?: boolean }> {
    switch (status) {
        case "OPEN":
            return [
                { label: "标记处理中", status: "PROCESSING" },
                { label: "标记已解决", status: "RESOLVED" },
                { label: "关闭工单", status: "CLOSED", danger: true },
            ];
        case "PROCESSING":
            return [
                { label: "标记已解决", status: "RESOLVED" },
                { label: "关闭工单", status: "CLOSED", danger: true },
            ];
        case "RESOLVED":
            return [
                { label: "重新打开", status: "PROCESSING" },
                { label: "关闭工单", status: "CLOSED", danger: true },
            ];
        case "CLOSED":
        default:
            return [{ label: "重新打开", status: "PROCESSING" }];
    }
}

/**
 * 工单管理。
 *
 * 指标卡读的是 `page.counts`——全量口径，不随后台筛选变化，否则运营一筛选"待处理"
 * 就会以为站内只有那几张工单。全局 message 在本项目里是关闭的，所有反馈一律走
 * 页面内的 `.admin-notice`。
 */
export function TicketsPane() {
    const [tickets, setTickets] = useState<AdminTicket[]>([]);
    const [counts, setCounts] = useState({ total: 0, open: 0, processing: 0, resolved: 0, closed: 0 });
    const [total, setTotal] = useState(0);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [status, setStatus] = useState<AdminTicketStatus | "">("");
    const [keywordInput, setKeywordInput] = useState("");
    const [keyword, setKeyword] = useState("");
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");

    const [detail, setDetail] = useState<AdminTicket | null>(null);
    const [detailLoading, setDetailLoading] = useState(false);
    const [detailError, setDetailError] = useState("");
    const [detailNotice, setDetailNotice] = useState("");
    const [replyBody, setReplyBody] = useState("");
    const [replyBusy, setReplyBusy] = useState(false);
    const [statusBusy, setStatusBusy] = useState(false);

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminTickets({ status, keyword, page, pageSize });
            setTickets(payload.tickets ?? []);
            setTotal(payload.total ?? 0);
            setCounts(payload.counts ?? { total: 0, open: 0, processing: 0, resolved: 0, closed: 0 });
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载工单失败");
        } finally {
            setLoading(false);
        }
    }, [status, keyword, page, pageSize]);

    useEffect(() => {
        void load();
    }, [load]);

    const openDetail = async (ticket: AdminTicket) => {
        setDetail(ticket);
        setReplyBody("");
        setDetailError("");
        setDetailNotice("");
        setDetailLoading(true);
        try {
            // 列表里不带对话流，展开时再拉一次详情，保证看到的是最新的回复。
            setDetail(await getAdminTicket(ticket.id));
        } catch (loadError) {
            setDetailError(loadError instanceof Error ? loadError.message : "加载工单详情失败");
        } finally {
            setDetailLoading(false);
        }
    };

    const closeDetail = () => {
        setDetail(null);
        setDetailError("");
        setDetailNotice("");
        setReplyBody("");
    };

    const submitReply = async () => {
        if (!detail || replyBusy) return;
        const body = replyBody.trim();
        if (!body) {
            setDetailError("回复内容不能为空");
            return;
        }
        setReplyBusy(true);
        setDetailError("");
        setDetailNotice("");
        try {
            const updated = await replyAdminTicket(detail.id, body);
            setDetail(updated);
            setReplyBody("");
            setDetailNotice("回复已发送。");
            await load();
        } catch (replyError) {
            setDetailError(replyError instanceof Error ? replyError.message : "回复失败");
        } finally {
            setReplyBusy(false);
        }
    };

    const changeStatus = async (next: AdminTicketStatus) => {
        if (!detail || statusBusy) return;
        setStatusBusy(true);
        setDetailError("");
        setDetailNotice("");
        try {
            const updated = await updateAdminTicketStatus(detail.id, next);
            setDetail(updated);
            setDetailNotice(`工单状态已更新为「${statusViews[next].label}」。`);
            await load();
        } catch (statusError) {
            setDetailError(statusError instanceof Error ? statusError.message : "更新状态失败");
        } finally {
            setStatusBusy(false);
        }
    };

    const columns: TableProps<AdminTicket>["columns"] = [
        {
            title: "工单号",
            dataIndex: "ticketNo",
            key: "ticketNo",
            width: 190,
            render: (value: string) => <span className="admin-console-mono">{value}</span>,
        },
        {
            title: "账号",
            key: "user",
            width: 230,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{userLabel(row)}</span>
                    <span className="admin-user-sub">{row.userEmail || row.userPhone || row.userId}</span>
                </span>
            ),
        },
        {
            title: "分类",
            dataIndex: "category",
            key: "category",
            width: 120,
            render: (value: string) => <Tag>{categoryLabels[value] ?? value}</Tag>,
        },
        {
            title: "标题",
            dataIndex: "title",
            key: "title",
            ellipsis: true,
            render: (value: string) => <span className="admin-user-name">{value}</span>,
        },
        {
            title: "状态",
            dataIndex: "status",
            key: "status",
            width: 110,
            render: (value: AdminTicketStatus) => {
                const view = statusViews[value] ?? { label: value, color: "default" };
                return <Tag color={view.color}>{view.label}</Tag>;
            },
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
            width: 120,
            fixed: "right",
            render: (_, row) => (
                <Button size="small" type="text" icon={<MessageSquare className="size-3.5" />} onClick={() => void openDetail(row)}>
                    处理
                </Button>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">工单与反馈</h2>
                    <p className="admin-section-desc">
                        指标卡读的是全量读数，不随下方筛选变化。首次回复会把「待处理」自动推进到「处理中」，关闭后用户无法再追加回复。
                    </p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                    刷新
                </Button>
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
                    <span className="admin-metric-label">工单总数</span>
                    <span className="admin-metric-value">{formatCount(counts.total)}</span>
                    <span className="admin-metric-note">全量口径</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">待处理</span>
                    <span className="admin-metric-value">{formatCount(counts.open)}</span>
                    <span className="admin-metric-note">还没有客服回复</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">处理中</span>
                    <span className="admin-metric-value">{formatCount(counts.processing)}</span>
                    <span className="admin-metric-note">已回复，待用户确认</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">已解决</span>
                    <span className="admin-metric-value">{formatCount(counts.resolved)}</span>
                    <span className="admin-metric-note">不含已关闭</span>
                </div>
            </div>

            <div className="admin-toolbar">
                <Select
                    value={status}
                    options={statusOptions}
                    style={{ width: 150 }}
                    onChange={(value) => {
                        setPage(1);
                        setStatus(value);
                    }}
                />
                <Input
                    allowClear
                    value={keywordInput}
                    prefix={<Search className="size-3.5" />}
                    placeholder="工单号 / 标题 / 账号邮箱或手机号"
                    style={{ width: 300 }}
                    onChange={(event) => setKeywordInput(event.target.value)}
                    onPressEnter={() => {
                        setPage(1);
                        setKeyword(keywordInput.trim());
                    }}
                />
                <Button
                    icon={<Search className="size-3.5" />}
                    onClick={() => {
                        setPage(1);
                        setKeyword(keywordInput.trim());
                    }}
                >
                    查询
                </Button>
                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {formatCount(total)} 条</span>
            </div>

            <div className="admin-card">
                <Table<AdminTicket>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={tickets}
                    columns={columns}
                    scroll={{ x: 1320 }}
                    onRow={(row) => ({ onClick: () => void openDetail(row), style: { cursor: "pointer" } })}
                    pagination={{
                        current: page,
                        pageSize,
                        total,
                        size: "small",
                        showSizeChanger: true,
                        showTotal: (value) => `共 ${value} 条工单`,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setPageSize(nextSize);
                        },
                    }}
                />
            </div>

            <Drawer
                open={detail !== null}
                size={720}
                title={detail ? `工单 ${detail.ticketNo}` : "工单详情"}
                onClose={closeDetail}
            >
                {detail ? (
                    <div className="flex flex-col gap-3">
                        {detailError ? (
                            <div className="admin-notice is-error">
                                <span>{detailError}</span>
                            </div>
                        ) : null}
                        {detailNotice ? (
                            <div className="admin-notice is-ok">
                                <span>{detailNotice}</span>
                            </div>
                        ) : null}

                        <div className="admin-gateway-status">
                            <Tag color={statusViews[detail.status]?.color ?? "default"}>{statusViews[detail.status]?.label ?? detail.status}</Tag>
                            <Tag>{categoryLabels[detail.category] ?? detail.category}</Tag>
                            <span className="admin-gateway-detail">
                                {userLabel(detail)} · {detail.userEmail || detail.userPhone || detail.userId}
                            </span>
                        </div>
                        <div className="admin-canvas-meta">
                            <span><b>提交时间</b>{formatDateTime(detail.createdAt)}</span>
                            <span><b>最近更新</b>{formatDateTime(detail.updatedAt)}</span>
                            <span><b>关闭时间</b>{detail.closedAt ? formatDateTime(detail.closedAt) : "未关闭"}</span>
                            <span><b>联系方式</b>{detail.contact || "未填写"}</span>
                        </div>

                        <div>
                            <h3 className="admin-settings-title">{detail.title}</h3>
                            <pre className="admin-agreement-body">{detail.body}</pre>
                        </div>

                        <div className="flex flex-col gap-2">
                            <span className="admin-user-sub">共 {formatCount(detail.replies.length)} 条回复</span>
                            {detail.replies.length === 0 ? (
                                <p className="admin-user-sub">还没有回复，写下第一条跟进记录。</p>
                            ) : (
                                detail.replies.map((reply) => (
                                    <div className="admin-canvas-node" key={reply.id}>
                                        <div className="admin-canvas-node-head">
                                            <span className="admin-user-name">
                                                {reply.authorName || (reply.authorRole === "STAFF" ? "客服" : "用户")}
                                            </span>
                                            <span className="admin-user-sub">
                                                {reply.authorRole === "STAFF" ? "客服" : "用户"} · {formatDateTime(reply.createdAt)}
                                            </span>
                                        </div>
                                        <p className="admin-canvas-node-body">{reply.body}</p>
                                    </div>
                                ))
                            )}
                        </div>

                        <div className="flex flex-col gap-2">
                            <Input.TextArea
                                value={replyBody}
                                rows={3}
                                maxLength={2000}
                                showCount
                                placeholder="回复用户，说明处理进展与结论"
                                onChange={(event) => setReplyBody(event.target.value)}
                            />
                            <div className="admin-settings-inline">
                                <Button type="primary" icon={<Send className="size-3.5" />} loading={replyBusy} onClick={() => void submitReply()}>
                                    发送回复
                                </Button>
                                {statusActions(detail.status).map((action) => (
                                    <Button
                                        key={action.status}
                                        danger={action.danger}
                                        loading={statusBusy}
                                        onClick={() => void changeStatus(action.status)}
                                    >
                                        {action.label}
                                    </Button>
                                ))}
                                {detailLoading ? <span className="admin-user-sub">正在刷新详情…</span> : null}
                            </div>
                        </div>
                    </div>
                ) : null}
            </Drawer>
        </div>
    );
}
