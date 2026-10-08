/**
 * 节点渲染用的缩放分档。
 *
 * 缩放的原始倍率每 64ms 变化一次（虚拟化节拍）。如果把它原样传给每个节点，节点的
 * memo 比较每拍都会失败，于是缩放到几百个节点时整层节点逐帧重渲染——实测 136 个
 * 节点时平均帧从 17ms 掉到 31ms、最差帧 217ms。
 *
 * 分档后节点只在跨越档位时才重渲染。连续变化的视觉量（外置标题的最大宽度）改由
 * CSS 变量逐帧驱动，因此分档不会让它在缩放中"跳档"。
 *
 * 档位必须**精确包含 0.35 与 0.5**：节点内部用它们判断外置标题与分镜子标题是否隐藏，
 * 只有档位值本身等于阈值，`bucket < 阈值` 才与 `scale < 阈值` 完全等价。
 */
export const CANVAS_NODE_SCALE_STOPS = [0.1, 0.14, 0.2, 0.25, 0.3, 0.35, 0.42, 0.5, 0.6, 0.71, 0.85, 1, 1.2, 1.41, 1.7, 2, 2.4, 2.83, 3.4, 4] as const;

export function canvasNodeScaleBucket(scale: number): number {
    if (!Number.isFinite(scale) || scale <= 0) return 1;
    let bucket: number = CANVAS_NODE_SCALE_STOPS[0];
    for (const stop of CANVAS_NODE_SCALE_STOPS) {
        if (stop > scale) break;
        bucket = stop;
    }
    return bucket;
}
