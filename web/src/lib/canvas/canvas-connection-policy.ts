import { getNodeInputKind, getNodeLabel, getNodeMaxInputCount } from "@/lib/canvas/node-registry";
import type { ModelInputSummary } from "@/lib/model-selection";
import type { CanvasConnection, CanvasNodeData } from "@/types/canvas";

type ConnectionCandidate = Pick<CanvasConnection, "fromNodeId" | "toNodeId">;

/**
 * 连线表达的是项目结构——"这张卡在上游、那张卡在下游"，不等于这张卡会被当成素材用上：
 * 用户可以先把线索全连起来，再在提示词里 @ 其中几张。
 *
 * 所以这里不再按卡片类型、媒体种类或模型参考容量拦连线：那是"引用"的约束，
 * 留到生成那一刻由模型能力声明收口（图片张数见 canvasImageReferenceLimitError，
 * 视频见 assertVideoCapability）。保留的只有"只吃一路"这类节点自身的结构上限，
 * 例如转换节点同时接两路就无法判断该转哪一个。
 */
export function canvasConnectionError(_config: unknown, nodes: CanvasNodeData[], connections: CanvasConnection[], candidate: ConnectionCandidate) {
    const target = nodes.find((node) => node.id === candidate.toNodeId);
    if (!target) return "找不到连线目标节点";
    const maxInputCount = getNodeMaxInputCount(target.type);
    if (maxInputCount) {
        const inputCount = new Set(
            [...connections, { id: "candidate", ...candidate }]
                .filter((connection) => connection.toNodeId === target.id)
                .map((connection) => connection.fromNodeId),
        ).size;
        if (inputCount > maxInputCount) return `${getNodeLabel(target.type)}节点最多连接 ${maxInputCount} 个输入`;
    }
    return "";
}

export function connectionInputSummary(targetNodeId: string, nodes: CanvasNodeData[], connections: CanvasConnection[], candidate?: ConnectionCandidate): ModelInputSummary {
    const sourceIds = new Set([...connections, ...(candidate ? [{ id: "candidate", ...candidate }] : [])].filter((connection) => connection.toNodeId === targetNodeId).map((connection) => connection.fromNodeId));
    const input: ModelInputSummary = { textCount: 0, imageCount: 0, videoCount: 0, audioCount: 0, characterCount: 0 };
    sourceIds.forEach((sourceId) => {
        const source = nodes.find((node) => node.id === sourceId);
        if (!source) return;
        // 生成配置与背板不是参考素材，不参与容量计数——这一步必须早于角色卡判定，
        // 否则一个带角色元数据的配置/背板节点会被多算成角色。
        const inputKind = getNodeInputKind(source.type);
        if (!inputKind) return;
        // 角色卡是跨类型覆盖：落在可计数类型上时改记为角色。
        if (source.metadata?.workflowKind === "character") input.characterCount += 1;
        else if (inputKind !== "table_data") input[`${inputKind}Count`] += 1;
    });
    return input;
}
