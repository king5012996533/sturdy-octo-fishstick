import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

import { resolveCanvasAppearance, resolveCanvasGridColor, type CanvasAppearance } from "@/lib/canvas/canvas-appearance";
import { resolveCanvasPointerIntent } from "@/lib/canvas/canvas-selection";
import type { CanvasBackgroundMode } from "@/lib/canvas-theme";
import { applyCanvasLiveViewport, subscribeCanvasViewportPreview } from "@/lib/canvas/canvas-live-viewport";
import { canvasWheelDeltaToPixels, canvasWheelZoomFactor, clampCanvasScale, resolveCanvasWheelIntent } from "@/lib/canvas/canvas-wheel-zoom";
import { canvasZoomGlideEnabled } from "@/lib/canvas/canvas-zoom-glide";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";
import type { ViewportTransform } from "@/types/canvas";
import { useCanvasWheelZoomGlide } from "./use-canvas-wheel-zoom-glide";

type InfiniteCanvasProps = {
    interactive?: boolean;
    containerRef: React.RefObject<HTMLDivElement | null>;
    viewport: ViewportTransform;
    appearance?: CanvasAppearance;
    backgroundMode?: CanvasBackgroundMode;
    onViewportChange: (viewport: ViewportTransform) => void;
    onViewportPreviewChange?: (viewport: ViewportTransform) => void;
    /** 把"停掉滚轮滑行"交给页面控制器：面板/小地图/过渡开始驱动视口时要立刻停，避免两边抢写。 */
    registerViewportGlideCancel?: (cancel: (() => void) | null) => void;
    onCanvasMouseDown?: (event: React.PointerEvent<HTMLDivElement>) => void;
    boxSelectEnabled?: boolean;
    onCanvasDoubleClick?: (event: React.MouseEvent<HTMLDivElement>) => void;
    onCanvasDeselect?: () => void;
    onContextMenu?: (event: React.MouseEvent) => void;
    onDrop?: (event: React.DragEvent<HTMLDivElement>) => void;
    onFileDragEnter?: (event: React.DragEvent<HTMLDivElement>) => void;
    onFileDragLeave?: (event: React.DragEvent<HTMLDivElement>) => void;
    onFileDragOver?: (event: React.DragEvent<HTMLDivElement>) => void;
    children: React.ReactNode;
    graphicsLayer?: React.ReactNode;
};

const CANVAS_WHEEL_IGNORE_SELECTOR = "[data-canvas-no-zoom],[data-canvas-wheel-scroll],.ant-modal,.ant-popover,.ant-dropdown,.ant-select-dropdown,.ant-picker-dropdown";
const CANVAS_POINTER_IGNORE_SELECTOR = "[data-canvas-no-zoom],[data-connection-create-menu],.ant-modal,.ant-popover,.ant-dropdown,.ant-select-dropdown,.ant-picker-dropdown";

type TouchPoint = { x: number; y: number };

type PinchState = {
    active: boolean;
    pointerIds: [number, number];
    initialDistance: number;
    worldX: number;
    worldY: number;
    initialScale: number;
};

export function InfiniteCanvas({ interactive = true, containerRef, viewport, appearance, backgroundMode = "lines", onViewportChange, onViewportPreviewChange, registerViewportGlideCancel, onCanvasMouseDown, boxSelectEnabled = false, onCanvasDoubleClick, onCanvasDeselect, onContextMenu, onDrop, onFileDragEnter, onFileDragLeave, onFileDragOver, graphicsLayer, children }: InfiniteCanvasProps) {
    const colorTheme = useActiveTheme();
    const resolvedAppearance = resolveCanvasAppearance(appearance, colorTheme);
    const panState = useRef({
        isPanning: false,
        pointerId: -1,
        startX: 0,
        startY: 0,
        initialX: 0,
        initialY: 0,
        hasMoved: false,
    });
    const viewportRef = useRef(viewport);
    const scaleRef = useRef(viewport.k);
    const containerRectRef = useRef<DOMRect | null>(null);
    const frameRef = useRef<number | null>(null);
    const nextViewportRef = useRef<ViewportTransform | null>(null);
    const syncTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const lastPreviewNotifyRef = useRef(0);
    const interactingRef = useRef(false);
    /** 滑行取消函数由下面的滑行 hook 提供；这里留一个 ref，供更早声明的 effect 调用。 */
    const cancelGlideRef = useRef<(() => void) | null>(null);
    const touchPointsRef = useRef(new Map<number, TouchPoint>());
    const pinchStateRef = useRef<PinchState>({ active: false, pointerIds: [-1, -1], initialDistance: 1, worldX: 0, worldY: 0, initialScale: viewport.k });
    const spacePressedRef = useRef(false);
    const [isSpacePressed, setIsSpacePressed] = useState(false);
    const [isPanning, setIsPanning] = useState(false);
    /**
     * 交互结束的显式信号。
     *
     * 复位世界层变换的 layout effect 依赖视口值；但缩放过程里虚拟化节流会把同一个视口
     * 先提交进 React，等交互真正结束时 setViewport 因值相同而 bail，effect 不再执行，
     * 世界层就留着"交互期倍率"永远不复位：节点层停在 k×ratio，而连线层（Leafer）按真实
     * 视口画，两边错开，看起来就是连线从节点上崩开。用自增信号保证每次交互结束都复位一次。
     */
    const [viewportCommitEpoch, setViewportCommitEpoch] = useState(0);
    const endViewportInteraction = useCallback(() => {
        interactingRef.current = false;
        delete containerRef.current?.dataset.canvasViewportInteracting;
        setViewportCommitEpoch((epoch) => epoch + 1);
    }, [containerRef]);

    useLayoutEffect(() => {
        if (interactive) return;
        const container = containerRef.current;
        for (const id of [panState.current.pointerId, ...touchPointsRef.current.keys()]) {
            if (container?.hasPointerCapture(id)) container.releasePointerCapture(id);
        }
        panState.current.isPanning = false;
        pinchStateRef.current.active = false;
        touchPointsRef.current.clear();
        cancelGlideRef.current?.();
        endViewportInteraction();
        if (frameRef.current) cancelAnimationFrame(frameRef.current);
        if (syncTimerRef.current) clearTimeout(syncTimerRef.current);
        frameRef.current = null;
        syncTimerRef.current = null;
        nextViewportRef.current = null;
        delete container?.dataset.canvasViewportInteracting;
        setIsPanning(false);
        setIsSpacePressed(false);
        document.body.style.cursor = "default";
    }, [interactive, containerRef]);

    useLayoutEffect(() => {
        const container = containerRef.current;
        /**
         * React 提交视口意味着光栅层倍率（--canvas-committed-scale）刚刚变化，世界层的
         * 补偿倍率必须同一帧按实时视口重算。
         *
         * 补偿只在逐帧写入里更新的话，提交之后的那几帧会渲染成 实时倍率 × 补偿倍率：
         * 滚一格（1.14×）实际画出 1.14×1.14，等于比预想大 30%，而且会一直停在这个倍率上，
         * 直到下一次滚轮写入才纠正——用户看到的就是每滚一格放大一截再弹回去。鼠标滚轮是
         * 整档跳变，最明显；触控板与面板缩放是连续小量、比值为 1.00x，所以只有滚轮用户看得出。
         */
        if (interactingRef.current) {
            applyCanvasLiveViewport(container, viewportRef.current, { silent: true });
            return;
        }
        viewportRef.current = viewport;
        scaleRef.current = viewport.k;
        applyCanvasLiveViewport(container, viewport, { commit: true });
    }, [containerRef, viewport, viewportCommitEpoch]);

    useEffect(() => {
        const container = containerRef.current;
        if (!container) return;
        return subscribeCanvasViewportPreview(container, (next) => {
            viewportRef.current = next;
            scaleRef.current = next.k;
        });
    }, [containerRef]);

    useEffect(
        () => () => {
            if (frameRef.current) cancelAnimationFrame(frameRef.current);
            if (syncTimerRef.current) clearTimeout(syncTimerRef.current);
            delete containerRef.current?.dataset.canvasViewportInteracting;
            document.body.style.cursor = "";
        },
        [containerRef],
    );

    const syncViewport = useCallback(() => { if (interactive) onViewportChange(viewportRef.current); }, [interactive, onViewportChange]);

    const scheduleViewportChange = useCallback(
        (next: ViewportTransform, commitAfterIdle = false) => {
            viewportRef.current = next;
            scaleRef.current = next.k;
            onViewportPreviewChange?.(next);
            const container = containerRef.current;
            // 与视口无关的属性重复写会反复触发 [data-canvas-viewport-interacting] 子树失效，只写变化。
            if (container && container.dataset.canvasViewportInteracting !== "true") container.dataset.canvasViewportInteracting = "true";
            nextViewportRef.current = next;
            if (frameRef.current) return;
            frameRef.current = requestAnimationFrame((now) => {
                frameRef.current = null;
                const pending = nextViewportRef.current;
                if (!pending) return;
                const notify = now - lastPreviewNotifyRef.current >= 32;
                applyCanvasLiveViewport(containerRef.current, pending, { notify });
                if (notify) lastPreviewNotifyRef.current = now;
            });
            if (!commitAfterIdle) return;
            if (syncTimerRef.current) clearTimeout(syncTimerRef.current);
            syncTimerRef.current = setTimeout(() => {
                endViewportInteraction();
                syncViewport();
                syncTimerRef.current = null;
            }, 120);
        },
        [containerRef, onViewportPreviewChange, syncViewport],
    );

    const { glideTo, glideTarget, cancelGlide } = useCanvasWheelZoomGlide({
        applyViewport: scheduleViewportChange,
        readViewport: useCallback(() => viewportRef.current, []),
        finish: useCallback(() => {
            endViewportInteraction();
            syncViewport();
        }, [endViewportInteraction, syncViewport]),
    });

    useEffect(() => {
        cancelGlideRef.current = cancelGlide;
    }, [cancelGlide]);

    useEffect(() => {
        if (!registerViewportGlideCancel) return;
        registerViewportGlideCancel(cancelGlide);
        return () => registerViewportGlideCancel(null);
    }, [cancelGlide, registerViewportGlideCancel]);

    useEffect(() => {
        if (!interactive) return;
        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.code !== "Space") return;
            if (event.target instanceof Element && event.target.closest("input,textarea,select,button,[contenteditable='true']")) return;
            event.preventDefault();
            spacePressedRef.current = true;
            setIsSpacePressed(true);
        };

        const handleKeyUp = (event: KeyboardEvent) => {
            if (event.code !== "Space") return;
            spacePressedRef.current = false;
            setIsSpacePressed(false);
        };

        const handleBlur = () => {
            spacePressedRef.current = false;
            setIsSpacePressed(false);
        };

        window.addEventListener("keydown", handleKeyDown);
        window.addEventListener("keyup", handleKeyUp);
        window.addEventListener("blur", handleBlur);
        return () => {
            window.removeEventListener("keydown", handleKeyDown);
            window.removeEventListener("keyup", handleKeyUp);
            window.removeEventListener("blur", handleBlur);
        };
    }, [interactive]);

    const handleWheel = useCallback(
        (event: WheelEvent) => {
            const target = event.target instanceof Element ? event.target : null;
            const absX = Math.abs(canvasWheelDeltaToPixels(event.deltaX, event.deltaMode));
            const absY = Math.abs(canvasWheelDeltaToPixels(event.deltaY, event.deltaMode));
            const isPinchZoom = event.ctrlKey || event.metaKey;
            if (target?.closest(CANVAS_WHEEL_IGNORE_SELECTOR) && !isPinchZoom) {
                // 内部区域保留纵向滚动，但横向手势不能泄漏为 macOS 浏览器前进/后退。
                if (event.shiftKey || absX > absY) event.preventDefault();
                return;
            }

            const intent = resolveCanvasWheelIntent(event);
            if (intent.kind === "none") return;
            // Ctrl/Meta + 滚轮在画布内始终由画布接管，避免浮层区域触发浏览器页面缩放。
            event.preventDefault();
            interactingRef.current = true;
            const current = viewportRef.current;

            if (intent.kind === "pan") {
                // 平移和缩放的滑行不能同时写视口：先停滑行，再按指针/滚轮的位置走。
                cancelGlide();
                scheduleViewportChange({
                    x: current.x - intent.deltaX,
                    y: current.y - intent.deltaY,
                    k: current.k,
                }, true);
                return;
            }

            const rect = containerRectRef.current || containerRef.current?.getBoundingClientRect();
            if (!rect) return;
            const mouseX = event.clientX - rect.left;
            const mouseY = event.clientY - rect.top;
            // 连续滚动时以"滑行目标"为基准累积：画面还在追上一格时按实时视口算，会把刚滚的档位吃掉。
            const base = glideTarget() ?? current;
            // 一档固定一个倍率：设备档距（100 / 120 / 80 / 53 像素、行模式 3 行）只决定"几档"。
            const newScale = clampCanvasScale(base.k * canvasWheelZoomFactor(intent.notches));
            const worldX = (mouseX - base.x) / base.k;
            const worldY = (mouseY - base.y) / base.k;

            // 整档跳变滑行成连续推镜；捏合本来就是连续量，直接跟手。
            glideTo({
                x: mouseX - worldX * newScale,
                y: mouseY - worldY * newScale,
                k: newScale,
            }, { animated: intent.source === "notch" && canvasZoomGlideEnabled() });
        },
        [cancelGlide, containerRef, glideTarget, glideTo, scheduleViewportChange],
    );

    const handlePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
        if (!interactive) return;
        // 指针一旦落到画布上（拖拽、框选、双指捏合），滚轮滑行就要立刻让位，
        // 否则滑行目标会继续按帧覆盖指针刚写下的视口。
        cancelGlide();
        const target = event.target instanceof Element ? event.target : null;
        // AntD 浮层通过 Portal 渲染到节点 DOM 之外；若不统一排除，会被误判为画布空白并捕获指针。
        if (target?.closest(CANVAS_POINTER_IGNORE_SELECTOR)) return;
        const isBackgroundClick = !target?.closest("[data-node-id],[data-connection-id]");
        const isTouch = event.pointerType === "touch";

        const pointerIntent = resolveCanvasPointerIntent({
            altKey: event.altKey,
            background: isBackgroundClick,
            boxSelectEnabled,
            button: event.button,
            ctrlKey: event.ctrlKey,
            metaKey: event.metaKey,
            pointerType: event.pointerType,
            shiftKey: event.shiftKey,
            spacePressed: spacePressedRef.current,
        });
        if (pointerIntent === "select") {
            event.preventDefault();
            event.currentTarget.setPointerCapture(event.pointerId);
            onCanvasMouseDown?.(event);
            return;
        }

        if (isTouch) {
            touchPointsRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
            if (touchPointsRef.current.size >= 2) {
                const [[firstId, first], [secondId, second]] = Array.from(touchPointsRef.current.entries());
                event.preventDefault();
                event.currentTarget.setPointerCapture(firstId);
                event.currentTarget.setPointerCapture(secondId);
                const rect = containerRectRef.current || event.currentTarget.getBoundingClientRect();
                const current = viewportRef.current;
                const centerX = (first.x + second.x) / 2 - rect.left;
                const centerY = (first.y + second.y) / 2 - rect.top;
                pinchStateRef.current = {
                    active: true,
                    pointerIds: [firstId, secondId],
                    initialDistance: Math.max(Math.hypot(second.x - first.x, second.y - first.y), 1),
                    worldX: (centerX - current.x) / current.k,
                    worldY: (centerY - current.y) / current.k,
                    initialScale: current.k,
                };
                panState.current.isPanning = false;
                interactingRef.current = true;
                return;
            }

            if (!isBackgroundClick) return;
            event.preventDefault();
            event.currentTarget.setPointerCapture(event.pointerId);
            if (!event.isPrimary) return;
            const current = viewportRef.current;
            interactingRef.current = true;
            panState.current = {
                isPanning: true,
                pointerId: event.pointerId,
                startX: event.clientX,
                startY: event.clientY,
                initialX: current.x,
                initialY: current.y,
                hasMoved: false,
            };
            setIsPanning(true);
            document.body.style.cursor = "grabbing";
            return;
        }

        if (pointerIntent === "pan") {
            const current = viewportRef.current;
            event.preventDefault();
            event.currentTarget.setPointerCapture(event.pointerId);
            interactingRef.current = true;
            panState.current = {
                isPanning: true,
                pointerId: event.pointerId,
                startX: event.clientX,
                startY: event.clientY,
                initialX: current.x,
                initialY: current.y,
                hasMoved: false,
            };
            setIsPanning(true);
            document.body.style.cursor = "grabbing";
        }

    };

    useEffect(() => {
        if (!interactive) return;
        const handlePointerMove = (event: PointerEvent) => {
            if (event.pointerType === "touch" && touchPointsRef.current.has(event.pointerId)) {
                touchPointsRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
                const pinch = pinchStateRef.current;
                if (pinch.active) {
                    const first = touchPointsRef.current.get(pinch.pointerIds[0]);
                    const second = touchPointsRef.current.get(pinch.pointerIds[1]);
                    const rect = containerRectRef.current || containerRef.current?.getBoundingClientRect();
                    if (!first || !second || !rect) return;
                    event.preventDefault();
                    const centerX = (first.x + second.x) / 2 - rect.left;
                    const centerY = (first.y + second.y) / 2 - rect.top;
                    const distance = Math.max(Math.hypot(second.x - first.x, second.y - first.y), 1);
                    const scale = clampCanvasScale(pinch.initialScale * (distance / pinch.initialDistance));
                    scheduleViewportChange({
                        x: centerX - pinch.worldX * scale,
                        y: centerY - pinch.worldY * scale,
                        k: scale,
                    });
                    return;
                }
            }

            if (!panState.current.isPanning || panState.current.pointerId !== event.pointerId) return;

            const dx = event.clientX - panState.current.startX;
            const dy = event.clientY - panState.current.startY;
            if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
                panState.current.hasMoved = true;
            }

            scheduleViewportChange({
                x: panState.current.initialX + dx,
                y: panState.current.initialY + dy,
                k: scaleRef.current,
            });
        };

        const handlePointerEnd = (event: PointerEvent) => {
            if (event.pointerType === "touch" && pinchStateRef.current.active && pinchStateRef.current.pointerIds.includes(event.pointerId)) {
                pinchStateRef.current.active = false;
                touchPointsRef.current.clear();
                panState.current.isPanning = false;
                panState.current.pointerId = -1;
                endViewportInteraction();
                if (syncTimerRef.current) clearTimeout(syncTimerRef.current);
                syncViewport();
                setIsPanning(false);
                document.body.style.cursor = "";
                return;
            }

            if (event.pointerType === "touch") touchPointsRef.current.delete(event.pointerId);
            if (!panState.current.isPanning || panState.current.pointerId !== event.pointerId) return;

            if (event.type === "pointerup" && !panState.current.hasMoved) {
                onCanvasDeselect?.();
            }
            panState.current.isPanning = false;
            panState.current.pointerId = -1;
            endViewportInteraction();
            if (syncTimerRef.current) clearTimeout(syncTimerRef.current);
            syncViewport();
            setIsPanning(false);
            document.body.style.cursor = "";
        };

        window.addEventListener("pointermove", handlePointerMove);
        window.addEventListener("pointerup", handlePointerEnd);
        window.addEventListener("pointercancel", handlePointerEnd);
        return () => {
            window.removeEventListener("pointermove", handlePointerMove);
            window.removeEventListener("pointerup", handlePointerEnd);
            window.removeEventListener("pointercancel", handlePointerEnd);
        };
    }, [interactive, containerRef, onCanvasDeselect, scheduleViewportChange, syncViewport]);

    useEffect(() => {
        const container = containerRef.current;
        if (!container || !interactive) return;
        const updateRect = () => {
            containerRectRef.current = container.getBoundingClientRect();
        };
        updateRect();
        const observer = new ResizeObserver(updateRect);
        observer.observe(container);
        window.addEventListener("resize", updateRect);
        container.addEventListener("wheel", handleWheel, { passive: false, capture: true });
        return () => {
            observer.disconnect();
            window.removeEventListener("resize", updateRect);
            container.removeEventListener("wheel", handleWheel, { capture: true });
        };
    }, [interactive, containerRef, handleWheel]);

    return (
        <div
            ref={containerRef}
            data-canvas-viewport
            data-canvas-pan-state={isPanning ? "grabbing" : isSpacePressed || !boxSelectEnabled ? "grab" : undefined}
            className={`relative h-full w-full select-none overflow-hidden touch-none ${isPanning ? "cursor-grabbing" : isSpacePressed || !boxSelectEnabled ? "cursor-grab" : "canvas-cursor-select"}`}
            style={{
                background: resolvedAppearance.background,
                overscrollBehavior: "none",
                "--canvas-live-x": `${viewport.x}px`,
                "--canvas-live-y": `${viewport.y}px`,
                "--canvas-live-scale": viewport.k,
                "--canvas-live-inverse-scale": 1 / Math.max(viewport.k, 0.05),
                "--canvas-committed-scale": viewport.k,
            } as React.CSSProperties}
            onPointerDown={handlePointerDown}
            onDoubleClick={(event) => {
                const target = event.target instanceof Element ? event.target : null;
                if (!target?.closest("[data-node-id],[data-connection-id],[data-canvas-no-zoom]")) onCanvasDoubleClick?.(event);
            }}
            onContextMenu={onContextMenu}
            onDragEnter={onFileDragEnter}
            onDragLeave={onFileDragLeave}
            onDragOver={(event) => {
                event.preventDefault();
                onFileDragOver?.(event);
            }}
            onDrop={onDrop}
        >
            <CanvasGrid appearance={appearance} mode={backgroundMode} />
            {graphicsLayer}
            <div
                data-canvas-world-layer
                className="canvas-world-layer absolute origin-top-left"
            >
                <div data-canvas-world-raster-layer className="canvas-world-raster-layer absolute origin-top-left">
                    {children}
                </div>
            </div>
        </div>
    );
}

function CanvasGrid({ appearance, mode }: { appearance?: CanvasAppearance; mode: CanvasBackgroundMode }) {
    const colorTheme = useActiveTheme();
    const gridColor = resolveCanvasGridColor(appearance, colorTheme, mode);
    const backgroundImage = mode === "dots" ? `radial-gradient(circle, ${gridColor} 0.8px, transparent 1px)` : `linear-gradient(${gridColor} 1px, transparent 1px), linear-gradient(90deg, ${gridColor} 1px, transparent 1px)`;
    if (mode === "blank") return null;

    return (
        <div
            data-canvas-grid-layer
            className="pointer-events-none absolute"
            style={{
                // 装饰网格固定在屏幕坐标，避免缩放时改变密度或产生亚像素位移闪烁。
                inset: 0,
                backgroundImage,
                backgroundSize: "48px 48px",
                opacity: mode === "dots" ? 0.34 : 0.46,
            }}
        />
    );
}
