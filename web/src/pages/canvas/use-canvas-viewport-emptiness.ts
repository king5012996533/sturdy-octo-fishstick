import { useMemo } from "react";

import { isCanvasNodeHiddenFromView } from "@/lib/canvas/canvas-project-domain";
import { viewportContainsNodes } from "@/lib/canvas/canvas-viewport";
import type { CanvasNodeData, ViewportTransform } from "@/types/canvas";

// 视窗是否滑出了全部节点（画布上有节点，但一个都看不见）。
//
// “看不见”的口径由 isCanvasNodeHiddenFromView 定义，和缩略图、适合屏幕、连线吸附共用同一套；
// 那份判定要按节点列表逐个问，是 O(n²)，所以只在节点变化时算一次并缓存 id，
// 逐帧的视窗判定只做一次 Set 查询加矩形相交，必须保持廉价。
export function useCanvasViewportEmptiness(nodes: CanvasNodeData[], viewport: ViewportTransform, viewportSize: { width: number; height: number }) {
    const invisibleNodeIds = useMemo(() => {
        const invisible = new Set<string>();
        for (const node of nodes) {
            if (isCanvasNodeHiddenFromView(node, nodes)) invisible.add(node.id);
        }
        return invisible;
    }, [nodes]);

    return useMemo(
        () => nodes.length > 0 && !viewportContainsNodes(nodes, viewport, viewportSize, { margin: 24, isHidden: (node) => invisibleNodeIds.has(node.id) }),
        [invisibleNodeIds, nodes, viewport, viewportSize],
    );
}
