import { Button, Input, Modal, Select, Table, Tag, Tooltip, type TableProps } from "antd";
import { Ban, Eye, RefreshCw, RotateCcw, Search, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatBytes, formatDateTime } from "@/lib/format-usage";

import { getAdminCanvas, listAdminCanvases, updateAdminCanvasModeration, type AdminCanvas, type AdminCanvasDetail, type AdminCanvasModerationStatus } from "./api";

const statusOptions = [
    { value: "", label: "全部状态" },
    { value: "NORMAL", label: "正常" },
    { value: "HIDDEN", label: "已下架" },
    { value: "REMOVED", label: "已移除" },
];

const statusMeta: Record<AdminCanvasModerationStatus, { label: string; color: string }> = {
    NORMAL: { label: "正常", color: "default" },
    HIDDEN: { label: "已下架", color: "orange" },
    REMOVED: { label: "已移除", color: "red" },
};

function ownerOf(canvas: AdminCanvas) {
    return canvas.ownerName || canvas.ownerEmail || canvas.ownerPhone || canvas.userId;
}

/**
 * 内容审核 / 作品管理。
 *
 * 处置（下架、移除）会直接改变用户能不能打开自己的画布，所以理由必填、按钮二次确认，
 * 并且结果只落在页面内的反馈条上——本项目的全局 antd message 是关闭的。
 */
export function CanvasPane() {
    const [keywordInput, setKeywordInput] = useState("");
    const [keyword, setKeyword] = useState("");
    const [status, setStatus] = useState("");
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [canvases, setCanvases] = useState<AdminCanvas[]>([]);
    const [total, setTotal] = useState(0);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [detail, setDetail] = useState<AdminCanvasDetail | null>(null);
    const [detailLoading, setDetailLoading] = useState(false);
    const [reasonTarget, setReasonTarget] = useState<{ canvas: AdminCanvas; status: AdminCanvasModerationStatus } | null>(null);
    const [reason, setReason] = useState("");
    const [saving, setSaving] = useState(false);

    const load = useCallback(async (options: { keyword: string; status: string; page: number; pageSize: number }) => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminCanvases({
                keyword: options.keyword,
                status: options.status,
                page: options.page,
                pageSize: options.pageSize,
            });
            setCanvases(payload.canvases ?? []);
            setTotal(payload.total ?? 0);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载画布列表失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load({ keyword, status, page, pageSize });
    }, [load, keyword, status, page, pageSize]);

    // 输入即查询会把每一次按键都变成一次列表请求，这里做 300ms 防抖。
    useEffect(() => {
        const timer = setTimeout(() => {
            setPage(1);
            setKeyword(keywordInput.trim());
        }, 300);
        return () => clearTimeout(timer);
    }, [keywordInput]);

    const openDetail = async (canvas: AdminCanvas) => {
        setDetailLoading(true);
        setError("");
        try {
            setDetail(await getAdminCanvas(canvas.id));
        } catch (detailError) {
            setError(detailError instanceof Error ? detailError.message : "加载画布详情失败");
        } finally {
            setDetailLoading(false);
        }
    };

    const reload = () => load({ keyword, status, page, pageSize });

    const openReason = (canvas: AdminCanvas, next: AdminCanvasModerationStatus) => {
        setReasonTarget({ canvas, status: next });
        setReason(next === "NORMAL" ? "误判，已复核恢复" : "");
    };

    const submitModeration = async () => {
        if (!reasonTarget) return;
        const trimmed = reason.trim();
        if (reasonTarget.status !== "NORMAL" && !trimmed) {
            setError("下架或移除必须填写理由，用户会直接看到这句话");
            return;
        }
        setSaving(true);
        setError("");
        setNotice("");
        try {
            const updated = await updateAdminCanvasModeration(reasonTarget.canvas.id, reasonTarget.status, trimmed);
            const verb = reasonTarget.status === "NORMAL" ? "已恢复" : reasonTarget.status === "HIDDEN" ? "已下架" : "已移除";
            setNotice(`${verb}《${updated.title || updated.id}》，用户端已同步生效`);
            if (detail?.id === updated.id) setDetail((current) => (current ? { ...current, ...updated } : current));
            setReasonTarget(null);
            setReason("");
            await reload();
        } catch (actionError) {
            setError(actionError instanceof Error ? actionError.message : "处置失败");
        } finally {
            setSaving(false);
        }
    };

    const columns: TableProps<AdminCanvas>["columns"] = [
        {
            title: "画布",
            key: "canvas",
            render: (_, canvas) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-name">{canvas.title || "（未命名画布）"}</span>
                    <span className="admin-user-sub admin-canvas-id">{canvas.id}</span>
                </div>
            ),
        },
        {
            title: "所有者",
            key: "owner",
            width: 220,
            render: (_, canvas) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-name">{ownerOf(canvas)}</span>
                    <span className="admin-user-sub">{canvas.ownerEmail || canvas.userId}</span>
                </div>
            ),
        },
        {
            title: "内容状态",
            key: "moderationStatus",
            width: 160,
            render: (_, canvas) => (
                <span className="flex min-w-0 flex-col gap-1">
                    <Tag color={statusMeta[canvas.moderationStatus]?.color ?? "default"}>{statusMeta[canvas.moderationStatus]?.label ?? canvas.moderationStatus}</Tag>
                    {canvas.moderationReason ? (
                        <Tooltip title={`${canvas.moderationReason}${canvas.moderatedAt ? ` · ${formatDateTime(canvas.moderatedAt)}` : ""}`}>
                            <span className="admin-user-sub admin-canvas-reason">{canvas.moderationReason}</span>
                        </Tooltip>
                    ) : null}
                </span>
            ),
        },
        { title: "节点", key: "revision", width: 88, render: (_, canvas) => <span className="admin-user-sub">v{canvas.revision}</span> },
        { title: "大小", dataIndex: "payloadBytes", key: "payloadBytes", width: 96, render: (value: number) => <span className="admin-user-sub">{formatBytes(value ?? 0)}</span> },
        { title: "更新时间", dataIndex: "updatedAt", key: "updatedAt", width: 168, render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span> },
        {
            title: "操作",
            key: "actions",
            width: 250,
            render: (_, canvas) => {
                const normal = canvas.moderationStatus === "NORMAL";
                return (
                    <div className="flex flex-wrap items-center gap-1">
                        <Button size="small" type="text" icon={<Eye className="size-3.5" />} onClick={() => void openDetail(canvas)}>
                            查看
                        </Button>
                        {normal ? (
                            <Button size="small" type="text" danger icon={<Ban className="size-3.5" />} onClick={() => openReason(canvas, "HIDDEN")}>
                                下架
                            </Button>
                        ) : (
                            <Button size="small" type="text" icon={<RotateCcw className="size-3.5" />} onClick={() => openReason(canvas, "NORMAL")}>
                                恢复
                            </Button>
                        )}
                        {normal ? (
                            <Button size="small" type="text" danger icon={<Trash2 className="size-3.5" />} onClick={() => openReason(canvas, "REMOVED")}>
                                移除
                            </Button>
                        ) : null}
                    </div>
                );
            },
        },
    ];

    const nodeKinds = detail ? Object.entries(detail.nodeKinds ?? {}).sort((left, right) => right[1] - left[1]) : [];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">内容审核</h2>
                    <p className="admin-section-desc">全站画布的内容状态与处置。下架后用户仍看得到画布列表，但打开时会看到下架理由，且无法继续编辑。</p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={reload}>
                    刷新
                </Button>
            </div>

            <div className="admin-toolbar">
                <Input
                    allowClear
                    value={keywordInput}
                    prefix={<Search className="size-3.5" />}
                    placeholder="搜索标题 / 画布 ID / 所有者 ID"
                    style={{ width: 300 }}
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
                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {total.toLocaleString("zh-CN")} 块画布</span>
            </div>

            {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}
            {notice ? <div className="admin-notice is-ok"><span>{notice}</span></div> : null}

            <div className="admin-card">
                <Table<AdminCanvas>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={canvases}
                    columns={columns}
                    scroll={{ x: 1180 }}
                    pagination={{
                        current: page,
                        pageSize,
                        total,
                        size: "small",
                        showSizeChanger: true,
                        showTotal: (value) => `共 ${value} 块画布`,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setPageSize(nextSize);
                        },
                    }}
                />
            </div>

            <Modal
                open={detail !== null}
                width={760}
                title={detail ? `画布详情 · ${detail.title || detail.id}` : "画布详情"}
                footer={
                    detail ? (
                        <div className="flex items-center justify-end gap-2">
                            {detail.moderationStatus === "NORMAL" ? (
                                <Button danger icon={<Ban className="size-3.5" />} onClick={() => openReason(detail, "HIDDEN")}>
                                    下架
                                </Button>
                            ) : (
                                <Button icon={<RotateCcw className="size-3.5" />} onClick={() => openReason(detail, "NORMAL")}>
                                    恢复
                                </Button>
                            )}
                            <Button onClick={() => setDetail(null)}>关闭</Button>
                        </div>
                    ) : null
                }
                loading={detailLoading}
                onCancel={() => setDetail(null)}
            >
                {detail ? (
                    <div className="flex flex-col gap-3">
                        <div className="admin-canvas-meta">
                            <span><b>所有者</b>{ownerOf(detail)}</span>
                            <span><b>画布 ID</b><code>{detail.id}</code></span>
                            <span><b>版本</b>v{detail.revision}</span>
                            <span><b>大小</b>{formatBytes(detail.payloadBytes ?? 0)}</span>
                            <span><b>节点</b>{detail.nodeCount} 个 / 连线 {detail.connectionCount} 条</span>
                            <span><b>更新时间</b>{formatDateTime(detail.updatedAt)}</span>
                        </div>
                        {detail.moderationReason ? (
                            <div className="admin-notice">
                                <span>当前状态：{statusMeta[detail.moderationStatus]?.label ?? detail.moderationStatus} · 理由：{detail.moderationReason}</span>
                            </div>
                        ) : null}
                        {nodeKinds.length ? (
                            <div className="flex flex-wrap gap-1">
                                {nodeKinds.map(([kind, count]) => (
                                    <Tag key={kind}>{kind} × {count}</Tag>
                                ))}
                            </div>
                        ) : null}
                        <div className="admin-canvas-nodes">
                            {detail.nodes?.length ? (
                                detail.nodes.map((node) => (
                                    <div className="admin-canvas-node" key={node.id}>
                                        <div className="admin-canvas-node-head">
                                            <span className="admin-user-name">{node.title || node.type || node.id}</span>
                                            <span className="admin-user-sub">{node.type}</span>
                                        </div>
                                        {node.content ? <p className="admin-canvas-node-body">{node.content}</p> : null}
                                    </div>
                                ))
                            ) : (
                                <p className="admin-user-sub">这块画布还没有节点。</p>
                            )}
                        </div>
                        {detail.nodesTruncated ? <p className="admin-user-sub">节点过多，这里只展示前 {detail.nodes.length} 个；处置前请以用户举报内容为准。</p> : null}
                    </div>
                ) : null}
            </Modal>

            <Modal
                open={reasonTarget !== null}
                title={
                    reasonTarget
                        ? `${{ NORMAL: "恢复", HIDDEN: "下架", REMOVED: "移除" }[reasonTarget.status]}《${reasonTarget.canvas.title || reasonTarget.canvas.id}》`
                        : ""
                }
                okText={reasonTarget?.status === "NORMAL" ? "确认恢复" : "确认处置"}
                cancelText="取消"
                okButtonProps={{ danger: reasonTarget?.status !== "NORMAL" }}
                confirmLoading={saving}
                onOk={() => void submitModeration()}
                onCancel={() => {
                    setReasonTarget(null);
                    setReason("");
                }}
            >
                <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.7 }}>
                    {reasonTarget?.status === "NORMAL"
                        ? "恢复后用户立刻可以继续打开和编辑这块画布。"
                        : "下架后用户无法打开、编辑或删除这块画布；理由会原样展示给用户，请写清楚违反了什么规则。"}
                </p>
                <Input.TextArea
                    rows={3}
                    maxLength={200}
                    showCount
                    value={reason}
                    placeholder={reasonTarget?.status === "NORMAL" ? "复核结论（选填）" : "例如：包含未授权的品牌素材"}
                    onChange={(event) => setReason(event.target.value)}
                />
            </Modal>
        </div>
    );
}
