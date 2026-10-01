import type { ViewportTransform } from "@/types/canvas";

/**
 * 滚轮整档缩放的"滑行"曲线。
 *
 * 鼠标滚轮一格是 14% 的离散跳变（见 canvas-wheel-zoom）。直接把它写进视口，画面就是
 * 一格一格弹——用户的原话是"每个动作都像复位一样"。这里把"当前视口"按指数曲线拉向
 * "目标视口"：每来一格只把目标往前推一格，画面自己追上去，观感接近推镜（由远到近）。
 *
 * 为什么用指数趋近而不是"固定时长缓动"：连续滚动时目标会在半路变化，固定时长缓动每次
 * 重新开始，速度会出现断点（忽快忽慢）；指数趋近的速度是连续的，快的滚动只是目标更远。
 */

/**
 * 时间常数：越大越"重"。
 *
 * 70ms 时第一帧就吃掉约 21% 的距离（手感跟手），一次滚轮整档缩放约 260ms 走完，
 * 接近手机拍照那种快速推近的节奏。
 */
export const CANVAS_ZOOM_GLIDE_TIME_CONSTANT_MS = 70;
/** 单帧最多按 64ms 计：后台标签页恢复时不要一帧跳出去很远。 */
const MAX_GLIDE_FRAME_MS = 64;
/** 到位判定：倍率相对误差与屏幕像素误差都要够小，否则会拖着最后一点点走很久。 */
const GLIDE_SETTLE_SCALE_RATIO = 0.003;
const GLIDE_SETTLE_SCREEN_PX = 1;

/** 按帧间隔把当前视口向目标推进一步。deltaMs<=0 时原样返回，便于首帧不跳。 */
export function glideViewportTowards(current: ViewportTransform, target: ViewportTransform, deltaMs: number): ViewportTransform {
    const elapsed = Math.min(Math.max(deltaMs, 0), MAX_GLIDE_FRAME_MS);
    if (elapsed === 0) return current;
    const progress = 1 - Math.exp(-elapsed / CANVAS_ZOOM_GLIDE_TIME_CONSTANT_MS);
    return {
        x: current.x + (target.x - current.x) * progress,
        y: current.y + (target.y - current.y) * progress,
        k: current.k + (target.k - current.k) * progress,
    };
}

/** 尊重系统的"减少动态效果"：开启时整档缩放直接落位，不做滑行。 */
export function canvasZoomGlideEnabled() {
    return typeof window !== "undefined" && !window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/**
 * 是否已经追上目标。
 *
 * 位置按屏幕像素判定，倍率按相对误差判定：两者都收敛才收尾，避免"倍率到位了但画面还在
 * 缓慢平移"时提前提交，把最后几像素的位移留到静止期闪一下。
 */
export function isViewportGlideSettled(current: ViewportTransform, target: ViewportTransform) {
    if (Math.abs(current.k - target.k) > target.k * GLIDE_SETTLE_SCALE_RATIO) return false;
    return Math.abs(current.x - target.x) <= GLIDE_SETTLE_SCREEN_PX && Math.abs(current.y - target.y) <= GLIDE_SETTLE_SCREEN_PX;
}
