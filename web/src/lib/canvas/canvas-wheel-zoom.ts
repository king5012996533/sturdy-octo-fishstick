/**
 * 画布滚轮手势的意图判定与步长归一化。
 *
 * 之前缩放幅度直接拿事件里的像素数除以固定值（72），并且靠"deltaY 是不是 100 的整数倍"
 * 来判断"这是鼠标滚轮还是触控板"。这两个假设只在部分设备成立：
 *  - Windows 鼠标一档可以是 100 / 120 / 80 / 53 像素，行模式（deltaMode=1）则是 3 行 × 16px；
 *    同一个物理档位换算出来的倍率因此忽大忽小，用户感受就是"放大缩小不规则"；
 *  - 档距不是 100 的整数倍时会被判成触控板平移，于是滚轮在缩放和平移之间来回跳。
 *
 * 这里把两件事分开做：先判意图（缩放 / 平移），再把像素数归一化成"档位数"，
 * 一档永远等于同一个倍率，设备档距只影响"一档有多少像素"，不影响缩放幅度。
 */

/** 像素模式下一档的参考像素数：Windows Chrome 鼠标常见 100。 */
const WHEEL_NOTCH_PIXELS = 100;
/** 行模式（deltaMode=1）下一档按 3 行算，浏览器一行按 16px 折算。 */
const LINES_PER_NOTCH = 3;
const LINE_HEIGHT_PIXELS = 16;
/** 触控板捏合（ctrl + 滚轮）每档像素数；捏合是连续量，不能用整档取整。 */
const PINCH_NOTCH_PIXELS = 24;
/** 单次事件最多缩放几档：躲避平滑滚动一次喷出上千像素。 */
const MAX_NOTCHES_PER_EVENT = 3;
/**
 * 小于这个像素量按触控板处理。
 * 鼠标一档最少也在 40px 上下，触控板纵向平移基本在 1~20px，"一档"和"一小段"由此分开。
 */
const POINTER_NOTCH_MIN_PIXELS = 40;

/** 一档滚轮的缩放倍率。所有设备共用，保证同一台机器每滚一格的幅度一致。 */
export const CANVAS_WHEEL_ZOOM_RATIO = 1.14;

export const CANVAS_MIN_SCALE = 0.05;
export const CANVAS_MAX_SCALE = 2;

export type CanvasWheelIntent =
    | { kind: "none" }
    | { kind: "pan"; deltaX: number; deltaY: number }
    /**
     * source 让调用方决定"要不要滑行"：
     * - notch：鼠标滚轮的整档跳变，需要滑行成连续推镜；
     * - pinch：触控板捏合是连续量，直接跟手，再滑一层只会变钝。
     */
    | { kind: "zoom"; notches: number; source: "notch" | "pinch" };

export type CanvasWheelInput = {
    deltaX: number;
    deltaY: number;
    deltaMode: number;
    shiftKey: boolean;
    ctrlKey: boolean;
    metaKey: boolean;
};

export function clampCanvasScale(scale: number) {
    return Math.min(Math.max(scale, CANVAS_MIN_SCALE), CANVAS_MAX_SCALE);
}

/** 行 / 页模式统一折算成像素，后续判定只用像素量。 */
export function canvasWheelDeltaToPixels(delta: number, deltaMode: number) {
    if (deltaMode === 1) return delta * LINE_HEIGHT_PIXELS;
    if (deltaMode === 2) return delta * 720;
    return delta;
}

/**
 * 判定滚轮意图。
 *
 * 带横向分量或按住 shift 一律当平移：这是触控板双手势的常态，也是画布一直以来的手感，
 * 不能因为"想支持鼠标滚轮缩放"而把横滚误判成缩放。
 */
export function resolveCanvasWheelIntent(input: CanvasWheelInput): CanvasWheelIntent {
    const deltaX = canvasWheelDeltaToPixels(input.deltaX, input.deltaMode);
    const deltaY = canvasWheelDeltaToPixels(input.deltaY, input.deltaMode);
    if (deltaX === 0 && deltaY === 0) return { kind: "none" };

    const absX = Math.abs(deltaX);
    const absY = Math.abs(deltaY);
    if (input.shiftKey || absX > 0) {
        // shift + 纵向滚轮 = 横向平移；此时纵向分量归零，避免斜着跑。
        const horizontalOnly = input.shiftKey && absX < 1;
        return { kind: "pan", deltaX: horizontalOnly ? deltaY : deltaX, deltaY: horizontalOnly ? 0 : deltaY };
    }

    const pinch = input.ctrlKey || input.metaKey;
    // 捏合：触控板给的是连续小量。鼠标 Ctrl+滚轮是整档，走下面的分支。
    if (pinch && input.deltaMode === 0 && absY < POINTER_NOTCH_MIN_PIXELS) {
        return { kind: "zoom", notches: clampNotches(deltaY / PINCH_NOTCH_PIXELS), source: "pinch" };
    }
    if (input.deltaMode !== 0 || absY >= POINTER_NOTCH_MIN_PIXELS) {
        return { kind: "zoom", notches: wheelNotchCount(input.deltaY, input.deltaMode), source: "notch" };
    }
    return { kind: "pan", deltaX, deltaY };
}

/** 一档对应一个固定倍率；notches 为正表示缩小，为负表示放大。 */
export function canvasWheelZoomFactor(notches: number) {
    return Math.pow(CANVAS_WHEEL_ZOOM_RATIO, -notches);
}

/**
 * 把一次滚轮事件的位移换算成档位数。
 *
 * 注意这里吃的是**原始位移**：行 / 页模式各自的单位不同，先折算成像素再按像素取整
 * 会把"3 行"读成"16 档"。每种模式在自己的单位里取整，最后统一成档位。
 */
function wheelNotchCount(rawDelta: number, deltaMode: number) {
    const sign = rawDelta < 0 ? -1 : 1;
    const magnitude = Math.abs(rawDelta);
    if (deltaMode === 1) return sign * clampNotchCount(Math.round(magnitude / LINES_PER_NOTCH));
    // 页模式一次就是一档：换算成像素会读成 7 档。
    if (deltaMode === 2) return sign * clampNotchCount(Math.round(magnitude));
    // 像素模式：设备档距 100 / 120 / 80 / 53 / 42 一律吸到"整档"，
    // 同一台机器每滚一格的缩放幅度因此完全一致。
    return sign * clampNotchCount(Math.round(magnitude / WHEEL_NOTCH_PIXELS));
}

function clampNotchCount(count: number) {
    return Math.min(Math.max(count, 1), MAX_NOTCHES_PER_EVENT);
}

function clampNotches(notches: number) {
    return Math.min(Math.max(notches, -MAX_NOTCHES_PER_EVENT), MAX_NOTCHES_PER_EVENT);
}
