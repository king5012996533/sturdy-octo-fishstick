import { useCallback, useEffect, useLayoutEffect, useRef, useState, type Dispatch, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type SetStateAction } from "react";
import { App } from "antd";
import { nanoid } from "nanoid";
import { latchCanvasConnectionApproach, type CanvasConnectionApproach } from "@/lib/canvas/canvas-connection-tilt";

import type { PendingConnectionCreate } from "@/components/canvas/canvas-workspace-overlays";
import { getNodeSpec } from "@/constant/canvas";
import { batchSourceRestriction, buildBatchConnectionCreateRequest, hasBatchConnectionCandidate, planBatchConnections, type CanvasBatchConnectionPreview } from "@/lib/canvas/canvas-batch-connection";
import { batchReferenceHandleAtY } from "@/lib/canvas/canvas-batch-table";
import { resolveConnectedNodeCreatePosition } from "@/lib/canvas/canvas-connected-node-placement";
import { canvasConnectionError } from "@/lib/canvas/canvas-connection-policy";
import { attachNodeToStoryboardRow, createCanvasNode, getConnectionTargetAnchor, isHiddenBatchChild, normalizeConnection, storyboardHandleAtY, storyboardPromptTemplateMetadata, storyboardRowFromHandle } from "@/lib/canvas/canvas-project-domain";
import { createCanvasDrawingFromImage } from "@/lib/canvas/canvas-drawing-storage";
import type { CanvasDrawingEngine } from "@/lib/canvas/canvas-drawing-engine";
import { isFrameNode, isNodeHiddenByCollapsedFrame } from "@/lib/canvas/canvas-frame";
import { canOpenCanvasNodePromptPanel } from "@/lib/canvas/canvas-node-semantics";
import { normalizeRunningHubCapability, type AiConfig } from "@/stores/use-config-store";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata, type ConnectionHandle, type ContextMenuState, type Position, type ViewportTransform } from "@/types/canvas";
import { workflowProviderPluginEnabled } from "@/lib/plugins/builtin/workflows";
import { usePluginStore } from "@/stores/use-plugin-store";

type UseCanvasConnectionControllerOptions = {
    projectId: string;
    config: AiConfig;
    defaultDrawingEngine: CanvasDrawingEngine;
    nodesRef: { current: CanvasNodeData[] };
    connectionsRef: { current: CanvasConnection[] };
    viewportRef: { current: ViewportTransform };
    scriptScrollTopById: Record<string, number>;
    screenToCanvas: (clientX: number, clientY: number) => Position;
    setNodes: Dispatch<SetStateAction<CanvasNodeData[]>>;
    setConnections: Dispatch<SetStateAction<CanvasConnection[]>>;
    setSelectedNodeIds: Dispatch<SetStateAction<Set<string>>>;
    setSelectedConnectionId: Dispatch<SetStateAction<string | null>>;
    setContextMenu: Dispatch<SetStateAction<ContextMenuState | null>>;
    setDialogNodeId: Dispatch<SetStateAction<string | null>>;
    setDrawingNodeId: Dispatch<SetStateAction<string | null>>;
    onConnectedNodeCreated?: (node: CanvasNodeData, sourceNodeId?: string) => void;
    /**
     * 从连接点拖出时创建导演台节点。
     *
     * 导演台的场景状态归 useCanvasDirector 管，这里只借它的创建能力：本 hook 负责
     * 「新建的节点接上哪条线」，节点从哪来由调用方决定，避免两个模块互相 import。
     * 返回 null 表示导演台当前不可用，菜单会给出提示而不是留下一个悬空节点。
     */
    createDirectorNode?: (position: Position) => CanvasNodeData | null;
    onReplaceReference?: (targetNodeId: string, oldReference: { id: string; nodeId?: string; label?: string; title?: string }, sourceNodeId: string) => void;
};

export type ConnectionReplaceHover = {
    targetNodeId: string;
    referenceLabel: string;
    clientX: number;
    clientY: number;
};

type ConnectionDropTarget = {
    nodeId: string | null;
    handleId?: string;
    anchorRatio?: number;
    isNearNode: boolean;
};

type BatchConnectionDropTarget = ConnectionDropTarget;

// LibTV's 80px world-space quick-add zone renders at roughly 110px on the
// reference canvas. Use the same generous 112px screen-space diameter here,
// while retaining a circular boundary around the corresponding side anchor.
const CONNECTION_SNAP_RADIUS = 56;
/**
 * 连线拖拽期间做「是否悬停在参考芯片上」命中探测的最小间隔。
 *
 * 这个判定只驱动一个瞬态悬停提示，40ms（约 25Hz）肉眼无差别；但它每次都要调用
 * elementFromPoint，会强制同步样式与布局，在拖拽这种布局本就脏的场景里是实打实的
 * 每帧开销。
 */
const CONNECTION_REPLACE_HOVER_PROBE_MS = 40;

/** 距上次命中探测是否已超过节流间隔。抽出来是为了让这条约束可被测试钉住。 */
export function shouldProbeConnectionReplaceHover(lastProbeAt: number, now: number): boolean {
    return now - lastProbeAt >= CONNECTION_REPLACE_HOVER_PROBE_MS;
}

export const CONNECTION_REPLACE_HOVER_PROBE_INTERVAL_MS = CONNECTION_REPLACE_HOVER_PROBE_MS;
const NODE_STATUS_IDLE = "idle" as const;

function selectRunningHubWorkflow(config: AiConfig) {
    const capability = normalizeRunningHubCapability(config.runningHub.capability);
    return config.runningHub.workflows.find((item) => item.workflowId.trim() === config.runningHub.workflowId.trim()
        && (item.kind === "app" ? "app" : "workflow") === config.runningHub.selectedKind)
        || config.runningHub.workflows.find((item) => normalizeRunningHubCapability(item.capability, capability) === capability)
        || config.runningHub.workflows[0];
}

export function useCanvasConnectionController({
    projectId,
    config,
    defaultDrawingEngine,
    nodesRef,
    connectionsRef,
    viewportRef,
    scriptScrollTopById,
    screenToCanvas,
    setNodes,
    setConnections,
    setSelectedNodeIds,
    setSelectedConnectionId,
    setContextMenu,
    setDialogNodeId,
    setDrawingNodeId,
    onConnectedNodeCreated,
    createDirectorNode,
    onReplaceReference,
}: UseCanvasConnectionControllerOptions) {
    const { message } = App.useApp();
    const runtimeStatuses = usePluginStore((state) => state.runtimeStatuses);
    const [connectingParams, setConnectingParams] = useState<ConnectionHandle | null>(null);
    const [connectionTargetNodeId, setConnectionTargetNodeId] = useState<string | null>(null);
    const [connectionApproach, setConnectionApproach] = useState<CanvasConnectionApproach>(null);
    const [connectionTargetAnchorRatio, setConnectionTargetAnchorRatio] = useState<number | undefined>();
    const [connectionReplaceHover, setConnectionReplaceHover] = useState<ConnectionReplaceHover | null>(null);
    const [pendingConnectionCreate, setPendingConnectionCreate] = useState<PendingConnectionCreate | null>(null);
    const [batchConnectionPreview, setBatchConnectionPreview] = useState<CanvasBatchConnectionPreview | null>(null);
    /**
     * 草稿线的起点快照。
     *
     * 拖拽期间鼠标位置每帧都在变，但它只驱动 Leafer 覆盖层上的那条草稿线。逐帧
     * setState 会让整个画布页面（含全部节点）每帧重渲染一次，实测一次拖拽掉 4 帧。
     * 所以逐帧走 connectionDraftRef 的命令式入口，这个 state 只在拖拽开始时写入，
     * 作为首帧与外部读取者的种子值。
     */
    const [mouseWorld, setMouseWorld] = useState<Position>({ x: 0, y: 0 });
    const connectionDraftRef = useRef<((point: Position) => void) | null>(null);
    const connectingParamsRef = useRef(connectingParams);
    const connectingPointerIdRef = useRef<number | null>(null);
    const connectingPointerStartRef = useRef<Position | null>(null);
    const pendingConnectionCreateRef = useRef(pendingConnectionCreate);
    const batchConnectionPreviewRef = useRef<CanvasBatchConnectionPreview | null>(null);
    const batchConnectionPointerIdRef = useRef<number | null>(null);
    const batchConnectionPointerStartRef = useRef<Position | null>(null);
    const pointerMoveFrameRef = useRef<number | null>(null);
    const latestPointerMoveRef = useRef<PointerEvent | null>(null);
    /** 上次做 elementFromPoint 命中的时间；见 flushPointerMove 里的节流说明。 */
    const replaceHoverProbeAtRef = useRef(0);
    const hoveredReplaceElRef = useRef<HTMLElement | null>(null);

    /** 交给 Leafer 图层注册逐帧草稿更新入口；未挂载时静默降级为无草稿线。 */
    const registerConnectionDraft = useCallback((update: ((point: Position) => void) | null) => {
        connectionDraftRef.current = update;
    }, []);

    const updateConnectionReplaceHover = useCallback((element: HTMLElement | null, clientX = 0, clientY = 0) => {
        if (hoveredReplaceElRef.current === element) {
            if (element) {
                setConnectionReplaceHover((prev) => prev ? { ...prev, clientX, clientY } : null);
            }
            return;
        }
        if (hoveredReplaceElRef.current) {
            hoveredReplaceElRef.current.removeAttribute("data-connection-hover-replace");
        }
        hoveredReplaceElRef.current = element;
        if (element) {
            element.setAttribute("data-connection-hover-replace", "true");
            const targetNodeId = element.getAttribute("data-target-node-id") || "";
            const referenceLabel = element.getAttribute("data-reference-label") || "参考图";
            setConnectionReplaceHover({ targetNodeId, referenceLabel, clientX, clientY });
        } else {
            setConnectionReplaceHover(null);
        }
    }, []);

    useLayoutEffect(() => {
        connectingParamsRef.current = connectingParams;
        pendingConnectionCreateRef.current = pendingConnectionCreate;
    }, [connectingParams, pendingConnectionCreate]);

    const updateBatchConnectionPreview = useCallback((next: CanvasBatchConnectionPreview | null) => {
        batchConnectionPreviewRef.current = next;
        setBatchConnectionPreview(next);
        setConnectionApproach((previous) => latchCanvasConnectionApproach(previous, next && next.status !== "invalid" ? next.targetNodeId : null, next?.mouseWorld || { x: 0, y: 0 }));
    }, []);

    const clearBatchConnection = useCallback(() => {
        batchConnectionPointerIdRef.current = null;
        batchConnectionPointerStartRef.current = null;
        updateBatchConnectionPreview(null);
    }, [updateBatchConnectionPreview]);

    const setConnecting = useCallback((next: ConnectionHandle | null) => {
        connectingParamsRef.current = next;
        setConnectingParams(next);
        if (!next) {
            updateConnectionReplaceHover(null);
            setConnectionApproach(null);
            connectingPointerIdRef.current = null;
            connectingPointerStartRef.current = null;
            setConnectionTargetNodeId(null);
            setConnectionTargetAnchorRatio(undefined);
        }
    }, [updateConnectionReplaceHover]);

    const closeConnectionCreateMenu = useCallback(() => {
        pendingConnectionCreateRef.current = null;
        setPendingConnectionCreate(null);
    }, []);

    const cancelPendingConnectionCreate = useCallback(() => {
        closeConnectionCreateMenu();
        setConnecting(null);
        clearBatchConnection();
    }, [clearBatchConnection, closeConnectionCreateMenu, setConnecting]);

    const previewBatchConnection = useCallback((sourceNodeIds: string[], targetNodeId: string | null, targetHandleId: string | undefined, targetAnchorRatio: number | undefined, mouseWorld: Position) => {
        const plan = targetNodeId
            ? planBatchConnections({ sourceNodeIds, targetNodeId, targetHandleId, targetAnchorRatio, nodes: nodesRef.current, connections: connectionsRef.current, config })
            : null;
        const eligibleSourceCount = sourceNodeIds.filter((id) => {
            const node = nodesRef.current.find((item) => item.id === id);
            return Boolean(node && !batchSourceRestriction(node));
        }).length;
        const status = !targetNodeId || !plan ? "idle" : plan.connections.length === eligibleSourceCount ? "valid" : plan.connections.length ? "partial" : "invalid";
        updateBatchConnectionPreview({ sourceNodeIds, targetNodeId, targetHandleId, targetAnchorRatio, mouseWorld, status });
        return plan;
    }, [config, connectionsRef, nodesRef, updateBatchConnectionPreview]);

    const commitBatchConnection = useCallback((sourceNodeIds: string[], targetNodeId: string, targetHandleId?: string, targetAnchorRatio?: number) => {
        const plan = planBatchConnections({ sourceNodeIds, targetNodeId, targetHandleId, targetAnchorRatio, nodes: nodesRef.current, connections: connectionsRef.current, config });
        if (!plan.connections.length) {
            const reason = plan.skipped[0]?.reason || "没有可建立的连接";
            message.warning(reason);
            return plan;
        }
        setNodes((currentNodes) => plan.connections.reduce((current, connection) => attachNodeToStoryboardRow(current, connection), currentNodes));
        setConnections((currentConnections) => [...currentConnections, ...plan.connections]);
        setContextMenu(null);
        const skippedCount = plan.skipped.length;
        const duplicateCount = plan.duplicates.length;
        const suffix = skippedCount || duplicateCount ? `，跳过 ${skippedCount + duplicateCount} 个` : "";
        if (skippedCount) message.warning(`已连接 ${plan.connected.length} 个节点${suffix}：${plan.skipped[0].reason}`);
        else message.success(`已连接 ${plan.connected.length} 个节点${suffix}`);
        return plan;
    }, [config, connectionsRef, message, nodesRef, setConnections, setContextMenu, setNodes]);

    const connectNodes = useCallback((current: ConnectionHandle, targetNodeId: string, targetHandleId?: string, targetAnchorRatio?: number) => {
        if (current.nodeId === targetNodeId) return;
        const connection = normalizeConnection(current.nodeId, targetNodeId, nodesRef.current, current.handleType);
        if (!connection) {
            message.warning("配置节点之间不能连接");
            return;
        }
        const { fromNodeId, toNodeId } = connection;
        const fromHandleId = fromNodeId === current.nodeId ? current.handleId : targetHandleId;
        const toHandleId = toNodeId === current.nodeId ? current.handleId : targetHandleId;
        const fromAnchorRatio = fromNodeId === current.nodeId ? current.anchorRatio : targetAnchorRatio;
        const toAnchorRatio = toNodeId === current.nodeId ? current.anchorRatio : targetAnchorRatio;
        const policyError = canvasConnectionError(config, nodesRef.current, connectionsRef.current, { fromNodeId, toNodeId });
        if (policyError) {
            message.warning(policyError);
            return;
        }
        const exists = connectionsRef.current.find((item) => item.fromNodeId === fromNodeId && item.toNodeId === toNodeId && item.fromHandleId === fromHandleId && item.toHandleId === toHandleId);
        if (exists) {
            setConnections((currentConnections) => currentConnections.map((item) => item.id === exists.id ? { ...item, fromAnchorRatio, toAnchorRatio } : item));
        } else {
            setConnections((currentConnections) => [...currentConnections, { id: `conn-${Date.now()}`, fromNodeId, toNodeId, fromHandleId, toHandleId, fromAnchorRatio, toAnchorRatio }]);
            setNodes((currentNodes) => attachNodeToStoryboardRow(currentNodes, { fromNodeId, toNodeId, fromHandleId, toHandleId }));
        }
        setContextMenu(null);
    }, [config, connectionsRef, message, nodesRef, setConnections, setContextMenu, setNodes]);

    const createConnectedNode = useCallback(async (type: CanvasNodeType.Image | CanvasNodeType.Text | CanvasNodeType.Script | CanvasNodeType.BatchTable | CanvasNodeType.Video | CanvasNodeType.Audio | CanvasNodeType.Drawing | CanvasNodeType.Config | CanvasNodeType.MediaConversion, pending: PendingConnectionCreate, workflowProvider?: "runninghub") => {
        const nodeType = type;
        const batchSourceNodeIds = pending.batchSourceNodeIds?.length ? Array.from(new Set(pending.batchSourceNodeIds)) : [];
        const batchSourceNodes = batchSourceNodeIds
            .map((nodeId) => nodesRef.current.find((node) => node.id === nodeId))
            .filter((node): node is CanvasNodeData => Boolean(node));
        const storyboardRow = batchSourceNodeIds.length ? undefined : nodeType === CanvasNodeType.Video ? storyboardRowFromHandle(nodesRef.current, pending.connection.nodeId, pending.connection.handleId) : undefined;
        const videoPrompt = storyboardRow ? (storyboardRow.videoMotionPrompt || storyboardRow.plotDescription).trim() : "";
        const sourceNode = pending.connection.handleType === "source" ? nodesRef.current.find((node) => node.id === pending.connection.nodeId) : undefined;
        const batchScriptPrompt = batchSourceNodes
            .filter((node) => node.type === CanvasNodeType.Text)
            .map((node) => (node.metadata?.content || node.metadata?.prompt || "").trim())
            .filter(Boolean)
            .join("\n\n");
        const scriptPrompt = nodeType === CanvasNodeType.Script
            ? batchSourceNodeIds.length ? batchScriptPrompt : sourceNode?.type === CanvasNodeType.Text ? (sourceNode.metadata?.content || sourceNode.metadata?.prompt || "").trim() : ""
            : "";
        const selectedWorkflowProvider = nodeType === CanvasNodeType.Config
            ? workflowProvider || (workflowProviderPluginEnabled(runtimeStatuses, "runninghub") ? "runninghub" : undefined)
            : undefined;
        if (selectedWorkflowProvider && !workflowProviderPluginEnabled(runtimeStatuses, selectedWorkflowProvider)) {
            message.error("RunningHub 工作流插件未启用");
            closeConnectionCreateMenu();
            setConnecting(null);
            return;
        }
        const runningHubWorkflow = selectedWorkflowProvider === "runninghub" ? selectRunningHubWorkflow(config) : undefined;
        const workflowCapability = normalizeRunningHubCapability(runningHubWorkflow?.capability, normalizeRunningHubCapability(config.runningHub.capability));
        const metadata: CanvasNodeMetadata | undefined = nodeType === CanvasNodeType.Config
            ? {
                generationMode: selectedWorkflowProvider ? workflowCapability === "video" ? "video" as const : workflowCapability === "audio" ? "audio" as const : "image" as const : "image" as const,
                workflowProvider: selectedWorkflowProvider || "model",
                status: NODE_STATUS_IDLE,
                ...(selectedWorkflowProvider === "runninghub" ? {
                    workflowTitle: "RunningHub 工作流",
                    ...(runningHubWorkflow ? { runningHubWorkflowId: runningHubWorkflow.workflowId, runningHubWorkflowKind: runningHubWorkflow.kind === "app" ? "app" as const : "workflow" as const } : {}),
                } : {})
              }
            : nodeType === CanvasNodeType.Drawing
            ? { drawingEngine: defaultDrawingEngine }
            : nodeType === CanvasNodeType.Script && scriptPrompt
              ? { prompt: scriptPrompt, composerContent: scriptPrompt }
            : nodeType === CanvasNodeType.Video && storyboardRow
              ? { prompt: videoPrompt, composerContent: videoPrompt, ...storyboardPromptTemplateMetadata(storyboardRow, "video"), generationMode: "video" as const, videoEditOperation: "text_to_video" as const, workflowKind: "shot" as const, workflowTitle: `镜头 ${storyboardRow.shotNumber} 视频`, shotIndex: storyboardRow.shotNumber, seconds: String(storyboardRow.durationSeconds), status: NODE_STATUS_IDLE }
              : undefined;
        const spec = getNodeSpec(nodeType);
        // 拖拽释放落在松手处；点击快捷连接点没有落点，才锚到源节点同侧。
        const position = resolveConnectedNodeCreatePosition(nodesRef.current, pending, spec);
        const newNode = createCanvasNode(nodeType, position, metadata);
        if (nodeType === CanvasNodeType.Config && selectedWorkflowProvider) newNode.title = "RunningHub 工作流";
        if (storyboardRow) newNode.title = `镜头 ${storyboardRow.shotNumber} · 视频`;
        if (batchSourceNodeIds.length && nodeType === CanvasNodeType.Drawing) {
            message.error("批量连接暂不支持创建绘图，请先连接到普通节点");
            closeConnectionCreateMenu();
            setConnecting(null);
            return;
        }
        const batchPlan = batchSourceNodeIds.length
            ? planBatchConnections({ sourceNodeIds: batchSourceNodeIds, targetNodeId: newNode.id, nodes: [...nodesRef.current, newNode], connections: connectionsRef.current, config })
            : null;
        if (batchPlan) {
            if (!batchPlan.connections.length) {
                const detail = batchPlan.skipped.slice(0, 3).map((item) => item.reason).join("；");
                message.warning(detail ? `没有可建立的连接：${detail}` : "没有可建立的连接");
                closeConnectionCreateMenu();
                setConnecting(null);
                return;
            }
            const nextConnections = [...connectionsRef.current, ...batchPlan.connections];
            const nextNodes = batchPlan.connections.reduce((currentNodes, connection) => attachNodeToStoryboardRow(currentNodes, connection), [...nodesRef.current, newNode]);
            onConnectedNodeCreated?.(newNode, pending.connection.nodeId);
            nodesRef.current = nextNodes;
            connectionsRef.current = nextConnections;
            setNodes(nextNodes);
            setConnections(nextConnections);
            setSelectedNodeIds(new Set([newNode.id]));
            setSelectedConnectionId(null);
            setDialogNodeId(nodeType !== CanvasNodeType.Text && nodeType !== CanvasNodeType.BatchTable && nodeType !== CanvasNodeType.MediaConversion && canOpenCanvasNodePromptPanel(newNode) ? newNode.id : null);
            const skippedCount = batchPlan.skipped.length;
            const duplicateCount = batchPlan.duplicates.length;
            const suffix = skippedCount || duplicateCount ? `，跳过 ${skippedCount + duplicateCount} 个` : "";
            if (skippedCount) message.warning(`已创建并连接 ${batchPlan.connections.length} 个源节点${suffix}：${batchPlan.skipped[0].reason}`);
            else message.success(`已创建节点并连接 ${batchPlan.connections.length} 个源节点${suffix}`);
            closeConnectionCreateMenu();
            setConnecting(null);
            return;
        }
        const connection = normalizeConnection(pending.connection.nodeId, newNode.id, [...nodesRef.current, newNode], pending.connection.handleType);
        if (!connection) {
            message.warning("当前节点不能建立这条连线");
            closeConnectionCreateMenu();
            setConnecting(null);
            return;
        }
        const policyError = canvasConnectionError(config, [...nodesRef.current, newNode], connectionsRef.current, connection);
        if (policyError) {
            message.warning(policyError);
            closeConnectionCreateMenu();
            setConnecting(null);
            return;
        }
        if (nodeType === CanvasNodeType.Drawing) {
            const drawingSourceNode = nodesRef.current.find((node) => node.id === pending.connection.nodeId);
            const sourceUrl = drawingSourceNode?.type === CanvasNodeType.Image ? drawingSourceNode.metadata?.content : "";
            if (pending.connection.handleType !== "source" || !drawingSourceNode || !sourceUrl || !newNode.metadata?.drawingId) {
                message.error("只有已有图片内容的输出连线可以创建绘图");
                closeConnectionCreateMenu();
                setConnecting(null);
                return;
            }
            closeConnectionCreateMenu();
            setConnecting(null);
            try {
                const saved = await createCanvasDrawingFromImage(projectId, newNode.metadata.drawingId, defaultDrawingEngine, {
                    url: sourceUrl,
                    storageKey: drawingSourceNode.metadata?.storageKey,
                    name: drawingSourceNode.title || "来源图片",
                    mimeType: drawingSourceNode.metadata?.mimeType,
                });
                newNode.title = `${drawingSourceNode.title || "图片"} · 绘图`;
                newNode.metadata = {
                    ...newNode.metadata,
                    drawingEngine: saved.engine,
                    drawingRevision: saved.revision,
                    drawingUpdatedAt: saved.updatedAt,
                    drawingShapeCount: saved.shapeCount,
                    drawingPageCount: saved.pageCount,
                };
            } catch (error) {
                message.error(error instanceof Error ? `创建绘图失败：${error.message}` : "创建绘图失败");
                return;
            }
        }
        const fromHandleId = connection.fromNodeId === pending.connection.nodeId ? pending.connection.handleId : undefined;
        const toHandleId = connection.toNodeId === pending.connection.nodeId ? pending.connection.handleId : undefined;
        const fromAnchorRatio = connection.fromNodeId === pending.connection.nodeId ? pending.connection.anchorRatio : 0.5;
        const toAnchorRatio = connection.toNodeId === pending.connection.nodeId ? pending.connection.anchorRatio : 0.5;
        const connected = { ...connection, fromHandleId, toHandleId, fromAnchorRatio, toAnchorRatio };
        onConnectedNodeCreated?.(newNode, pending.connection.nodeId);
        setNodes((currentNodes) => attachNodeToStoryboardRow([...currentNodes, newNode], connected));
        setConnections((currentConnections) => [...currentConnections, { id: nanoid(), ...connected }]);
        setSelectedNodeIds(new Set([newNode.id]));
        setSelectedConnectionId(null);
        if (nodeType === CanvasNodeType.Drawing) setDrawingNodeId(newNode.id);
        setDialogNodeId(nodeType !== CanvasNodeType.Text && nodeType !== CanvasNodeType.BatchTable && nodeType !== CanvasNodeType.MediaConversion && canOpenCanvasNodePromptPanel(newNode) ? newNode.id : null);
        closeConnectionCreateMenu();
        setConnecting(null);
    }, [closeConnectionCreateMenu, config, connectionsRef, defaultDrawingEngine, message, nodesRef, onConnectedNodeCreated, projectId, runtimeStatuses, setConnecting, setConnections, setDialogNodeId, setDrawingNodeId, setNodes, setSelectedConnectionId, setSelectedNodeIds]);

    /**
     * 拖出连接后选「导演台」：先让 useCanvasDirector 建好节点，再把这条线接上。
     *
     * 导演台不是画布节点类型那样可以一次 createCanvasNode 造出来的——它还要同时落一份
     * 场景数据，所以节点由调用方给，这里只负责连线和收尾。缺少这一步时，导演台只能靠
     * 右键菜单单独新建，永远进不了任何一条工作流（"孤岛"）。
     */
    const createConnectedDirector = useCallback((pending: PendingConnectionCreate) => {
        const spec = getNodeSpec(CanvasNodeType.Director);
        const position = resolveConnectedNodeCreatePosition(nodesRef.current, pending, spec);
        const directorNode = createDirectorNode?.(position);
        if (!directorNode) {
            message.warning("导演台暂时不可用，稍后再试");
            closeConnectionCreateMenu();
            setConnecting(null);
            return;
        }
        const connection = normalizeConnection(pending.connection.nodeId, directorNode.id, nodesRef.current, pending.connection.handleType);
        if (!connection) {
            message.warning("当前节点不能建立这条连线");
            closeConnectionCreateMenu();
            setConnecting(null);
            return;
        }
        const policyError = canvasConnectionError(config, nodesRef.current, connectionsRef.current, connection);
        if (policyError) {
            message.warning(policyError);
            closeConnectionCreateMenu();
            setConnecting(null);
            return;
        }
        const attached = {
            id: nanoid(),
            ...connection,
            fromHandleId: connection.fromNodeId === pending.connection.nodeId ? pending.connection.handleId : undefined,
            toHandleId: connection.toNodeId === pending.connection.nodeId ? pending.connection.handleId : undefined,
            fromAnchorRatio: connection.fromNodeId === pending.connection.nodeId ? pending.connection.anchorRatio : 0.5,
            toAnchorRatio: connection.toNodeId === pending.connection.nodeId ? pending.connection.anchorRatio : 0.5,
        };
        const nextConnections = [...connectionsRef.current, attached];
        connectionsRef.current = nextConnections;
        setConnections(nextConnections);
        onConnectedNodeCreated?.(directorNode, pending.connection.nodeId);
        closeConnectionCreateMenu();
        setConnecting(null);
    }, [closeConnectionCreateMenu, config, connectionsRef, createDirectorNode, message, nodesRef, onConnectedNodeCreated, setConnecting, setConnections]);

    const getConnectionCreateDisabledReason = useCallback((type: CanvasNodeType.Image | CanvasNodeType.Text | CanvasNodeType.Script | CanvasNodeType.BatchTable | CanvasNodeType.Video | CanvasNodeType.Audio | CanvasNodeType.Drawing | CanvasNodeType.Config | CanvasNodeType.MediaConversion, pending: PendingConnectionCreate, workflowProvider?: "runninghub") => {
        const nodeType = type;
        if (nodeType === CanvasNodeType.Config) {
            if (workflowProvider && !workflowProviderPluginEnabled(runtimeStatuses, workflowProvider)) return "RunningHub 工作流插件未启用";
        }
        if (pending.batchSourceNodeIds?.length) {
            if (nodeType === CanvasNodeType.Drawing || nodeType === CanvasNodeType.Config) return "批量连接暂不支持此节点类型";
            const pendingNode: CanvasNodeData = { id: "__pending-connection-node__", type: nodeType, title: "", position: pending.position, width: getNodeSpec(nodeType).width, height: getNodeSpec(nodeType).height };
            const plan = planBatchConnections({ sourceNodeIds: pending.batchSourceNodeIds, targetNodeId: pendingNode.id, nodes: [...nodesRef.current, pendingNode], connections: connectionsRef.current, config });
            return plan.connections.length ? "" : plan.skipped[0]?.reason || "当前选中的节点不能连接到此类型";
        }
        const spec = getNodeSpec(nodeType);
        const pendingNode: CanvasNodeData = { id: "__pending-connection-node__", type: nodeType, title: "", position: pending.position, width: spec.width, height: spec.height };
        const pendingNodes = [...nodesRef.current, pendingNode];
        const connection = normalizeConnection(pending.connection.nodeId, pendingNode.id, pendingNodes, pending.connection.handleType);
        if (!connection) return "当前节点类型不能这样连接";
        return canvasConnectionError(config, pendingNodes, connectionsRef.current, connection);
    }, [config, connectionsRef, nodesRef, runtimeStatuses]);

    const getConnectionDropTarget = useCallback((clientX: number, clientY: number, current: ConnectionHandle): ConnectionDropTarget => {
        const world = screenToCanvas(clientX, clientY);
        const scale = Math.max(viewportRef.current.k, 0.05);
        const handleRadius = CONNECTION_SNAP_RADIUS / scale;
        let isNearNode = false;
        let bestNodeId: string | null = null;
        let bestHandleId: string | undefined;
        let bestAnchorRatio: number | undefined;
        let bestPriority = Number.POSITIVE_INFINITY;

        [...nodesRef.current]
            .filter((node) => !isHiddenBatchChild(node, nodesRef.current) && !isNodeHiddenByCollapsedFrame(node, nodesRef.current) && !isFrameNode(node))
            .reverse()
            .forEach((node) => {
                const scrollTop = scriptScrollTopById[node.id] || 0;
                const targetHandleId = node.type === CanvasNodeType.Script ? storyboardHandleAtY(node, world.y, scrollTop) : node.type === CanvasNodeType.BatchTable ? batchReferenceHandleAtY(node, world.y, handleRadius) : undefined;
                if ((node.type === CanvasNodeType.Script || node.type === CanvasNodeType.BatchTable) && !targetHandleId) return;
                // Ordinary nodes expose one centered input/output port. Only
                // storyboard rows have a meaningful vertical target position.
                const targetAnchorRatio = undefined;
                const anchor = getConnectionTargetAnchor(node, current, targetHandleId, scrollTop);
                const dx = world.x - anchor.x;
                const dy = world.y - anchor.y;
                // Do not treat the node body or a rectangular padding band as a
                // connection target. LibTV snaps only inside the circular
                // quick-add area centred on the corresponding side handle.
                const hitsSnapZone = dx * dx + dy * dy <= handleRadius * handleRadius;
                if (!hitsSnapZone) return;
                isNearNode = true;
                const normalized = node.id === current.nodeId ? null : normalizeConnection(current.nodeId, node.id, nodesRef.current, current.handleType);
                if (!normalized || canvasConnectionError(config, nodesRef.current, connectionsRef.current, normalized)) return;
                if (1 < bestPriority) {
                    bestNodeId = node.id;
                    bestHandleId = targetHandleId;
                    bestAnchorRatio = targetAnchorRatio;
                    bestPriority = 1;
                }
            });
        return { nodeId: bestNodeId, handleId: bestHandleId, anchorRatio: bestAnchorRatio, isNearNode };
    }, [config, connectionsRef, nodesRef, screenToCanvas, scriptScrollTopById, viewportRef]);

    const getBatchConnectionDropTarget = useCallback((clientX: number, clientY: number, sourceNodeIds: string[]): BatchConnectionDropTarget => {
        const source = sourceNodeIds
            .map((id) => nodesRef.current.find((node) => node.id === id))
            .find((node): node is CanvasNodeData => Boolean(node && !batchSourceRestriction(node)));
        if (!source) return { nodeId: null, isNearNode: false };

        const world = screenToCanvas(clientX, clientY);
        const scale = Math.max(viewportRef.current.k, 0.05);
        const handleRadius = CONNECTION_SNAP_RADIUS / scale;
        let isNearNode = false;
        let bestNodeId: string | null = null;
        let bestHandleId: string | undefined;
        let bestAnchorRatio: number | undefined;
        let bestPriority = Number.POSITIVE_INFINITY;
        const current: ConnectionHandle = { nodeId: source.id, handleType: "source" };

        [...nodesRef.current]
            .filter((node) => !isHiddenBatchChild(node, nodesRef.current) && !isNodeHiddenByCollapsedFrame(node, nodesRef.current) && !isFrameNode(node))
            .reverse()
            .forEach((node) => {
                const scrollTop = scriptScrollTopById[node.id] || 0;
                const targetHandleId = node.type === CanvasNodeType.Script ? storyboardHandleAtY(node, world.y, scrollTop) : node.type === CanvasNodeType.BatchTable ? batchReferenceHandleAtY(node, world.y, handleRadius) : undefined;
                if ((node.type === CanvasNodeType.Script || node.type === CanvasNodeType.BatchTable) && !targetHandleId) return;
                const targetAnchorRatio = undefined;
                const anchor = getConnectionTargetAnchor(node, current, targetHandleId, scrollTop);
                const dx = world.x - anchor.x;
                const dy = world.y - anchor.y;
                const hitsSnapZone = dx * dx + dy * dy <= handleRadius * handleRadius;
                if (!hitsSnapZone) return;
                isNearNode = true;
                if (!hasBatchConnectionCandidate(sourceNodeIds, node.id, nodesRef.current)) return;
                if (1 < bestPriority) {
                    bestNodeId = node.id;
                    bestHandleId = targetHandleId;
                    bestAnchorRatio = targetAnchorRatio;
                    bestPriority = 1;
                }
            });
        return { nodeId: bestNodeId, handleId: bestHandleId, anchorRatio: bestAnchorRatio, isNearNode };
    }, [nodesRef, screenToCanvas, scriptScrollTopById, viewportRef]);

    const startBatchConnection = useCallback((event: ReactPointerEvent, sourceNodeIds: string[]) => {
        const eligible = sourceNodeIds.filter((id) => {
            const node = nodesRef.current.find((item) => item.id === id);
            return Boolean(node && !batchSourceRestriction(node));
        });
        if (!eligible.length) {
            message.warning("当前选区没有可作为连接源的节点");
            return;
        }
        event.preventDefault();
        event.stopPropagation();
        batchConnectionPointerIdRef.current = event.pointerId;
        batchConnectionPointerStartRef.current = { x: event.clientX, y: event.clientY };
        setSelectedConnectionId(null);
        const mouseWorld = screenToCanvas(event.clientX, event.clientY);
        previewBatchConnection(eligible, null, undefined, undefined, mouseWorld);
    }, [message, nodesRef, previewBatchConnection, screenToCanvas, setSelectedConnectionId]);

    const beginBatchConnectionMode = useCallback((sourceNodeIds: string[]) => {
        const eligible = sourceNodeIds.filter((id) => {
            const node = nodesRef.current.find((item) => item.id === id);
            return Boolean(node && !batchSourceRestriction(node));
        });
        const source = eligible.map((id) => nodesRef.current.find((node) => node.id === id)).find((node): node is CanvasNodeData => Boolean(node));
        if (!source) {
            message.warning("当前选区没有可作为连接源的节点");
            return;
        }
        previewBatchConnection(eligible, null, undefined, undefined, { x: source.position.x + source.width, y: source.position.y + source.height / 2 });
        setSelectedConnectionId(null);
    }, [message, nodesRef, previewBatchConnection, setSelectedConnectionId]);

    const finishBatchConnection = useCallback((clientX: number, clientY: number) => {
        const batch = batchConnectionPreviewRef.current;
        if (!batch) return false;
        const target = getBatchConnectionDropTarget(clientX, clientY, batch.sourceNodeIds);
        if (!target.nodeId) return false;
        commitBatchConnection(batch.sourceNodeIds, target.nodeId, target.handleId, target.anchorRatio);
        clearBatchConnection();
        return true;
    }, [clearBatchConnection, commitBatchConnection, getBatchConnectionDropTarget]);

    const openBatchConnectionCreateMenu = useCallback((clientX: number, clientY: number) => {
        const batch = batchConnectionPreviewRef.current;
        if (!batch) return false;
        const position = screenToCanvas(clientX, clientY);
        const request = buildBatchConnectionCreateRequest(batch.sourceNodeIds, nodesRef.current, position);
        if (!request) {
            clearBatchConnection();
            message.warning("当前选区没有可作为连接源的节点");
            return false;
        }
        const pending: PendingConnectionCreate = request;
        pendingConnectionCreateRef.current = pending;
        setPendingConnectionCreate(pending);
        setMouseWorld(position);
        clearBatchConnection();
        return true;
    }, [clearBatchConnection, message, nodesRef, screenToCanvas]);

    const handleBatchConnectionTargetClick = useCallback((event: ReactPointerEvent | ReactMouseEvent) => {
        if (!batchConnectionPreviewRef.current) return false;
        const completed = finishBatchConnection(event.clientX, event.clientY);
        if (!completed) message.warning("请点击目标节点的输入端");
        return true;
    }, [finishBatchConnection, message]);

    const finishConnection = useCallback((clientX: number, clientY: number) => {
        updateConnectionReplaceHover(null);
        if (pendingConnectionCreateRef.current) return;
        const currentConnection = connectingParamsRef.current;
        if (!currentConnection) return;

        // 1. 如果是从输出端（source）拖出的连线，检查是否拖到了参考图卡片或参考区上进行替换
        if (currentConnection.handleType === "source" && typeof document !== "undefined") {
            const hitElement = document.elementFromPoint(clientX, clientY);
            if (hitElement) {
                // A) 直接释放在提示词面板的参考图卡片上
                const refChip = hitElement.closest<HTMLElement>("[data-reference-chip]");
                if (refChip) {
                    const targetNodeId = refChip.getAttribute("data-target-node-id");
                    const oldNodeId = refChip.getAttribute("data-reference-node-id");
                    const oldLabel = refChip.getAttribute("data-reference-label") || "";
                    const oldRefId = refChip.getAttribute("data-reference-id") || "";
                    const oldTitle = refChip.getAttribute("data-reference-title") || oldLabel;
                    if (targetNodeId && oldNodeId && targetNodeId !== currentConnection.nodeId && onReplaceReference) {
                        if (oldNodeId === currentConnection.nodeId) {
                            setConnecting(null);
                            return;
                        }
                        onReplaceReference(targetNodeId, { id: oldRefId, nodeId: oldNodeId, label: oldLabel, title: oldTitle }, currentConnection.nodeId);
                        setConnecting(null);
                        return;
                    }
                }

                // B) 释放在提示词参考架容器内（自动就近匹配最近的参考卡片）
                const refShelf = hitElement.closest<HTMLElement>(".canvas-node-composer-references");
                if (refShelf) {
                    const chips = Array.from(refShelf.querySelectorAll<HTMLElement>("[data-reference-chip]"));
                    let closestChip: HTMLElement | null = null;
                    let minDistance = Number.POSITIVE_INFINITY;
                    chips.forEach((chip) => {
                        const rect = chip.getBoundingClientRect();
                        const centerX = rect.left + rect.width / 2;
                        const dist = Math.abs(clientX - centerX);
                        if (dist < minDistance) {
                            minDistance = dist;
                            closestChip = chip;
                        }
                    });
                    if (closestChip) {
                        const targetNodeId = (closestChip as HTMLElement).getAttribute("data-target-node-id");
                        const oldNodeId = (closestChip as HTMLElement).getAttribute("data-reference-node-id");
                        const oldLabel = (closestChip as HTMLElement).getAttribute("data-reference-label") || "";
                        const oldRefId = (closestChip as HTMLElement).getAttribute("data-reference-id") || "";
                        const oldTitle = (closestChip as HTMLElement).getAttribute("data-reference-title") || oldLabel;
                        if (targetNodeId && oldNodeId && targetNodeId !== currentConnection.nodeId && onReplaceReference) {
                            if (oldNodeId === currentConnection.nodeId) {
                                setConnecting(null);
                                return;
                            }
                            onReplaceReference(targetNodeId, { id: oldRefId, nodeId: oldNodeId, label: oldLabel, title: oldTitle }, currentConnection.nodeId);
                            setConnecting(null);
                            return;
                        }
                    }
                }
            }
        }

        const dropTarget = getConnectionDropTarget(clientX, clientY, currentConnection);
        if (dropTarget.nodeId) {
            connectNodes(currentConnection, dropTarget.nodeId, dropTarget.handleId, dropTarget.anchorRatio);
            setConnecting(null);
        } else if (dropTarget.isNearNode) {
            setConnecting(null);
        } else {
            const position = screenToCanvas(clientX, clientY);
            setMouseWorld(position);
            const pending = { connection: currentConnection, position };
            pendingConnectionCreateRef.current = pending;
            setPendingConnectionCreate(pending);
        }
    }, [connectNodes, getConnectionDropTarget, onReplaceReference, screenToCanvas, setConnecting, updateConnectionReplaceHover]);

    const handleConnectStart = useCallback((event: ReactPointerEvent, nodeId: string, handleType: "source" | "target", handleId?: string, anchorRatio?: number) => {
        event.preventDefault();
        event.stopPropagation();
        // A new pin interaction always starts a fresh session. Without this
        // reset, an old quick-create menu can coexist with the new draft line;
        // pointerup then sees the stale pending menu and silently aborts.
        if (pendingConnectionCreateRef.current) closeConnectionCreateMenu();
        if (batchConnectionPreviewRef.current && handleType === "target") {
            commitBatchConnection(batchConnectionPreviewRef.current.sourceNodeIds, nodeId, handleId, anchorRatio);
            clearBatchConnection();
            return;
        }
        // A batch table exposes real target pins inside its content. When a normal
        // image output is dragged onto one, commit that active connection instead
        // of starting a new connection session from the target pin.
        const activeConnection = connectingParamsRef.current;
        if (activeConnection && handleType === "target" && activeConnection.nodeId !== nodeId) {
            connectNodes(activeConnection, nodeId, handleId, anchorRatio);
            setConnecting(null);
            return;
        }
        if (activeConnection) setConnecting(null);
        if (batchConnectionPreviewRef.current) clearBatchConnection();
        connectingPointerIdRef.current = event.pointerId;
        connectingPointerStartRef.current = { x: event.clientX, y: event.clientY };
        // 新一轮拖拽的第一帧就要探测，否则会沿用上一次拖拽的时间戳而被节流掉。
        replaceHoverProbeAtRef.current = 0;
        setMouseWorld(screenToCanvas(event.clientX, event.clientY));
        setConnecting({ nodeId, handleType, handleId, anchorRatio });
        setConnectionTargetNodeId(null);
        setConnectionTargetAnchorRatio(undefined);
        setSelectedConnectionId(null);
    }, [clearBatchConnection, closeConnectionCreateMenu, commitBatchConnection, connectNodes, screenToCanvas, setConnecting, setSelectedConnectionId]);

    const handleConnectDrop = useCallback((event: ReactPointerEvent, nodeId: string, handleId?: string) => {
        event.preventDefault();
        event.stopPropagation();
        if (batchConnectionPreviewRef.current) {
            commitBatchConnection(batchConnectionPreviewRef.current.sourceNodeIds, nodeId, handleId);
            clearBatchConnection();
            return;
        }
        const current = connectingParamsRef.current;
        if (!current || current.nodeId === nodeId) return;
        connectNodes(current, nodeId, handleId);
        setConnecting(null);
    }, [clearBatchConnection, commitBatchConnection, connectNodes, setConnecting]);

    useEffect(() => {
        const cancelPendingPointerMove = () => {
            latestPointerMoveRef.current = null;
            if (pointerMoveFrameRef.current !== null) window.cancelAnimationFrame(pointerMoveFrameRef.current);
            pointerMoveFrameRef.current = null;
        };
        const flushPointerMove = () => {
            pointerMoveFrameRef.current = null;
            const event = latestPointerMoveRef.current;
            latestPointerMoveRef.current = null;
            if (!event) return;
            const batch = batchConnectionPreviewRef.current;
            if (batch && (batchConnectionPointerIdRef.current === null || batchConnectionPointerIdRef.current === event.pointerId)) {
                const target = getBatchConnectionDropTarget(event.clientX, event.clientY, batch.sourceNodeIds);
                const mouseWorld = screenToCanvas(event.clientX, event.clientY);
                previewBatchConnection(batch.sourceNodeIds, target.nodeId, target.handleId, target.anchorRatio, mouseWorld);
                return;
            }
            const current = connectingParamsRef.current;
            if (!current || connectingPointerIdRef.current !== event.pointerId || pendingConnectionCreateRef.current) return;
            if (current.handleType === "source" && typeof document !== "undefined") {
                // elementFromPoint 会强制同步样式计算与布局，而连线拖拽期间布局本来就是脏的，
                // 逐帧探测实测每次要几毫秒。它只驱动「悬停在参考芯片上」这一个瞬态提示，
                // 所以降到 ~25Hz，并在页面上根本没有参考芯片时直接跳过。
                const now = performance.now();
                if (!document.querySelector("[data-reference-chip]")) {
                    updateConnectionReplaceHover(null);
                } else if (shouldProbeConnectionReplaceHover(replaceHoverProbeAtRef.current, now)) {
                    replaceHoverProbeAtRef.current = now;
                    const el = document.elementFromPoint(event.clientX, event.clientY);
                    let chip = el?.closest<HTMLElement>("[data-reference-chip]");
                    if (!chip) {
                        const shelf = el?.closest<HTMLElement>(".canvas-node-composer-references");
                        if (shelf) {
                            const chips = Array.from(shelf.querySelectorAll<HTMLElement>("[data-reference-chip]"));
                            let closestChip: HTMLElement | null = null;
                            let minDistance = Number.POSITIVE_INFINITY;
                            chips.forEach((c) => {
                                const rect = c.getBoundingClientRect();
                                const dist = Math.abs(event.clientX - (rect.left + rect.width / 2));
                                if (dist < minDistance) {
                                    minDistance = dist;
                                    closestChip = c;
                                }
                            });
                            chip = closestChip;
                        }
                    }
                    updateConnectionReplaceHover(chip || null, event.clientX, event.clientY);
                }
            } else {
                updateConnectionReplaceHover(null);
            }
            const dropTarget = getConnectionDropTarget(event.clientX, event.clientY, current);
            const point = screenToCanvas(event.clientX, event.clientY);
            setConnectionApproach((previous) => latchCanvasConnectionApproach(previous, dropTarget.nodeId, point));
            setConnectionTargetNodeId(dropTarget.nodeId);
            setConnectionTargetAnchorRatio(dropTarget.anchorRatio);
            // 逐帧只重画 Leafer 覆盖层，不进 React。上面两个 setState 在值没变时 React 会
            // 自行 bail out，而鼠标位置每帧都是新对象——走 state 就等于每帧把整个画布页面
            // （含全部节点）重渲染一次，实测一次拖拽因此掉 4 帧。
            connectionDraftRef.current?.(point);
        };
        const handlePointerMove = (event: PointerEvent) => {
            // Pointer events can arrive faster than the canvas can paint. Keep
            // only the latest position and update the preview once per frame,
            // which prevents redundant React/Leafer work and visible jitter.
            latestPointerMoveRef.current = event;
            if (pointerMoveFrameRef.current === null) pointerMoveFrameRef.current = window.requestAnimationFrame(flushPointerMove);
        };
        const handlePointerUp = (event: PointerEvent) => {
            latestPointerMoveRef.current = null;
            if (pointerMoveFrameRef.current !== null) {
                window.cancelAnimationFrame(pointerMoveFrameRef.current);
                pointerMoveFrameRef.current = null;
            }
            if (batchConnectionPointerIdRef.current === event.pointerId) {
                const start = batchConnectionPointerStartRef.current;
                const batch = batchConnectionPreviewRef.current;
                if (batch && start && Math.hypot(event.clientX - start.x, event.clientY - start.y) <= 5) {
                    const target = getBatchConnectionDropTarget(event.clientX, event.clientY, batch.sourceNodeIds);
                    if (target.nodeId) {
                        commitBatchConnection(batch.sourceNodeIds, target.nodeId, target.handleId, target.anchorRatio);
                        clearBatchConnection();
                    } else if (!target.isNearNode) openBatchConnectionCreateMenu(event.clientX, event.clientY);
                    else clearBatchConnection();
                    return;
                }
                const completed = finishBatchConnection(event.clientX, event.clientY);
                if (!completed) {
                    const target = getBatchConnectionDropTarget(event.clientX, event.clientY, batch?.sourceNodeIds || []);
                    if (!target.isNearNode) openBatchConnectionCreateMenu(event.clientX, event.clientY);
                    else clearBatchConnection();
                }
                return;
            }
            if (connectingPointerIdRef.current !== event.pointerId) return;
            const start = connectingPointerStartRef.current;
            if (start && Math.hypot(event.clientX - start.x, event.clientY - start.y) <= 5) {
                const current = connectingParamsRef.current;
                if (current) {
                    const pending = { connection: current, position: screenToCanvas(event.clientX, event.clientY), quick: true };
                    pendingConnectionCreateRef.current = pending;
                    setPendingConnectionCreate(pending);
                    setConnecting(null);
                    return;
                }
            }
            finishConnection(event.clientX, event.clientY);
        };
        const handlePointerCancel = (event: PointerEvent) => {
            cancelPendingPointerMove();
            updateConnectionReplaceHover(null);
            if (batchConnectionPointerIdRef.current === event.pointerId) {
                clearBatchConnection();
                return;
            }
            if (connectingPointerIdRef.current === event.pointerId) setConnecting(null);
        };
        const cancel = () => {
            cancelPendingPointerMove();
            updateConnectionReplaceHover(null);
            if (connectingParamsRef.current) setConnecting(null);
            if (batchConnectionPreviewRef.current) clearBatchConnection();
        };
        window.addEventListener("pointermove", handlePointerMove);
        window.addEventListener("pointerup", handlePointerUp);
        window.addEventListener("pointercancel", handlePointerCancel);
        window.addEventListener("blur", cancel);
        return () => {
            cancelPendingPointerMove();
            window.removeEventListener("pointermove", handlePointerMove);
            window.removeEventListener("pointerup", handlePointerUp);
            window.removeEventListener("pointercancel", handlePointerCancel);
            window.removeEventListener("blur", cancel);
        };
    }, [clearBatchConnection, commitBatchConnection, finishBatchConnection, finishConnection, getBatchConnectionDropTarget, getConnectionDropTarget, openBatchConnectionCreateMenu, previewBatchConnection, screenToCanvas, setConnecting]);

    return {
        cancelPendingConnectionCreate,
        closeConnectionCreateMenu,
        connectionTargetNodeId,
        connectionApproach,
        connectionTargetAnchorRatio,
        connectionReplaceHover,
        connectingParams,
        createConnectedDirector,
        createConnectedNode,
        getConnectionCreateDisabledReason,
        handleConnectStart,
        handleConnectDrop,
        handleBatchConnectionTargetClick,
        batchConnectionPreview,
        beginBatchConnectionMode,
        startBatchConnection,
        mouseWorld,
        pendingConnectionCreate,
        registerConnectionDraft,
        setConnecting,
    };
}
