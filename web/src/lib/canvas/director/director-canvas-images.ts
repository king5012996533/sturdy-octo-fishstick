import type { CanvasNodeData } from "@/types/canvas";
import type { DirectorObject } from "@/types/director";

export type DirectorCanvasImageRow = {
    node: CanvasNodeData;
    /** 画布上的原始顺序，用来保证同组内不发生额外重排。 */
    index: number;
    /** 画布上有一条连线指向本导演节点。 */
    connected: boolean;
    /** 已经在当前场景里放了立牌。 */
    inScene: boolean;
};

/**
 * 导演台「画布图片」入口的呈现顺序：已连线到本节点的排最前，其次是已经放进场景当立牌的，
 * 其余保持画布原始顺序。连线是画布侧关系，立牌是导演台侧关系，两者都只用来排序和标注——
 * 谁都不是"删掉画布节点"的理由。
 */
export function buildDirectorCanvasImageRows(imageNodes: readonly CanvasNodeData[], connectedImageNodeIds: readonly string[], billboardSourceNodeIds: readonly string[]): DirectorCanvasImageRow[] {
    const connected = new Set(connectedImageNodeIds);
    const inScene = new Set(billboardSourceNodeIds);
    return imageNodes
        .map((node, index) => ({ node, index, connected: connected.has(node.id), inScene: inScene.has(node.id) }))
        .sort((left, right) => Number(right.connected) - Number(left.connected) || Number(right.inScene) - Number(left.inScene) || left.index - right.index);
}

/** 当前场景里引用了画布节点的立牌，取它们指向的画布节点 id。 */
export function directorBillboardSourceNodeIds(objects: readonly DirectorObject[]): string[] {
    return objects.filter((item) => item.kind === "billboard" && item.sourceNodeId).map((item) => item.sourceNodeId as string);
}
