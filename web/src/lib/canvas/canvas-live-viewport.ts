import type { SelectionBox, ViewportTransform } from "@/types/canvas";

export const CANVAS_VIEWPORT_PREVIEW_EVENT = "canvas:viewport-preview";
export const CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT = "canvas:graphics-viewport-preview";
export const CANVAS_SELECTION_PREVIEW_EVENT = "canvas:selection-preview";
export const CANVAS_NODE_DRAG_PREVIEW_EVENT = "canvas:node-drag-preview";

export type CanvasNodeDragPreview = {
    x: number;
    y: number;
    nodeIds: ReadonlySet<string>;
};

type NodeDragPreviewDomState = {
    elementsById: Map<string, HTMLElement>;
    previousIds: Set<string>;
    selectionBounds: HTMLElement | null;
};

type NodeSelectionPreviewDomState = {
    elementsById: Map<string, HTMLElement>;
    previousStates: Map<string, "include" | "remove">;
};

export type CanvasNodeSelectionPreview = {
    includeNodeIds: ReadonlySet<string>;
    removeNodeIds: ReadonlySet<string>;
};

const nodeDragPreviewDomStates = new WeakMap<HTMLDivElement, NodeDragPreviewDomState>();
const nodeSelectionPreviewDomStates = new WeakMap<HTMLDivElement, NodeSelectionPreviewDomState>();
const liveViewportElements = new WeakMap<HTMLDivElement, { worldLayer: HTMLElement | null }>();
/**
 * 逐帧消费实时倍率的元素（目前是外置节点标题），按画布容器分组。
 *
 * 曾经这些值统一写在画布容器的内联样式上，容器是整棵画布子树的祖先，
 * 一次继承型自定义属性写入会让全部节点重新参与样式计算：实测 24 节点 /
 * 90 次滚轮缩放在 4 倍降频下「重算样式」从 18ms/步 涨到 43ms/步，
 * Windows 上就是用户感受到的缩放掉帧。改成只写真正读它的元素后，
 * 失效范围从整棵子树收敛到这几个标题元素本身。
 */
const liveScaleTargets = new WeakMap<HTMLElement, Set<HTMLElement>>();
/** 框选轮廓用 calc() 读实时逆倍率，只有它需要容器级变量逐帧更新。 */
const containerLiveScaleConsumers = new WeakSet<HTMLElement>();

/**
 * 注册需要在缩放期保持屏幕尺寸的元素。返回注销函数，元素卸载时必须调用，
 * 否则集合会留下游离节点（每帧多写一次样式）。
 */
export function registerCanvasLiveScaleTarget(element: HTMLElement | null) {
    if (!element) return () => {};
    const container = element.closest<HTMLElement>("[data-canvas-viewport]");
    if (!container) return () => {};
    let targets = liveScaleTargets.get(container);
    if (!targets) {
        targets = new Set();
        liveScaleTargets.set(container, targets);
    }
    targets.add(element);
    return () => {
        targets.delete(element);
        if (targets.size === 0) liveScaleTargets.delete(container);
    };
}

export type CanvasLiveViewportOptions = {
    /** 通知浮层 / 小地图等订阅方，按 32ms 节流而非每帧。 */
    notify?: boolean;
    /** 提交态：把视口写回容器变量，供静止期布局与非交互期 CSS 继承读取。 */
    commit?: boolean;
    /**
     * 只重算世界层补偿倍率，不广播预览事件。
     *
     * React 提交视口的那一帧必须重算补偿（光栅层倍率刚变化），但视口本身没变，
     * 图形层不需要按新视口重画：广播会让 Leafer 每次虚拟化提交都整帧重绘。
     */
    silent?: boolean;
};

export function applyCanvasLiveViewport(container: HTMLDivElement | null, viewport: ViewportTransform, options: CanvasLiveViewportOptions = {}) {
    const { notify = true, commit = false, silent = false } = options;
    if (!container) return;
    const committedScale = Number(container.style.getPropertyValue("--canvas-committed-scale")) || viewport.k;
    let elements = liveViewportElements.get(container);
    if (!elements) {
        elements = {
            worldLayer: container.querySelector<HTMLElement>("[data-canvas-world-layer]"),
        };
        liveViewportElements.set(container, elements);
    }
    const worldLayer = elements.worldLayer;
    if (worldLayer) {
        // 平移期间直接更新合成层，避免修改容器继承变量导致所有节点重新计算样式。
        worldLayer.style.transformOrigin = "0 0";
        worldLayer.style.transform = `translate3d(${viewport.x}px, ${viewport.y}px, 0) scale(${viewport.k / committedScale})`;
        worldLayer.style.willChange = container.dataset.canvasViewportInteracting === "true" ? "transform" : "";
    }
    // 外置节点标题用同一帧逆倍率抵消世界层缩放，避免等待 React 提交后再校正尺寸。
    // 只写注册过的元素本身；容器变量仅在提交态或框选轮廓需要时更新。
    const inverseScale = String(1 / Math.max(viewport.k, 0.05));
    // 交互期提交路径拿到的 React 视口最多滞后一次虚拟化刷新（64ms），
    // 用它覆盖逐帧值会让标题在缩放中途突然跳大小；此时交给逐帧写入。
    const interacting = container.dataset.canvasViewportInteracting === "true";
    const targets = liveScaleTargets.get(container);
    if (targets && !(commit && interacting)) {
        for (const target of targets) {
            if (!target.isConnected) continue;
            target.style.setProperty("--canvas-live-inverse-scale", inverseScale);
        }
    }
    // Keep the live camera coordinates observable to overlays and automation
    // while the world layer is moved through a compositor transform.
    if (commit || containerLiveScaleConsumers.has(container)) {
        container.style.setProperty("--canvas-live-x", String(viewport.x));
        container.style.setProperty("--canvas-live-y", String(viewport.y));
        container.style.setProperty("--canvas-live-scale", String(viewport.k));
        container.style.setProperty("--canvas-live-inverse-scale", inverseScale);
    }
    if (silent) return;
    // 图形层必须逐帧跟随 DOM 世界层；浮层和滚动通知仍可按原频率节流。
    container.dispatchEvent(new CustomEvent<ViewportTransform>(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, { detail: viewport }));
    if (notify) {
        container.dispatchEvent(new CustomEvent<ViewportTransform>(CANVAS_VIEWPORT_PREVIEW_EVENT, { detail: viewport }));
        // Ant Design overlays watch scrollable ancestors, but CSS transforms do not emit layout events.
        container.dispatchEvent(new Event("scroll"));
    }
}

export function subscribeCanvasGraphicsViewportPreview(container: HTMLDivElement, listener: (viewport: ViewportTransform) => void) {
    const handlePreview = (event: Event) => listener((event as CustomEvent<ViewportTransform>).detail);
    container.addEventListener(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, handlePreview);
    return () => container.removeEventListener(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, handlePreview);
}

export function subscribeCanvasViewportPreview(container: HTMLDivElement, listener: (viewport: ViewportTransform) => void) {
    const handlePreview = (event: Event) => listener((event as CustomEvent<ViewportTransform>).detail);
    container.addEventListener(CANVAS_VIEWPORT_PREVIEW_EVENT, handlePreview);
    return () => container.removeEventListener(CANVAS_VIEWPORT_PREVIEW_EVENT, handlePreview);
}

/**
 * Applies transient node movement without changing React state. Only nodes
 * currently mounted in the virtualized world are touched; the committed
 * positions are still written by the drag-end path.
 */
export function applyCanvasNodeDragPreview(container: HTMLDivElement | null, preview: CanvasNodeDragPreview | null) {
    if (!container) return;
    if (preview) container.dataset.canvasNodeDragging = "true";
    else delete container.dataset.canvasNodeDragging;

    let state = nodeDragPreviewDomStates.get(container);
    if (!state) {
        state = { elementsById: new Map(), previousIds: new Set(), selectionBounds: null };
        nodeDragPreviewDomStates.set(container, state);
    }

    for (const nodeId of state.previousIds) {
        state.elementsById.get(nodeId)?.style.removeProperty("translate");
    }
    state.selectionBounds?.style.removeProperty("translate");

    state.previousIds.clear();
    if (preview) {
        // Build the lookup once per drag session. A single DOM scan is much
        // cheaper than one selector query per selected node on every frame.
        if (state.elementsById.size === 0) {
            container.querySelectorAll<HTMLElement>("[data-node-id]").forEach((element) => {
                const nodeId = element.dataset.nodeId;
                if (nodeId) state?.elementsById.set(nodeId, element);
            });
            state.selectionBounds = container.querySelector<HTMLElement>("[data-canvas-selection-bounds]");
        }
        for (const nodeId of preview.nodeIds) {
            const element = state.elementsById.get(nodeId);
            if (!element || !element.isConnected) continue;
            element.style.setProperty("translate", `${preview.x}px ${preview.y}px`);
            state.previousIds.add(nodeId);
        }
        if (!state.selectionBounds?.isConnected) {
            state.selectionBounds = container.querySelector<HTMLElement>("[data-canvas-selection-bounds]");
        }
        state.selectionBounds?.style.setProperty("translate", `${preview.x}px ${preview.y}px`);
    } else {
        state.elementsById.clear();
        state.selectionBounds = null;
    }

    container.dispatchEvent(new CustomEvent<CanvasNodeDragPreview | null>(CANVAS_NODE_DRAG_PREVIEW_EVENT, { detail: preview }));
}

export function subscribeCanvasNodeDragPreview(container: HTMLDivElement, listener: (preview: CanvasNodeDragPreview | null) => void) {
    const handlePreview = (event: Event) => listener((event as CustomEvent<CanvasNodeDragPreview | null>).detail);
    container.addEventListener(CANVAS_NODE_DRAG_PREVIEW_EVENT, handlePreview);
    return () => container.removeEventListener(CANVAS_NODE_DRAG_PREVIEW_EVENT, handlePreview);
}

/**
 * Shows the selection delta on mounted nodes without changing React state.
 * The controller commits the final Set once on pointer-up.
 */
export function applyCanvasNodeSelectionPreview(container: HTMLDivElement | null, preview: CanvasNodeSelectionPreview | null) {
    if (!container) return;

    let state = nodeSelectionPreviewDomStates.get(container);
    if (!state) {
        state = { elementsById: new Map(), previousStates: new Map() };
        nodeSelectionPreviewDomStates.set(container, state);
    }

    const nextStates = new Map<string, "include" | "remove">();
    if (preview) {
        for (const nodeId of preview.includeNodeIds) nextStates.set(nodeId, "include");
        for (const nodeId of preview.removeNodeIds) nextStates.set(nodeId, "remove");
    }

    if (state.elementsById.size === 0 && nextStates.size > 0) {
        container.querySelectorAll<HTMLElement>("[data-node-id]").forEach((element) => {
            const nodeId = element.dataset.nodeId;
            if (nodeId) state?.elementsById.set(nodeId, element);
        });
    }

    for (const [nodeId, previousState] of state.previousStates) {
        if (nextStates.get(nodeId) === previousState) continue;
        state.elementsById.get(nodeId)?.removeAttribute("data-canvas-selection-preview");
    }
    for (const [nodeId, nextState] of nextStates) {
        if (state.previousStates.get(nodeId) === nextState) continue;
        const element = state.elementsById.get(nodeId);
        if (!element || !element.isConnected) continue;
        element.dataset.canvasSelectionPreview = nextState;
    }

    state.previousStates = nextStates;
    if (nextStates.size > 0) containerLiveScaleConsumers.add(container);
    else containerLiveScaleConsumers.delete(container);
    if (!preview) state.elementsById.clear();
}

export function applyCanvasSelectionPreview(container: HTMLDivElement | null, selection: SelectionBox) {
    container?.dispatchEvent(new CustomEvent<SelectionBox>(CANVAS_SELECTION_PREVIEW_EVENT, { detail: selection }));
}

export function subscribeCanvasSelectionPreview(container: HTMLDivElement, listener: (selection: SelectionBox) => void) {
    const handlePreview = (event: Event) => listener((event as CustomEvent<SelectionBox>).detail);
    container.addEventListener(CANVAS_SELECTION_PREVIEW_EVENT, handlePreview);
    return () => container.removeEventListener(CANVAS_SELECTION_PREVIEW_EVENT, handlePreview);
}
