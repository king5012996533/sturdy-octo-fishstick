import { Button, DatePicker, Input, Select, Switch, Table, Tag, Tooltip, type TableProps } from "antd";
import type { Dayjs } from "dayjs";
import { AlertTriangle, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { formatBytes, formatCount, formatDateTime } from "@/lib/format-usage";

import { listAdminResources, type AdminResource, type AdminResourceReconciliation, type AdminResourceTotals } from "./api-resources";
import { MediaPreview } from "./media-preview";
import { chargeStateColor, chargeStateLabel, ResourcesReconciliation } from "./resources-reconciliation";

const kindOptions = [
    { value: "", label: "全部类型" },
    { value: "image", label: "图片" },
    { value: "video", label: "视频" },
    { value: "audio", label: "音频" },
    { value: "text", label: "文本" },
];

const kindLabels: Record<string, string> = Object.fromEntries(kindOptions.filter((option) => option.value).map((option) => [option.value, option.label]));

function kindLabel(kind: string) {
    return kindLabels[kind] ?? (kind || "—");
}

const emptyTotals: AdminResourceTotals = { total: 0, unreferenced: 0, untracked: 0, totalBytes: 0, users: 0 };

/**
 * 列表筛选条件。
 *
 * 加载、重试、回填后刷新都要原样带上同一份条件，散在五处各写一遍的话，漏掉一个字段
 * 就会静默查到另一个口径——那正是这一页最不该出的错。
 */
type ResourceListFilters = {
    keyword: string;
    kind: string;
    unreferenced: boolean;
    untracked: boolean;
    tracked: boolean;
    range: [Dayjs | null, Dayjs | null] | null;
    page: number;
    pageSize: number;
};

/** 时长读成秒，比毫秒更贴近运营对视频、音频的直觉。 */
function formatDuration(durationMs: number) {
    if (!Number.isFinite(durationMs) || durationMs <= 0) return "—";
    if (durationMs >= 60_000) return `${Math.floor(durationMs / 60_000)}分${Math.round((durationMs % 60_000) / 1000)}秒`;
    return `${(durationMs / 1000).toFixed(1)}秒`;
}

function formatDimension(resource: AdminResource) {
    if (resource.width > 0 && resource.height > 0) return `${resource.width}×${resource.height}`;
    if (resource.durationMs > 0) return formatDuration(resource.durationMs);
    return "—";
}

/**
 * 生成产物对账。
 *
 * 与「素材管理」的区别必须写清楚：那边读的是客户端回写的 assets，这边读的是 resources
 * 全量。用户端回写失败或产物根本没进素材库、画布时，只有这里能看到——上游扣了钱、
 * 产出了结果，前台却是失败提示，损失要对得上就得靠这一页。
 */
export function ResourcesPane() {
    const [keywordInput, setKeywordInput] = useState("");
    const [keyword, setKeyword] = useState("");
    const [kind, setKind] = useState("");
    const [unreferencedOnly, setUnreferencedOnly] = useState(false);
    const [untrackedOnly, setUntrackedOnly] = useState(false);
    const [trackedOnly, setTrackedOnly] = useState(false);
    const [range, setRange] = useState<[Dayjs | null, Dayjs | null] | null>(null);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [resources, setResources] = useState<AdminResource[]>([]);
    const [total, setTotal] = useState(0);
    const [totals, setTotals] = useState<AdminResourceTotals>(emptyTotals);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [reconciliation, setReconciliation] = useState<AdminResourceReconciliation | null>(null);
    const [reconciliationOpen, setReconciliationOpen] = useState(false);

    const load = useCallback(
        async (options: ResourceListFilters) => {
            setLoading(true);
            setError("");
            try {
                const payload = await listAdminResources({
                    keyword: options.keyword,
                    kind: options.kind,
                    unreferenced: options.unreferenced,
                    untracked: options.untracked,
                    tracked: options.tracked,
                    since: options.range?.[0]?.startOf("day").toISOString(),
                    until: options.range?.[1]?.endOf("day").toISOString(),
                    page: options.page,
                    pageSize: options.pageSize,
                });
                setResources(payload.resources ?? []);
                setTotal(payload.total ?? 0);
                setTotals(payload.totals ?? emptyTotals);
                setReconciliation(payload.reconciliation ?? null);
            } catch (loadError) {
                setError(loadError instanceof Error ? loadError.message : "加载生成产物失败");
            } finally {
                setLoading(false);
            }
        },
        [],
    );

    const filters = useMemo<ResourceListFilters>(
        () => ({
            keyword,
            kind,
            unreferenced: unreferencedOnly,
            untracked: untrackedOnly,
            tracked: trackedOnly,
            range,
            page,
            pageSize,
        }),
        [keyword, kind, unreferencedOnly, untrackedOnly, trackedOnly, range, page, pageSize],
    );

    useEffect(() => {
        void load(filters);
    }, [load, filters]);

    // 输入即查询会把每一次按键都变成一次列表请求，这里做 300ms 防抖。
    useEffect(() => {
        const timer = setTimeout(() => {
            setPage(1);
            setKeyword(keywordInput.trim());
        }, 300);
        return () => clearTimeout(timer);
    }, [keywordInput]);

    const columns: TableProps<AdminResource>["columns"] = [
        {
            title: "预览",
            key: "preview",
            width: 132,
            render: (_, resource) => (
                <div className="admin-resource-preview-cell">
                    <MediaPreview kind={resource.kind} src={resource.previewUrl} />
                </div>
            ),
        },
        {
            title: "产物",
            key: "id",
            render: (_, resource) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-sub admin-canvas-id">{resource.id}</span>
                    <span className="admin-user-sub">{resource.objectKey || "—"}</span>
                </div>
            ),
        },
        {
            title: "账号",
            key: "owner",
            width: 190,
            render: (_, resource) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-name">{resource.userName || resource.userId}</span>
                    <span className="admin-user-sub">{resource.userId}</span>
                </div>
            ),
        },
        { title: "类型", key: "kind", width: 90, render: (_, resource) => <span className="admin-user-sub">{kindLabel(resource.kind)}</span> },
        { title: "尺寸 / 时长", key: "dimension", width: 110, render: (_, resource) => <span className="admin-user-sub">{formatDimension(resource)}</span> },
        { title: "大小", key: "size", width: 92, render: (_, resource) => <span className="admin-user-sub">{formatBytes(resource.size)}</span> },
        {
            title: "用户是否拿到",
            key: "referenced",
            width: 150,
            render: (_, resource) =>
                resource.referenced ? (
                    <Tag color="green">已入素材库 / 画布</Tag>
                ) : (
                    <Tooltip title="既不在素材库，也没在任何画布上：上游可能已产出，但用户端没拿到">
                        <Tag color="red">用户没拿到</Tag>
                    </Tooltip>
                ),
        },
        {
            title: "关联任务",
            key: "task",
            width: 220,
            render: (_, resource) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-sub admin-canvas-id">{resource.taskId || "—（未关联）"}</span>
                    <span className="admin-user-sub">
                        {resource.taskType || resource.source || "—"}
                        {resource.providerRequestId ? ` · ${resource.providerRequestId}` : ""}
                    </span>
                </div>
            ),
        },
        {
            title: "扣费",
            key: "charge",
            width: 150,
            render: (_, resource) => (
                <span className="flex min-w-0 flex-col gap-1">
                    <Tag color={chargeStateColor(resource.chargeState)}>{chargeStateLabel(resource.chargeState)}</Tag>
                    {resource.chargedCredits ? <span className="admin-user-sub">{formatCount(resource.chargedCredits)} 积分</span> : null}
                </span>
            ),
        },
        {
            title: "状态",
            key: "status",
            width: 130,
            render: (_, resource) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-sub">{resource.status}{resource.provider ? ` · ${resource.provider}` : ""}</span>
                    {resource.error ? (
                        <Tooltip title={resource.error}>
                            <span className="admin-user-sub admin-canvas-reason">{resource.error}</span>
                        </Tooltip>
                    ) : null}
                </div>
            ),
        },
        { title: "创建时间", dataIndex: "createdAt", key: "createdAt", width: 168, render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span> },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">生成产物</h2>
                    <p className="admin-section-desc">
                        读的是产物表全量，不是客户端回写的素材库。凡是「上游已产出、用户端却没拿到」的产物都会在这里亮出来，用来对账；预览地址现场签发，12 小时后失效。
                    </p>
                </div>
                <div className="flex items-center gap-2">
                    <Button
                        icon={<AlertTriangle className="size-3.5" />}
                        danger={(reconciliation?.uncharged ?? 0) > 0 || (reconciliation?.chargedWithoutResource ?? 0) > 0}
                        onClick={() => setReconciliationOpen(true)}
                    >
                        对账异常
                    </Button>
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load(filters)}>
                        刷新
                    </Button>
                </div>
            </div>

            <div className="admin-metric-grid">
                <div className="admin-metric">
                    <span className="admin-metric-label">产物总数</span>
                    <span className="admin-metric-value">{formatCount(totals.total)}</span>
                    <span className="admin-metric-note">涉及账号 {formatCount(totals.users)}</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">用户没拿到</span>
                    <span className="admin-metric-value">{formatCount(totals.unreferenced)}</span>
                    <span className="admin-metric-note">不在素材库也不在任何画布</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">未关联任务</span>
                    <span className="admin-metric-value">{formatCount(totals.untracked)}</span>
                    <span className="admin-metric-note">上传素材与回填后仍对不上的历史数据</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">漏扣费</span>
                    <span className="admin-metric-value">{formatCount(reconciliation?.uncharged ?? 0)}</span>
                    <span className="admin-metric-note">计费上线后产出却没有任何扣费</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">扣费无产物</span>
                    <span className="admin-metric-value">{formatCount(reconciliation?.chargedWithoutResource ?? 0)}</span>
                    <span className="admin-metric-note">扣了费的媒体任务没有任何产物</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">占用空间</span>
                    <span className="admin-metric-value">{formatBytes(totals.totalBytes)}</span>
                    <span className="admin-metric-note">按产物记录的文件大小合计</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">涉及账号</span>
                    <span className="admin-metric-value">{formatCount(totals.users)}</span>
                    <span className="admin-metric-note">有产物的账号数</span>
                </div>
            </div>

            <div className="admin-toolbar">
                <Input
                    allowClear
                    value={keywordInput}
                    prefix={<Search className="size-3.5" />}
                    placeholder="搜索产物 ID / 账号 ID / 账号名"
                    style={{ width: 260 }}
                    onChange={(event) => setKeywordInput(event.target.value)}
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
                <DatePicker.RangePicker
                    value={range}
                    allowClear
                    style={{ width: 260 }}
                    onChange={(value) => {
                        setPage(1);
                        setRange(value);
                    }}
                />
                <span className="flex items-center gap-2">
                    <Switch
                        size="small"
                        checked={unreferencedOnly}
                        onChange={(checked) => {
                            setPage(1);
                            setUnreferencedOnly(checked);
                        }}
                    />
                    <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>只看用户没拿到的</span>
                </span>
                <span className="flex items-center gap-2">
                    <Switch
                        size="small"
                        checked={untrackedOnly}
                        onChange={(checked) => {
                            setPage(1);
                            setUntrackedOnly(checked);
                            // 「未关联任务」与「生成的」互为补集，同时打开只会得到空表。
                            if (checked) setTrackedOnly(false);
                        }}
                    />
                    <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>只看未关联任务</span>
                </span>
                <span className="flex items-center gap-2">
                    <Switch
                        size="small"
                        checked={trackedOnly}
                        onChange={(checked) => {
                            setPage(1);
                            setTrackedOnly(checked);
                            if (checked) setUntrackedOnly(false);
                        }}
                    />
                    <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>只看生成的</span>
                </span>
                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {formatCount(total)} 条产物</span>
            </div>

            {error ? (
                <div className="admin-notice is-error">
                    <span>{error}</span>
                    <Button size="small" type="text" onClick={() => void load(filters)}>
                        重试
                    </Button>
                </div>
            ) : null}

            {!loading && resources.length === 0 ? <div className="admin-card admin-empty">没有符合条件的产物。对账时先确认筛选条件，再放大时间范围。</div> : null}

            <div className="admin-card" style={!loading && resources.length === 0 ? { display: "none" } : undefined}>
                <Table<AdminResource>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={resources}
                    columns={columns}
                    scroll={{ x: 1500 }}
                    pagination={{
                        current: page,
                        pageSize,
                        total,
                        size: "small",
                        showSizeChanger: true,
                        showTotal: (value) => `共 ${value} 条产物`,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setPageSize(nextSize);
                        },
                    }}
                />
            </div>

            <ResourcesReconciliation
                open={reconciliationOpen}
                reconciliation={reconciliation}
                onClose={() => setReconciliationOpen(false)}
                onBackfilled={() => void load(filters)}
            />
        </div>
    );
}
