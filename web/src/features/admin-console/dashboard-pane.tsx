import { Button, Segmented } from "antd";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatBytes, formatCount, formatTokens } from "@/lib/format-usage";

import { getAdminOverview, type AdminOverview, type AdminTrendPoint } from "./api";

const windows = [
    { label: "近 7 天", value: 7 },
    { label: "近 30 天", value: 30 },
];

const emptyOverview: AdminOverview = {
    users: { total: 0, active: 0, disabled: 0, admins: 0, newUsers: 0 },
    canvases: 0,
    activeCanvases: 0,
    assets: 0,
    storedBytes: 0,
    calls: 0,
    failedCalls: 0,
    inputTokens: 0,
    outputTokens: 0,
    channels: 0,
    enabledChannels: 0,
    models: 0,
    enabledModels: 0,
    trend: [],
    days: 7,
    generatedAt: "",
};

function dayLabel(day: string) {
    const [, month, date] = day.split("-");
    return `${month}/${date}`;
}

/**
 * 调用量趋势。
 *
 * 手写 SVG 而不是引入图表库：这里只有一条折线加一层面积，图表库带来的主题适配
 * 和 canvas 尺寸问题比收益大。缺日已由服务端补零，所以下标即日期。
 */
function TrendChart({ points }: { points: AdminTrendPoint[] }) {
    if (points.length === 0) {
        return <div className="admin-empty">所选窗口内还没有模型调用记录</div>;
    }
    const width = 720;
    const height = 168;
    const padding = { top: 12, right: 8, bottom: 22, left: 8 };
    const plotWidth = width - padding.left - padding.right;
    const plotHeight = height - padding.top - padding.bottom;
    const peak = Math.max(...points.map((point) => point.calls), 1);
    const step = points.length > 1 ? plotWidth / (points.length - 1) : 0;
    const x = (index: number) => padding.left + (points.length > 1 ? index * step : plotWidth / 2);
    const y = (value: number) => padding.top + plotHeight - (value / peak) * plotHeight;
    const line = points.map((point, index) => `${index === 0 ? "M" : "L"}${x(index).toFixed(1)},${y(point.calls).toFixed(1)}`).join(" ");
    const area = `${line} L${x(points.length - 1).toFixed(1)},${padding.top + plotHeight} L${x(0).toFixed(1)},${padding.top + plotHeight} Z`;
    // 日期标签在 30 天窗口下会挤在一起，按窗口长度抽稀。
    const labelStride = Math.ceil(points.length / 7);

    return (
        <div>
            <div className="admin-trend-legend">
                <span>调用量峰值 {formatCount(peak)}</span>
                <span>窗口内合计 {formatCount(points.reduce((sum, point) => sum + point.calls, 0))}</span>
            </div>
            <svg className="admin-trend" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label="调用量趋势">
                <defs>
                    <linearGradient id="admin-trend-fill" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stopColor="rgba(120,190,255,0.34)" />
                        <stop offset="100%" stopColor="rgba(120,190,255,0)" />
                    </linearGradient>
                </defs>
                {[0, 0.5, 1].map((ratio) => (
                    <line
                        key={ratio}
                        x1={padding.left}
                        x2={width - padding.right}
                        y1={padding.top + plotHeight * ratio}
                        y2={padding.top + plotHeight * ratio}
                        stroke="rgba(255,255,255,0.08)"
                        strokeWidth={1}
                    />
                ))}
                <path d={area} fill="url(#admin-trend-fill)" />
                <path d={line} fill="none" stroke="#7cc0ff" strokeWidth={1.6} strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
                {points.map((point, index) => (
                    <g key={point.day}>
                        <circle cx={x(index)} cy={y(point.calls)} r={2.4} fill="#0b0c10" stroke="#7cc0ff" strokeWidth={1.4} />
                        <title>{`${point.day} · 调用 ${point.calls} · 失败 ${point.failedCalls} · 输入 ${point.inputTokens} / 输出 ${point.outputTokens} tokens`}</title>
                    </g>
                ))}
                {points.map((point, index) =>
                    index % labelStride === 0 || index === points.length - 1 ? (
                        <text key={point.day} x={x(index)} y={height - 6} textAnchor="middle" fontSize={10} fill="rgba(255,255,255,0.34)">
                            {dayLabel(point.day)}
                        </text>
                    ) : null,
                )}
            </svg>
        </div>
    );
}

export function DashboardPane() {
    const [days, setDays] = useState(7);
    const [overview, setOverview] = useState<AdminOverview>(emptyOverview);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");

    const load = useCallback(async (windowDays: number) => {
        setLoading(true);
        setError("");
        try {
            setOverview(await getAdminOverview(windowDays));
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载仪表盘失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load(days);
    }, [days, load]);

    const windowLabel = `近 ${days} 天`;
    const failureRate = overview.calls > 0 ? (overview.failedCalls / overview.calls) * 100 : 0;

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">仪表盘</h2>
                    <p className="admin-section-desc">账号、画布、调用量与存储的实时读数；趋势按天归并，缺数据的日期补零。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Segmented value={days} options={windows} onChange={(value) => setDays(Number(value))} />
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load(days)}>
                        刷新
                    </Button>
                </div>
            </div>

            {error ? (
                <div className="admin-notice is-error">
                    <span>{error}</span>
                    <Button size="small" onClick={() => void load(days)}>重试</Button>
                </div>
            ) : null}

            <div className="admin-metric-grid">
                <div className="admin-metric">
                    <span className="admin-metric-label">总用户</span>
                    <span className="admin-metric-value">{formatCount(overview.users.total)}</span>
                    <span className="admin-metric-note">
                        活跃 {formatCount(overview.users.active)} · 封禁 {formatCount(overview.users.disabled)} · 管理员 {formatCount(overview.users.admins)}
                    </span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">新增用户</span>
                    <span className="admin-metric-value">{formatCount(overview.users.newUsers)}</span>
                    <span className="admin-metric-note">{windowLabel}注册</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">画布</span>
                    <span className="admin-metric-value">{formatCount(overview.canvases)}</span>
                    <span className="admin-metric-note">{windowLabel}活跃 {formatCount(overview.activeCanvases)}</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">AI 调用量</span>
                    <span className="admin-metric-value">{formatCount(overview.calls)}</span>
                    <span className="admin-metric-note">
                        失败 {formatCount(overview.failedCalls)}
                        {overview.calls > 0 ? `（${failureRate.toFixed(1)}%）` : ""}
                    </span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">Token 消耗</span>
                    <span className="admin-metric-value">{formatTokens(overview.inputTokens + overview.outputTokens)}</span>
                    <span className="admin-metric-note">输入 {formatTokens(overview.inputTokens)} · 输出 {formatTokens(overview.outputTokens)}</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">存储用量</span>
                    <span className="admin-metric-value">{formatBytes(overview.storedBytes)}</span>
                    <span className="admin-metric-note">素材 {formatCount(overview.assets)} 个（去重后统计）</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">渠道</span>
                    <span className="admin-metric-value">{formatCount(overview.channels)}</span>
                    <span className="admin-metric-note">启用 {formatCount(overview.enabledChannels)}</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">可售模型</span>
                    <span className="admin-metric-value">{formatCount(overview.models)}</span>
                    <span className="admin-metric-note">启用 {formatCount(overview.enabledModels)}</span>
                </div>
            </div>

            <div className="admin-card">
                <div className="admin-card-head">
                    <span className="flex min-w-0 flex-col">
                        <b style={{ fontSize: "var(--fs-body)" }}>调用趋势</b>
                        <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>按天统计模型调用次数，鼠标悬停查看 token 明细</span>
                    </span>
                </div>
                <div className="admin-card-pad">
                    <TrendChart points={overview.trend} />
                </div>
            </div>

            {overview.generatedAt ? (
                <p style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>
                    数据生成于 {new Date(overview.generatedAt).toLocaleString("zh-CN")}
                </p>
            ) : null}
        </div>
    );
}
