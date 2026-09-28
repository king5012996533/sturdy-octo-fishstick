/**
 * 用量读数的展示格式。
 *
 * 仪表盘（管理后台）与账户页（用户端）展示的是同一批数字，格式必须一致：
 * 两处各写一份四舍五入，很快就会出现"后台说 1.5GB、账户页说 1.4GB"这类对不上的读数。
 */

export function formatCount(value: number) {
    if (!Number.isFinite(value)) return "0";
    return Math.trunc(value).toLocaleString("zh-CN");
}

export function formatBytes(value: number) {
    if (!Number.isFinite(value) || value <= 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB", "PB"];
    const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
    const scaled = value / 1024 ** index;
    return `${scaled >= 100 || index === 0 ? Math.round(scaled) : scaled.toFixed(1)} ${units[index]}`;
}

export function formatTokens(value: number) {
    if (!Number.isFinite(value) || value <= 0) return "0";
    if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
    if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
    return formatCount(value);
}

export function formatDateTime(value?: string) {
    if (!value) return "—";
    const at = new Date(value);
    return Number.isNaN(at.getTime()) ? "—" : at.toLocaleString("zh-CN", { hour12: false });
}
