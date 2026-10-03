/**
 * 广场"查看更多"的分页节奏与展开反馈。
 *
 * 这里原先只有一句 setLimit(count => count + 12)：新卡片全部落在首屏之外，按钮文案、
 * 页脚索引条都不动，点完和没点一样 —— 用户直接判定这个按钮"点不动、是个占位符"。
 * 所以把节奏抽出来，让按钮文案、页脚计数、"把第一张新卡带回视野"用同一份数字。
 */

/** 首屏张数：bento 四列，13 张正好铺满四行，末尾不留缺口。 */
export const CREATION_INSPIRATION_PAGE_SIZE = 13;

/** 每次展开的张数：三行，一次加满，省得连点。 */
export const CREATION_INSPIRATION_PAGE_STEP = 12;

export type CreationInspirationProgress = {
    shown: number;
    total: number;
    remaining: number;
};

/** 把 limit 和总数折成"已展示 / 总数 / 剩余"，按钮与页脚共用一份口径。 */
export function creationInspirationProgress(limit: number, total: number): CreationInspirationProgress {
    const ceiling = Math.max(0, Math.trunc(total));
    const shown = Math.max(0, Math.min(Math.trunc(limit), ceiling));
    return { shown, total: ceiling, remaining: ceiling - shown };
}

/** 展开后第一张新卡的序号；已经没有新卡时返回 null，调用方据此不滚动。 */
export function creationInspirationRevealIndex(limit: number, total: number): number | null {
    const { shown, remaining } = creationInspirationProgress(limit, total);
    return remaining > 0 ? shown : null;
}

/**
 * 把第 index 张卡卷回视野。用 block:"nearest" 只滚最小距离：新卡通常紧贴视口下沿，
 * 这样点一下就能看见多了东西，又不会把整个广场推走。跟随"减少动态效果"系统设置。
 */
export function revealCreationInspirationCard(container: HTMLElement | null, index: number | null) {
    if (!container || index === null || index < 0) return;
    const card = container.querySelectorAll<HTMLElement>(".creation-featured-card")[index];
    if (!card) return;
    const reduced = typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    card.scrollIntoView({ behavior: reduced ? "auto" : "smooth", block: "nearest" });
}
