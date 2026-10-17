import { Button, Segmented, Table, Tag, type TableProps } from "antd";
import { RefreshCw, SearchCheck } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import { getAdminChannelHealth, reconcileAdminChannelModels, type AdminChannelHealthResult, type AdminChannelHealthRow, type AdminChannelReconcileResult, type AdminChannelReconcileRow } from "./api";

const windows = [
    { label: "今日", value: 1 },
    { label: "近 7 天", value: 7 },
    { label: "近 30 天", value: 30 },
];

function errorMessage(error: unknown, fallback: string) {
    if (error && typeof error === "object" && "message" in error) {
        const message = String((error as { message?: unknown }).message || "");
        if (message) return message;
    }
    return fallback;
}

const reconcileStatusMeta: Record<string, { label: string; color: string }> = {
    ok: { label: "一致", color: "green" },
    drift: { label: "有缺失", color: "red" },
    error: { label: "读取失败", color: "volcano" },
    unsupported: { label: "不支持巡检", color: "default" },
    empty: { label: "无在售模型", color: "default" },
};

const modelStatusMeta: Record<string, { label: string; color: string }> = {
    ok: { label: "一致", color: "green" },
    missing: { label: "上游已无", color: "red" },
    partial: { label: "部分缺失", color: "orange" },
    unknown: { label: "无法核对", color: "default" },
};

function statusTag(meta: Record<string, { label: string; color: string }>, status: string) {
    const item = meta[status] ?? { label: status || "未知", color: "default" };
    return <Tag color={item.color}>{item.label}</Tag>;
}

/**
 * 渠道健康。
 *
 * 两块读数解决的是同一个问题：「货架上还有没有坏按钮」。
 *
 * - 上游对账是零成本的：只读每条渠道的模型目录，不发起生成；它抓的是
 *   "上游悄悄撤掉某个模型标识"（例如 Fmini 消失）这类不会在后台报错的问题。
 * - 失败看板读 api_call_logs 的 create 记录，按渠道 × 模型看失败率与错误码；
 *   poll/download 是同一次提交的后续动作，不算进去，否则一条失败会被放大成十次。
 */
export function ChannelHealthPane() {
    const [days, setDays] = useState(7);
    const [health, setHealth] = useState<AdminChannelHealthResult | null>(null);
    const [healthLoading, setHealthLoading] = useState(true);
    const [healthError, setHealthError] = useState("");

    const [reconcile, setReconcile] = useState<AdminChannelReconcileResult | null>(null);
    const [reconcileBusy, setReconcileBusy] = useState(false);
    const [reconcileError, setReconcileError] = useState("");

    const loadHealth = useCallback(async (windowDays: number) => {
        setHealthLoading(true);
        setHealthError("");
        try {
            setHealth(await getAdminChannelHealth(windowDays));
        } catch (loadError) {
            setHealthError(errorMessage(loadError, "加载失败看板失败"));
        } finally {
            setHealthLoading(false);
        }
    }, []);

    useEffect(() => {
        void loadHealth(days);
    }, [days, loadHealth]);

    const runReconcile = useCallback(async () => {
        setReconcileBusy(true);
        setReconcileError("");
        try {
            setReconcile(await reconcileAdminChannelModels());
        } catch (runError) {
            setReconcileError(errorMessage(runError, "上游对账失败"));
        } finally {
            setReconcileBusy(false);
        }
    }, []);

    const reconcileColumns: TableProps<AdminChannelReconcileRow>["columns"] = [
        {
            title: "渠道",
            key: "channel",
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{row.channelName}</span>
                    <span className="admin-user-sub">{row.baseUrl || row.channelId}</span>
                </span>
            ),
        },
        {
            title: "状态",
            dataIndex: "status",
            key: "status",
            width: 120,
            render: (value: string) => statusTag(reconcileStatusMeta, value),
        },
        {
            title: "上游目录",
            dataIndex: "upstreamCount",
            key: "upstreamCount",
            width: 110,
            render: (value: number, row) => (row.status === "ok" || row.status === "drift" ? formatCount(value) : "—"),
        },
        {
            title: "在售模型",
            key: "models",
            width: 110,
            render: (_, row) => (row.status === "ok" || row.status === "drift" ? formatCount(row.models.length) : "—"),
        },
        {
            title: "缺失",
            key: "missing",
            render: (_, row) => {
                if (row.status !== "drift") return <span className="admin-user-sub">{row.error || "—"}</span>;
                const missing = row.models.filter((item) => item.status !== "ok");
                return (
                    <span className="flex flex-wrap gap-1">
                        {missing.map((item) => (
                            <Tag key={item.modelKey} color={item.status === "partial" ? "orange" : "red"}>
                                {item.modelKey}
                            </Tag>
                        ))}
                    </span>
                );
            },
        },
    ];

    const modelColumns: TableProps<AdminChannelReconcileRow["models"][number]>["columns"] = [
        { title: "模型", dataIndex: "modelKey", key: "modelKey" },
        {
            title: "上游标识",
            key: "keys",
            render: (_, row) => <span className="admin-console-mono">{row.upstreamKeys.join(" / ") || "—"}</span>,
        },
        {
            title: "状态",
            dataIndex: "status",
            key: "status",
            width: 110,
            render: (value: string) => statusTag(modelStatusMeta, value),
        },
        {
            title: "缺失 / 无法确认",
            key: "missingKeys",
            render: (_, row) => (
                <span className="flex flex-wrap gap-1">
                    {row.missingKeys.map((key) => (
                        <Tag key={`missing-${key}`} color="red">
                            {key}
                        </Tag>
                    ))}
                    {row.unverifiedKeys.map((key) => (
                        <Tag key={`unverified-${key}`}>{key}（无法确认）</Tag>
                    ))}
                    {!row.missingKeys.length && !row.unverifiedKeys.length ? <span className="admin-user-sub">—</span> : null}
                </span>
            ),
        },
    ];

    const healthColumns: TableProps<AdminChannelHealthRow>["columns"] = [
        {
            title: "渠道",
            dataIndex: "channelName",
            key: "channelName",
            render: (value: string, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{value}</span>
                    <span className="admin-user-sub">{row.channelId}</span>
                </span>
            ),
        },
        { title: "模型", dataIndex: "model", key: "model", render: (value: string) => <span className="admin-console-mono">{value}</span> },
        { title: "提交", dataIndex: "total", key: "total", width: 90, render: (value: number) => formatCount(value) },
        { title: "成功", dataIndex: "succeeded", key: "succeeded", width: 90, render: (value: number) => formatCount(value) },
        {
            title: "失败",
            dataIndex: "failed",
            key: "failed",
            width: 90,
            render: (value: number) => <span style={value > 0 ? { color: "var(--admin-danger, #ff6b6b)" } : undefined}>{formatCount(value)}</span>,
        },
        {
            title: "失败率",
            dataIndex: "failureRate",
            key: "failureRate",
            width: 100,
            render: (value: number, row) => (row.total > 0 ? `${(value * 100).toFixed(1)}%` : "—"),
        },
        {
            title: "错误码",
            key: "errorCodes",
            render: (_, row) =>
                row.errorCodes.length ? (
                    <span className="flex flex-wrap gap-1">
                        {row.errorCodes.map((item) => (
                            <Tag key={`${item.code}-${item.statusCode}`}>
                                {item.code || "unknown"} × {item.count}
                            </Tag>
                        ))}
                    </span>
                ) : (
                    <span className="admin-user-sub">—</span>
                ),
        },
        {
            title: "最近一次失败",
            key: "lastError",
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-sub" style={{ maxWidth: 320, overflow: "hidden", textOverflow: "ellipsis" }}>
                        {row.lastError || "—"}
                    </span>
                    <span className="admin-user-sub">{row.lastErrorAt ? formatDateTime(row.lastErrorAt) : ""}</span>
                </span>
            ),
        },
    ];

    const summary = reconcile?.summary;
    const totals = health?.totals;
    const totalFailureRate = totals && totals.total > 0 ? ((totals.failed / totals.total) * 100).toFixed(1) : "0.0";

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">渠道健康</h2>
                    <p className="admin-section-desc">上游目录对账只读模型清单、不发起生成；失败看板按渠道与模型统计提交结果，用来判断哪条货还撑得住。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Segmented value={days} options={windows} onChange={(value) => setDays(Number(value))} />
                    <Button icon={<RefreshCw className="size-3.5" />} loading={healthLoading} onClick={() => void loadHealth(days)}>
                        刷新
                    </Button>
                </div>
            </div>

            {healthError ? (
                <div className="admin-notice is-error">
                    <span>{healthError}</span>
                    <Button size="small" onClick={() => void loadHealth(days)}>
                        重试
                    </Button>
                </div>
            ) : null}

            <div className="admin-card">
                <div className="admin-card-head">
                    <span className="flex min-w-0 flex-col">
                        <b style={{ fontSize: "var(--fs-body)" }}>上游模型对账</b>
                        <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>拿每条渠道的授权模型清单比对在售模型；上游撤掉标识、改名或换货都会在这里显形</span>
                    </span>
                    <Button icon={<SearchCheck className="size-3.5" />} loading={reconcileBusy} onClick={() => void runReconcile()}>
                        开始巡检
                    </Button>
                </div>
                <div className="admin-card-pad">
                    {reconcileError ? <div className="admin-notice is-error">{reconcileError}</div> : null}
                    {!reconcile ? (
                        <div className="admin-empty">还没有巡检记录。点「开始巡检」读取上游模型清单，只读、不产生调用费用。</div>
                    ) : (
                        <div className="flex flex-col gap-3">
                            <p style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>
                                {formatDateTime(reconcile.checkedAt)} · 已探测 {summary?.checked ?? 0} 条渠道 · 有缺失 {summary?.drifted ?? 0} 条 · 读取失败 {summary?.failed ?? 0} 条
                                {(summary?.unsupported ?? 0) + (summary?.empty ?? 0) > 0 ? ` · 未巡检 ${(summary?.unsupported ?? 0) + (summary?.empty ?? 0)} 条（协议不支持或无在售模型）` : ""}
                            </p>
                            <Table<AdminChannelReconcileRow>
                                rowKey="channelId"
                                size="small"
                                columns={reconcileColumns}
                                dataSource={reconcile.rows}
                                pagination={false}
                                expandable={{
                                    expandedRowRender: (row) =>
                                        row.models.length ? <Table rowKey="modelKey" size="small" columns={modelColumns} dataSource={row.models} pagination={false} /> : <span className="admin-user-sub">{row.error || "没有可对账的模型"}</span>,
                                    rowExpandable: (row) => row.models.length > 0,
                                }}
                            />
                        </div>
                    )}
                </div>
            </div>

            <div className="admin-card">
                <div className="admin-card-head">
                    <span className="flex min-w-0 flex-col">
                        <b style={{ fontSize: "var(--fs-body)" }}>提交失败看板</b>
                        <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>只统计 create 调用（一次生成算一次）；提交与轮询、下载分开，避免同一次失败被重复计数</span>
                    </span>
                </div>
                <div className="admin-card-pad">
                    {healthLoading && !health ? (
                        <div className="admin-empty">加载中…</div>
                    ) : !health || health.rows.length === 0 ? (
                        <div className="admin-empty">所选窗口内还没有提交记录。</div>
                    ) : (
                        <div className="flex flex-col gap-3">
                            <p style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>
                                合计提交 {formatCount(totals?.total ?? 0)} · 成功 {formatCount(totals?.succeeded ?? 0)} · 失败 {formatCount(totals?.failed ?? 0)}（{totalFailureRate}%）
                            </p>
                            <Table<AdminChannelHealthRow> rowKey={(row) => `${row.channelId}::${row.model}`} size="small" columns={healthColumns} dataSource={health.rows} pagination={health.rows.length > 20 ? { pageSize: 20, size: "small" } : false} />
                        </div>
                    )}
                </div>
            </div>

            {health?.generatedAt ? (
                <p style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>
                    数据生成于 {formatDateTime(health.generatedAt)}，窗口自 {formatDateTime(health.from)} 起
                </p>
            ) : null}
        </div>
    );
}
