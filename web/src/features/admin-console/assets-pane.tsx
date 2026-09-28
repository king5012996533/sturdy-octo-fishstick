import { Button, Drawer, Input, Modal, Select, Table, Tag, Tooltip, type TableProps } from "antd";
import { Eye, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { assetCategoryLabel } from "@/lib/asset-category";
import { formatBytes, formatCount, formatDateTime } from "@/lib/format-usage";

import { getAdminAsset, listAdminAssets, moderateAdminAsset, type AdminAsset, type AdminAssetModerationStatus, type AdminAssetTotals } from "./api-assets";

const statusOptions = [
    { value: "", label: "全部处置状态" },
    { value: "NORMAL", label: "正常" },
    { value: "HIDDEN", label: "已隐藏" },
    { value: "REMOVED", label: "已删除" },
];

const kindOptions = [
    { value: "", label: "全部类型" },
    { value: "text", label: "文本" },
    { value: "image", label: "图片" },
    { value: "video", label: "视频" },
    { value: "audio", label: "音频" },
    { value: "entity", label: "角色卡" },
    { value: "model", label: "模型" },
];

const statusMeta: Record<AdminAssetModerationStatus, { label: string; color: string }> = {
    NORMAL: { label: "正常", color: "default" },
    HIDDEN: { label: "已隐藏", color: "orange" },
    REMOVED: { label: "已删除", color: "red" },
};

const moderationOptions = [
    { value: "NORMAL", label: "恢复正常" },
    { value: "HIDDEN", label: "隐藏素材" },
    { value: "REMOVED", label: "删除素材" },
];

const emptyTotals: AdminAssetTotals = { total: 0, hidden: 0, removed: 0, totalBytes: 0, users: 0 };

const kindLabels: Record<string, string> = Object.fromEntries(kindOptions.filter((option) => option.value).map((option) => [option.value, option.label]));

function assetKindLabel(kind: string) {
    return kindLabels[kind] ?? (kind || "—");
}

function ownerOf(asset: AdminAsset) {
    return asset.userName || asset.userId;
}

function nextModerationStatus(status: AdminAssetModerationStatus): AdminAssetModerationStatus {
    // 处置栏默认给出最常见的下一步：正常素材建议隐藏，已处置的素材建议恢复。
    return status === "NORMAL" ? "HIDDEN" : "NORMAL";
}

/**
 * 素材资源管理。
 *
 * 处置只改数据库里的审核结论，不删对象存储文件（本仓库没有 CDN 接入）；理由必填、
 * 二次确认，结果只落在页面内的反馈条上——本项目全局 antd message 是关闭的。
 */
export function AssetsPane() {
    const [keywordInput, setKeywordInput] = useState("");
    const [keyword, setKeyword] = useState("");
    const [status, setStatus] = useState("");
    const [kind, setKind] = useState("");
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [assets, setAssets] = useState<AdminAsset[]>([]);
    const [total, setTotal] = useState(0);
    const [totals, setTotals] = useState<AdminAssetTotals>(emptyTotals);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [detail, setDetail] = useState<AdminAsset | null>(null);
    const [detailLoading, setDetailLoading] = useState(false);
    const [nextStatus, setNextStatus] = useState<AdminAssetModerationStatus>("HIDDEN");
    const [reason, setReason] = useState("");
    const [confirmOpen, setConfirmOpen] = useState(false);
    const [saving, setSaving] = useState(false);

    const load = useCallback(async (options: { keyword: string; status: string; kind: string; page: number; pageSize: number }) => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminAssets({
                keyword: options.keyword,
                status: options.status,
                kind: options.kind,
                page: options.page,
                pageSize: options.pageSize,
            });
            setAssets(payload.assets ?? []);
            setTotal(payload.total ?? 0);
            setTotals(payload.totals ?? emptyTotals);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载素材列表失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load({ keyword, status, kind, page, pageSize });
    }, [load, keyword, status, kind, page, pageSize]);

    // 输入即查询会把每一次按键都变成一次列表请求，这里做 300ms 防抖。
    useEffect(() => {
        const timer = setTimeout(() => {
            setPage(1);
            setKeyword(keywordInput.trim());
        }, 300);
        return () => clearTimeout(timer);
    }, [keywordInput]);

    const reload = () => load({ keyword, status, kind, page, pageSize });

    const closeDetail = () => {
        setDetail(null);
        setReason("");
        setConfirmOpen(false);
    };

    const openDetail = async (asset: AdminAsset) => {
        setDetailLoading(true);
        setError("");
        setNotice("");
        try {
            const payload = await getAdminAsset(asset.id);
            setDetail(payload.asset);
            setNextStatus(nextModerationStatus(payload.asset.moderationStatus));
            setReason("");
        } catch (detailError) {
            setError(detailError instanceof Error ? detailError.message : "加载素材详情失败");
        } finally {
            setDetailLoading(false);
        }
    };

    const requestModeration = () => {
        if (!detail) return;
        const trimmed = reason.trim();
        if (nextStatus !== "NORMAL" && !trimmed) {
            setError("隐藏或删除必须填写理由，留痕需要能解释当时的判断");
            return;
        }
        setError("");
        setConfirmOpen(true);
    };

    const submitModeration = async () => {
        if (!detail) return;
        setSaving(true);
        setError("");
        setNotice("");
        try {
            const payload = await moderateAdminAsset(detail.id, { status: nextStatus, reason: reason.trim() });
            const verb = nextStatus === "NORMAL" ? "已恢复" : nextStatus === "HIDDEN" ? "已隐藏" : "已删除";
            setNotice(`${verb}素材《${payload.asset.title || payload.asset.id}》`);
            setDetail(payload.asset);
            setReason("");
            setNextStatus(nextModerationStatus(payload.asset.moderationStatus));
            await reload();
        } catch (actionError) {
            setError(actionError instanceof Error ? actionError.message : "素材处置失败");
        } finally {
            setConfirmOpen(false);
            setSaving(false);
        }
    };

    const columns: TableProps<AdminAsset>["columns"] = [
        {
            title: "标题",
            key: "title",
            render: (_, asset) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-name">{asset.title || "（未命名素材）"}</span>
                    <span className="admin-user-sub admin-canvas-id">{asset.id}</span>
                </div>
            ),
        },
        {
            title: "账号",
            key: "owner",
            width: 200,
            render: (_, asset) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-name">{ownerOf(asset)}</span>
                    <span className="admin-user-sub">{asset.userId}</span>
                </div>
            ),
        },
        { title: "类型", key: "kind", width: 96, render: (_, asset) => <span className="admin-user-sub">{assetKindLabel(asset.kind)}</span> },
        { title: "分类", key: "category", width: 88, render: (_, asset) => <span className="admin-user-sub">{assetCategoryLabel(asset.category)}</span> },
        { title: "版本数", dataIndex: "versionCount", key: "versionCount", width: 84, render: (value: number) => <span className="admin-user-sub">{formatCount(value ?? 0)}</span> },
        { title: "占用", dataIndex: "payloadBytes", key: "payloadBytes", width: 96, render: (value: number) => <span className="admin-user-sub">{formatBytes(value ?? 0)}</span> },
        {
            title: "处置状态",
            key: "moderationStatus",
            width: 160,
            render: (_, asset) => (
                <span className="flex min-w-0 flex-col gap-1">
                    <Tag color={statusMeta[asset.moderationStatus]?.color ?? "default"}>{statusMeta[asset.moderationStatus]?.label ?? asset.moderationStatus}</Tag>
                    {asset.moderationReason ? (
                        <Tooltip title={asset.moderationReason}>
                            <span className="admin-user-sub admin-canvas-reason">{asset.moderationReason}</span>
                        </Tooltip>
                    ) : null}
                </span>
            ),
        },
        { title: "更新时间", dataIndex: "updatedAt", key: "updatedAt", width: 168, render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span> },
        {
            title: "操作",
            key: "actions",
            width: 96,
            render: (_, asset) => (
                <Button size="small" type="text" icon={<Eye className="size-3.5" />} onClick={() => void openDetail(asset)}>
                    查看
                </Button>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">素材资源</h2>
                    <p className="admin-section-desc">
                        全站素材的处置状态与规模概览。隐藏或删除只改变平台侧可见性，不清理对象存储文件；「占用」是按 payload 与版本定义字符长度估算的近似值。
                    </p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={reload}>
                    刷新
                </Button>
            </div>

            <div className="admin-metric-grid">
                <div className="admin-metric">
                    <span className="admin-metric-label">素材总数</span>
                    <span className="admin-metric-value">{formatCount(totals.total)}</span>
                    <span className="admin-metric-note">涉及账号 {formatCount(totals.users)}</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">已隐藏</span>
                    <span className="admin-metric-value">{formatCount(totals.hidden)}</span>
                    <span className="admin-metric-note">前台不展示，可恢复</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">已删除</span>
                    <span className="admin-metric-value">{formatCount(totals.removed)}</span>
                    <span className="admin-metric-note">判定违规，数据保留</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">占用近似值</span>
                    <span className="admin-metric-value">{formatBytes(totals.totalBytes)}</span>
                    <span className="admin-metric-note">payload 与版本定义字符长度之和</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">涉及账号</span>
                    <span className="admin-metric-value">{formatCount(totals.users)}</span>
                    <span className="admin-metric-note">有素材的账号数</span>
                </div>
            </div>

            <div className="admin-toolbar">
                <Input
                    allowClear
                    value={keywordInput}
                    prefix={<Search className="size-3.5" />}
                    placeholder="搜索标题 / 素材 ID / 账号"
                    style={{ width: 280 }}
                    onChange={(event) => setKeywordInput(event.target.value)}
                />
                <Select
                    value={status}
                    options={statusOptions}
                    style={{ width: 140 }}
                    onChange={(value) => {
                        setPage(1);
                        setStatus(value);
                    }}
                />
                <Select
                    value={kind}
                    options={kindOptions}
                    style={{ width: 130 }}
                    onChange={(value) => {
                        setPage(1);
                        setKind(value);
                    }}
                />
                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {formatCount(total)} 条素材</span>
            </div>

            {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}
            {notice ? <div className="admin-notice is-ok"><span>{notice}</span></div> : null}

            <div className="admin-card">
                <Table<AdminAsset>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={assets}
                    columns={columns}
                    scroll={{ x: 1180 }}
                    pagination={{
                        current: page,
                        pageSize,
                        total,
                        size: "small",
                        showSizeChanger: true,
                        showTotal: (value) => `共 ${value} 条素材`,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setPageSize(nextSize);
                        },
                    }}
                />
            </div>

            <Drawer
                open={detail !== null}
                width={720}
                title={detail ? `素材详情 · ${detail.title || detail.id}` : "素材详情"}
                onClose={closeDetail}
            >
                {detailLoading ? (
                    <p className="admin-user-sub">加载中…</p>
                ) : detail ? (
                    <div className="flex flex-col gap-3">
                        <div className="admin-canvas-meta">
                            <span><b>账号</b>{ownerOf(detail)}</span>
                            <span><b>素材 ID</b><code>{detail.id}</code></span>
                            <span><b>类型</b>{assetKindLabel(detail.kind)}</span>
                            <span><b>分类</b>{assetCategoryLabel(detail.category)}</span>
                            <span><b>版本数</b>{formatCount(detail.versionCount ?? 0)}</span>
                            <span><b>占用近似值</b>{formatBytes(detail.payloadBytes ?? 0)}</span>
                            <span><b>更新时间</b>{formatDateTime(detail.updatedAt)}</span>
                        </div>

                        {detail.moderationReason ? (
                            <div className="admin-notice">
                                <span>当前状态：{statusMeta[detail.moderationStatus]?.label ?? detail.moderationStatus} · 理由：{detail.moderationReason}</span>
                            </div>
                        ) : null}

                        <div className="admin-form-narrow flex flex-col gap-3">
                            <label className="flex flex-col gap-1">
                                <span className="admin-user-sub">处置状态</span>
                                <Select
                                    value={nextStatus}
                                    options={moderationOptions}
                                    style={{ maxWidth: 220 }}
                                    onChange={(value) => setNextStatus(value as AdminAssetModerationStatus)}
                                />
                            </label>
                            <label className="flex flex-col gap-1">
                                <span className="admin-user-sub">处置理由{nextStatus === "NORMAL" ? "（选填）" : "（必填，会写入审计留痕）"}</span>
                                <Input.TextArea
                                    rows={3}
                                    maxLength={200}
                                    showCount
                                    value={reason}
                                    placeholder={nextStatus === "NORMAL" ? "复核结论（选填）" : "例如：包含未授权的品牌素材"}
                                    onChange={(event) => setReason(event.target.value)}
                                />
                            </label>
                            <div className="flex items-center gap-2">
                                <Button danger={nextStatus !== "NORMAL"} loading={saving} onClick={requestModeration}>
                                    {nextStatus === "NORMAL" ? "确认恢复" : "提交处置"}
                                </Button>
                                <span className="admin-user-sub">隐藏后前台不再展示该素材；恢复后立即重新可见。</span>
                            </div>
                        </div>
                    </div>
                ) : null}
            </Drawer>

            <Modal
                open={confirmOpen}
                title={detail ? `${nextStatus === "NORMAL" ? "恢复" : nextStatus === "HIDDEN" ? "隐藏" : "删除"}《${detail.title || detail.id}》` : "确认处置"}
                okText={nextStatus === "NORMAL" ? "确认恢复" : "确认处置"}
                cancelText="取消"
                okButtonProps={{ danger: nextStatus !== "NORMAL" }}
                confirmLoading={saving}
                onOk={() => void submitModeration()}
                onCancel={() => setConfirmOpen(false)}
            >
                <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.7 }}>
                    {nextStatus === "NORMAL"
                        ? "恢复后该素材立即重新展示给用户。"
                        : `将把该素材标记为${nextStatus === "HIDDEN" ? "隐藏" : "删除"}；只改数据库里的处置结论，不清理对象存储文件。理由会写入审计留痕。`}
                </p>
                {nextStatus !== "NORMAL" ? <p className="admin-user-sub">理由：{reason.trim() || "（未填写）"}</p> : null}
            </Modal>
        </div>
    );
}
