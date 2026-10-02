import { resourceFileUrl, resourceIdFromStorageKey } from "@/services/api/resources";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

/**
 * 画布媒体节点的云端地址归一。
 *
 * 节点把展示地址存在 metadata.content 上，历史上写入过 blob:、data: 和本地
 * IndexedDB 的 objectURL。这些地址只对写下它的那个页面会话有意义，一旦换机器、
 * 换浏览器，或者本地存储被清理，节点就变成空白——而同一节点上的 storageKey 其实
 * 已经指向云端资源。这里把「能解析出资源 ID」的节点统一回落到云端 file URL，
 * 让持久化下来的画布本身就是云端可复现的。
 */

const MEDIA_NODE_TYPES: ReadonlySet<string> = new Set([
    CanvasNodeType.Image,
    CanvasNodeType.Video,
    CanvasNodeType.Audio,
    CanvasNodeType.Panorama,
]);

export function isCanvasMediaNodeType(type: string) {
    return MEDIA_NODE_TYPES.has(type);
}

/** blob:/data: 是页面态地址；空值同样不可跨会话复现。 */
export function isEphemeralMediaUrl(url?: string) {
    if (!url) return true;
    return url.startsWith("blob:") || url.startsWith("data:");
}

/** 由 storageKey 推导云端可访问地址；非云端 key 返回空串。 */
export function cloudMediaUrl(storageKey?: string) {
    const resourceId = resourceIdFromStorageKey(storageKey);
    return resourceId ? resourceFileUrl(resourceId) : "";
}

/**
 * 只收敛「已经是云端资源、但展示地址还是本地态」的节点。
 * 外链、第三方 URL 和本来就没有资源 ID 的节点一律不动，避免把别人的地址误改成本站资源。
 */
export function withCloudMediaContent(node: CanvasNodeData): CanvasNodeData {
    if (!node.metadata || !isCanvasMediaNodeType(node.type)) return node;
    const cloudUrl = cloudMediaUrl(node.metadata.storageKey);
    if (!cloudUrl) return node;
    if (node.metadata.content === cloudUrl) return node;
    if (!isEphemeralMediaUrl(node.metadata.content)) return node;
    return { ...node, metadata: { ...node.metadata, content: cloudUrl } };
}

export function withCloudMediaContentOnProject<T extends { nodes: CanvasNodeData[] }>(project: T): T {
    let changed = false;
    const nodes = project.nodes.map((node) => {
        const next = withCloudMediaContent(node);
        if (next !== node) changed = true;
        return next;
    });
    return changed ? { ...project, nodes } : project;
}
