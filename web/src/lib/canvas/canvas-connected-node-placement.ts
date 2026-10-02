import type { ConnectionHandle, CanvasNodeData, Position } from "@/types/canvas";

type NodeSize = {
    width: number;
    height: number;
};

export function connectedNodeCenterFromEdgeDrop(dropPosition: Position, nodeSize: NodeSize, handleType: "source" | "target"): Position {
    return {
        x: dropPosition.x + (handleType === "source" ? nodeSize.width / 2 : -nodeSize.width / 2),
        y: dropPosition.y,
    };
}

/**
 * Keep quick-created nodes on the side of their real source and avoid
 * stacking repeated outputs on top of one another. Positions are world-space
 * centers, while node data stores top-left coordinates.
 */
export function placeConnectedNodeWithoutOverlap(
    nodes: CanvasNodeData[],
    source: CanvasNodeData | undefined,
    handleType: ConnectionHandle["handleType"],
    anchorY: number,
    size: NodeSize,
): Position {
    if (!source) return { x: 0, y: anchorY };

    const gap = 36;
    const left = handleType === "source"
        ? source.position.x + source.width + 96
        : source.position.x - 96 - size.width;
    let top = anchorY - size.height / 2;

    // Resolve vertical collisions deterministically. Repeated outputs are
    // stacked downward, preserving the source-side alignment and leaving a
    // small LibTV-like breathing gap between cards.
    for (let pass = 0; pass < nodes.length + 1; pass += 1) {
        let moved = false;
        for (const node of nodes) {
            if (node.id === source.id) continue;
            const overlapsX = left < node.position.x + node.width + gap
                && left + size.width + gap > node.position.x;
            const overlapsY = top < node.position.y + node.height + gap
                && top + size.height + gap > node.position.y;
            if (overlapsX && overlapsY) {
                top = node.position.y + node.height + gap;
                moved = true;
            }
        }
        if (!moved) break;
    }

    return { x: left + size.width / 2, y: top + size.height / 2 };
}

/**
 * 从某个连接点拖出并新建节点时的落点。
 *
 * 锚点取源节点上真实的握手位置，而不是指针松开的世界坐标：点击快捷连接点
 * 和拖拽到空白处释放都会走到这里，用源节点做锚点，重复连线才会稳定地排在同一侧，
 * 而不是随指针位置漂移。
 */
export function resolveConnectedNodePlacement(
    nodes: CanvasNodeData[],
    pending: { connection: ConnectionHandle; position: Position },
    size: NodeSize,
): Position {
    const source = nodes.find((node) => node.id === pending.connection.nodeId);
    if (!source) return connectedNodeCenterFromEdgeDrop(pending.position, size, pending.connection.handleType);
    const anchorY = source.position.y + source.height * (pending.connection.anchorRatio ?? 0.5);
    return placeConnectedNodeWithoutOverlap(nodes, source, pending.connection.handleType, anchorY, size);
}
