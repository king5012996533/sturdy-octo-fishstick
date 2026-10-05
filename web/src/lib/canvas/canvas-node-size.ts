import { NODE_DEFAULT_SIZE } from "@/constant/canvas";
import { CanvasNodeType, isBuiltinCanvasNodeType, type CanvasNodeData } from "@/types/canvas";

export const MEDIA_NODE_MIN_SIZE = { width: 360, height: 202 } as const;
// 媒体卡片的展示上限。新建、上传、生成结果回填共用同一档，避免同一张素材在三条路径下
// 落成三种尺寸（上一版默认 720×520，拖进来的方图比新建的卡片还大）。
// 显式标注为 number：这个常量会作为 fitNodeSize 的默认参数，字面量类型会把调用方传进来的
// 动态宽高判成类型错误。
export const MEDIA_NODE_MAX_SIZE: { width: number; height: number } = { width: 560, height: 405 };
export const VIDEO_NODE_MAX_SIZE = MEDIA_NODE_MAX_SIZE;

/**
 * 用户是否手动定过这张卡片的几何。
 *
 * 生成结果回填默认按上游产物的真实比例重算宽高并居中，会把用户手动拉过的卡片压回默认
 * 尺寸（Windows 上表现为「卡片一直变」）。只要动过（锁定 / 自由比例 / 手动拉过）就原样保留。
 */
export function hasManualNodeGeometry(node: CanvasNodeData) {
    return Boolean(node.metadata?.locked || node.metadata?.freeResize || node.metadata?.manualSize);
}

export function fitNodeSize(width: number, height: number, maxWidth = MEDIA_NODE_MAX_SIZE.width, maxHeight = MEDIA_NODE_MAX_SIZE.height, minWidth = MEDIA_NODE_MIN_SIZE.width, minHeight = MEDIA_NODE_MIN_SIZE.height) {
    const w = Math.max(1, width);
    const h = Math.max(1, height);
    // 媒体节点既要保留原始比例，也要给生成状态、操作按钮留下稳定的可读空间。
    const preferredScale = Math.min(1, maxWidth / w, maxHeight / h);
    const minimumScale = Math.max(minWidth / w, minHeight / h);
    const scale = Math.max(preferredScale, minimumScale);
    return { width: w * scale, height: h * scale };
}

export function nodeSizeFromRatio(size: string, baseWidth: number, baseHeight: number) {
    const raw = String(size || "").trim();
    if (!raw || raw.toLowerCase() === "auto") return null;
    let width = 0;
    let height = 0;
    const match = raw.match(/^(\d+(?:\.\d+)?)(?:x|:)(\d+(?:\.\d+)?)/i);
    if (match) {
        width = Number(match[1]);
        height = Number(match[2]);
    } else if (raw.includes("竖") || raw.includes("portrait") || raw.includes("9:16")) {
        width = 9;
        height = 16;
    } else if (raw.includes("横") || raw.includes("landscape") || raw.includes("16:9")) {
        width = 16;
        height = 9;
    } else if (raw.includes("(1:1)") || raw.includes("1:1") || raw.includes("square")) {
        width = 1;
        height = 1;
    } else if (raw.includes("3:4")) {
        width = 3;
        height = 4;
    } else if (raw.includes("4:3")) {
        width = 4;
        height = 3;
    } else if (raw.includes("2:3")) {
        width = 2;
        height = 3;
    } else if (raw.includes("3:2")) {
        width = 3;
        height = 2;
    }
    if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) return null;
    const ratio = width / Math.max(1, height);
    if (ratio < 0.25 || ratio > 4) return { width: baseWidth, height: baseHeight };
    const candidateSize = ratio >= baseWidth / baseHeight ? { width: baseWidth, height: baseWidth / ratio } : { width: baseHeight * ratio, height: baseHeight };
    return fitNodeSize(candidateSize.width, candidateSize.height, baseWidth, baseHeight);
}

export function ensureMediaNodeMinimumSize(node: CanvasNodeData) {
    if (node.type !== CanvasNodeType.Image && node.type !== CanvasNodeType.Video) return node;
    const title = node.title === "New Generation" ? "图片" : node.title === "Video" ? "视频" : node.title;
    let width = node.width;
    let height = node.height;
    const emptyStage = isBuiltinCanvasNodeType(node.type) ? NODE_DEFAULT_SIZE[node.type] : undefined;

    // 如果未完成节点（生成中/失败/空节点）指定了目标比例（如 3:4, 9:16），按目标比例保持占位框尺寸，不能强制变成 16:9 横屏。
    const targetSize = node.metadata?.size ? nodeSizeFromRatio(node.metadata.size, emptyStage?.width || 720, emptyStage?.height || 405) : null;
    if (targetSize && !node.metadata?.content && !node.metadata?.freeResize && !node.metadata?.locked) {
        width = targetSize.width;
        height = targetSize.height;
    } else {
        const shouldPromoteEmptyStage = !node.metadata?.content && !node.metadata?.freeResize && !node.metadata?.locked && emptyStage !== undefined && (width <= 0 || height <= 0);
        if (shouldPromoteEmptyStage) {
            width = emptyStage.width;
            height = emptyStage.height;
        }
    }
    const naturalWidth = node.metadata?.naturalWidth || 0;
    const naturalHeight = node.metadata?.naturalHeight || 0;
    const requestedSize = node.type === CanvasNodeType.Image && node.metadata?.generationType === "edit" ? nodeSizeFromRatio(node.metadata.size || "auto", node.width, node.height) : null;
    const naturalRatio = naturalWidth / Math.max(1, naturalHeight);
    const nodeRatio = node.width / Math.max(1, node.height);
    // 修复旧版图生图无条件继承参考节点尺寸造成的比例错误，不覆盖自由拉伸或锁定布局。
    if (requestedSize && naturalWidth > 0 && naturalHeight > 0 && !node.metadata?.freeResize && !node.metadata?.locked && Math.abs(naturalRatio - nodeRatio) > 0.01) {
        const alignedSize = fitNodeSize(naturalWidth, naturalHeight, requestedSize.width, requestedSize.height);
        width = alignedSize.width;
        height = alignedSize.height;
    }
    if (width < MEDIA_NODE_MIN_SIZE.width || height < MEDIA_NODE_MIN_SIZE.height) {
        const scale = Math.max(1, MEDIA_NODE_MIN_SIZE.width / Math.max(1, width), MEDIA_NODE_MIN_SIZE.height / Math.max(1, height));
        width *= scale;
        height *= scale;
    }
    if (width === node.width && height === node.height && title === node.title) return node;
    return {
        ...node,
        title,
        position: {
            x: node.position.x + node.width / 2 - width / 2,
            y: node.position.y + node.height / 2 - height / 2,
        },
        width,
        height,
    };
}
