export type CanvasScreenBounds = { left: number; top: number; right: number; bottom: number };

/**
 * 把一块屏幕坐标矩形挪进安全区所需的最小平移量；已在安全区内时返回 null。
 *
 * 从边缘拉线新建卡片后只允许平移、不允许改倍率。重算倍率会让整块画布在新建卡片的
 * 瞬间同时位移并缩放，用户看到的是镜头被撑开、画面抖动。所以这里只回答"挪多少"，
 * 倍率由调用方保持不变。
 *
 * 某个轴上目标已经把安全区整个盖住时（目标比视口还大）该轴不平移：此时无论怎么挪
 * 都露不全，挪反而是一次大幅跳屏。
 */
export function resolveCanvasMinimalPan(bounds: CanvasScreenBounds, safe: CanvasScreenBounds): { x: number; y: number } | null {
    if (safe.right <= safe.left || safe.bottom <= safe.top) return null;
    let x = 0;
    let y = 0;
    const spansX = bounds.left <= safe.left && bounds.right >= safe.right;
    const spansY = bounds.top <= safe.top && bounds.bottom >= safe.bottom;
    if (!spansX) {
        if (bounds.right > safe.right) x = safe.right - bounds.right;
        else if (bounds.left < safe.left) x = safe.left - bounds.left;
    }
    if (!spansY) {
        if (bounds.bottom > safe.bottom) y = safe.bottom - bounds.bottom;
        else if (bounds.top < safe.top) y = safe.top - bounds.top;
    }
    return x || y ? { x, y } : null;
}
