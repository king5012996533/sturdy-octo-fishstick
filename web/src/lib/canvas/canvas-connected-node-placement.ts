import type { ConnectionHandle, CanvasNodeData, Position } from "@/types/canvas";

type NodeSize = {
    width: number;
    height: number;
};

/** 卡片之间保留的呼吸间距，与锚定摆放保持一致。 */
const PLACEMENT_GAP = 36;

export function connectedNodeCenterFromEdgeDrop(dropPosition: Position, nodeSize: NodeSize, handleType: "source" | "target"): Position {
    return {
        x: dropPosition.x + (handleType === "source" ? nodeSize.width / 2 : -nodeSize.width / 2),
        y: dropPosition.y,
    };
}

function overlapsAt(node: CanvasNodeData, left: number, top: number, size: NodeSize): boolean {
    return left < node.position.x + node.width + PLACEMENT_GAP
        && left + size.width + PLACEMENT_GAP > node.position.x
        && top < node.position.y + node.height + PLACEMENT_GAP
        && top + size.height + PLACEMENT_GAP > node.position.y;
}

/**
 * 锚定摆放用的让位：只向下推。
 *
 * 同侧重复输出要稳定地往下排，所以这里刻意只动纵向，并跳过"本来就应该贴着"的源卡片。
 */
function pushDownUntilClear(nodes: CanvasNodeData[], left: number, top: number, size: NodeSize, ignoreNodeId?: string): number {
    let resolvedTop = top;
    for (let pass = 0; pass < nodes.length + 1; pass += 1) {
        let moved = false;
        for (const node of nodes) {
            if (node.id === ignoreNodeId) continue;
            if (overlapsAt(node, left, resolvedTop, size)) {
                resolvedTop = node.position.y + node.height + PLACEMENT_GAP;
                moved = true;
            }
        }
        if (!moved) break;
    }
    return resolvedTop;
}

/**
 * 自由摆放用的让位：四个方向里挑位移最小的那个让开。
 *
 * 一律向下推会让「拖到源卡片右侧一点点」变成「整张卡片掉到源卡片下方」——用户感觉卡片
 * 没落在鼠标上。这里改为最小位移，落点附近只要有缝就只挪一点点。
 * 位移相同时按拖拽方向优先（向右拉出去的走右边），其次纵向。
 */
function shiftToClearMinimally(nodes: CanvasNodeData[], left: number, top: number, size: NodeSize, handleType: ConnectionHandle["handleType"]): { left: number; top: number } {
    let resolvedLeft = left;
    let resolvedTop = top;
    for (let pass = 0; pass < nodes.length + 1; pass += 1) {
        const hit = nodes.find((node) => overlapsAt(node, resolvedLeft, resolvedTop, size));
        if (!hit) break;
        const rightward = { dx: hit.position.x + hit.width + PLACEMENT_GAP - resolvedLeft, dy: 0 };
        const leftward = { dx: hit.position.x - PLACEMENT_GAP - size.width - resolvedLeft, dy: 0 };
        const upward = { dx: 0, dy: hit.position.y - PLACEMENT_GAP - size.height - resolvedTop };
        const downward = { dx: 0, dy: hit.position.y + hit.height + PLACEMENT_GAP - resolvedTop };
        const ordered = handleType === "target" ? [leftward, upward, downward, rightward] : [rightward, upward, downward, leftward];
        let best = ordered[0];
        for (const candidate of ordered) {
            if (Math.abs(candidate.dx) + Math.abs(candidate.dy) < Math.abs(best.dx) + Math.abs(best.dy)) best = candidate;
        }
        resolvedLeft += best.dx;
        resolvedTop += best.dy;
    }
    return { left: resolvedLeft, top: resolvedTop };
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

    const left = handleType === "source"
        ? source.position.x + source.width + 96
        : source.position.x - 96 - size.width;

    // Resolve vertical collisions deterministically. Repeated outputs are
    // stacked downward, preserving the source-side alignment and leaving a
    // small LibTV-like breathing gap between cards. The source itself is the
    // deliberate neighbour, so it is excluded from the collision set.
    const top = pushDownUntilClear(nodes, left, anchorY - size.height / 2, size, source.id);

    return { x: left + size.width / 2, y: top + size.height / 2 };
}

/**
 * 按用户松手的位置摆放新节点，但不允许盖住任何已有卡片（含源卡片）。
 *
 * 卡片中心就是落点；只有在压到别的卡片时才按最小位移让开。落点本来就空着时，
 * 位置与松手点完全一致。
 */
export function placeConnectedNodeAtDrop(nodes: CanvasNodeData[], dropPosition: Position, size: NodeSize, handleType: ConnectionHandle["handleType"]): Position {
    const { left, top } = shiftToClearMinimally(nodes, dropPosition.x - size.width / 2, dropPosition.y - size.height / 2, size, handleType);
    return { x: left + size.width / 2, y: top + size.height / 2 };
}

/**
 * 锚定摆放：把新节点贴在源节点同侧，而不是放在指针松开的世界坐标。
 *
 * 只用于「点击快捷连接点」这种没有位移的手势——它没有真实落点，用源节点做锚点才不会
 * 把新卡片压在源卡片上。拖拽释放请走 resolveConnectedNodeCreatePosition，不要直接用它。
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

export type PendingConnectedNodeCreate = {
    connection: ConnectionHandle;
    position: Position;
    /** 点击快捷连接点（指针几乎没位移）时为 true；拖拽释放时为 falsy。 */
    quick?: boolean;
    batchSourceNodeIds?: string[];
};

/**
 * 从连线新建节点时，新节点世界坐标（中心）的最终决策。
 *
 * 拖到空白处释放就用松手的位置——用户已经明确指了落点，卡片就该出现在那里，而不是
 * 一律贴到源节点右侧。落点压在卡片上时只向下让位，不横向挪走，也不盖住源卡片。
 * 只有点击快捷连接点（quick）没有位移、落点等于源节点边缘时，才回退到锚定同侧摆放。
 */
export function resolveConnectedNodeCreatePosition(nodes: CanvasNodeData[], pending: PendingConnectedNodeCreate, size: NodeSize): Position {
    if (pending.quick && !pending.batchSourceNodeIds?.length) return resolveConnectedNodePlacement(nodes, pending, size);
    // 批量连接的落点与排版由 planBatchConnections 统一处理，这里保持原始落点。
    if (pending.batchSourceNodeIds?.length) return pending.position;
    return placeConnectedNodeAtDrop(nodes, pending.position, size, pending.connection.handleType);
}
