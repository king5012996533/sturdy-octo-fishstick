import { AlertTriangle, Image as ImageIcon, LoaderCircle, RefreshCw } from "lucide-react";

import type { CanvasTheme } from "@/lib/canvas-theme";

export type CanvasImageNodePlaceholderState = "empty" | "loading" | "failed";

type CanvasImageNodePlaceholderProps = {
    theme: CanvasTheme;
    state: CanvasImageNodePlaceholderState;
    /** 空节点上的一句话说明；角色三视图等多视角节点用它替换默认文案。 */
    hint?: string;
    onRetry?: () => void;
};

/**
 * 图片节点的占位态。
 *
 * 之前"从来没有图""还没取到图""取图失败"共用同一个低透明度图标，用户会把
 * 三者一律读成破图（Windows 上缩放时反馈过一次），真实失败也没有恢复入口。
 * 这里把三态拆开：空节点说明自己是空的，加载中给转圈，失败才给重试。
 */
export function CanvasImageNodePlaceholder({ theme, state, hint, onRetry }: CanvasImageNodePlaceholderProps) {
    if (state === "loading") {
        return (
            <div role="status" data-canvas-image-state="loading" className="grid size-full place-items-center" style={{ color: theme.node.muted }}>
                <LoaderCircle className="size-5 animate-spin" aria-label="图片加载中" />
            </div>
        );
    }
    if (state === "failed") {
        return (
            <div data-canvas-image-state="failed" className="flex size-full flex-col items-center justify-center gap-2 px-3 text-center" style={{ color: theme.node.muted }}>
                <AlertTriangle className="size-7 opacity-60" strokeWidth={1.35} aria-hidden />
                <span className="text-xs font-medium">图片加载失败</span>
                {onRetry ? (
                    <button
                        type="button"
                        data-canvas-image-retry
                        className="pointer-events-auto inline-flex items-center gap-1 rounded-[var(--r-sm)] border px-2 py-1 text-[var(--fs-tiny)] transition-opacity hover:opacity-75"
                        style={{ borderColor: theme.node.stroke, color: theme.node.text }}
                        onPointerDown={(event) => event.stopPropagation()}
                        onMouseDown={(event) => event.stopPropagation()}
                        onClick={(event) => {
                            event.stopPropagation();
                            onRetry();
                        }}
                    >
                        <RefreshCw className="size-3" aria-hidden />
                        重试
                    </button>
                ) : null}
            </div>
        );
    }
    return (
        <div data-canvas-image-state="empty" className="flex size-full flex-col items-center justify-center gap-2 px-3 text-center" style={{ color: theme.node.muted }}>
            <ImageIcon className="canvas-node-empty-image-mark size-14 opacity-30" strokeWidth={1.35} aria-hidden />
            <span className="text-xs font-medium opacity-70">空白图片节点</span>
            <span className="max-w-full truncate text-[var(--fs-tiny)] opacity-45" title={hint}>{hint || "工具栏可生成或上传"}</span>
        </div>
    );
}
