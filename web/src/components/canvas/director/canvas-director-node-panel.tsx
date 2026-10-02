import { useEffect, useState, type ReactNode } from "react";
import { Clapperboard, Move3d } from "lucide-react";

import { canvasThemes, type CanvasTheme } from "@/lib/canvas-theme";
import { resolveDirectorActiveShot, resolveDirectorPreviewSource, type DirectorNodeContentReader } from "@/lib/canvas/director/director-preview";
import { resolveImageUrl } from "@/services/image-storage";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";
import type { CanvasNodeData } from "@/types/canvas";
import type { DirectorScene } from "@/types/director";

export function CanvasDirectorNodePanel({ node, scene, readNodeContent, readNodeStorageKey, onOpen, professional = true }: { node: CanvasNodeData; scene: DirectorScene | null; readNodeContent: DirectorNodeContentReader; readNodeStorageKey?: DirectorNodeContentReader; onOpen: () => void; professional?: boolean }) {
    const theme = canvasThemes[useActiveTheme()];
    const shot = resolveDirectorActiveShot(scene, node.metadata?.directorShotId);
    // 记录「失败的那个 URL」而非布尔量：同一个坏 URL 不再反复渲染，换成另一个 URL 时自动重试。
    const [failedUrl, setFailedUrl] = useState<string | null>(null);
    const coverStorageKey = node.metadata?.directorCoverStorageKey || (!node.metadata?.directorCoverUrl ? readNodeStorageKey?.(node.metadata?.directorPreviewNodeId) || readNodeStorageKey?.(shot?.previewNodeId) : undefined);
    const [resolvedCover, setResolvedCover] = useState<{ key: string; url: string } | null>(null);
    useEffect(() => {
        if (!coverStorageKey) return;
        let live = true;
        void resolveImageUrl(coverStorageKey, node.metadata?.directorCoverUrl || "", { cacheMiss: true })
            .then((url) => { if (live && url) setResolvedCover({ key: coverStorageKey, url }); })
            .catch(() => { /* 旧预览或空态仍可使用。 */ });
        return () => { live = false; };
    }, [coverStorageKey, node.metadata?.directorCoverUrl]);
    const coverUrl = resolvedCover && resolvedCover.key === coverStorageKey ? resolvedCover.url : node.metadata?.directorCoverUrl;
    const preview = resolveDirectorPreviewSource({ scene, shot, coverUrl, failedUrl, previewNodeId: node.metadata?.directorPreviewNodeId, readNodeContent });

    return (
        <div className="relative flex h-full w-full min-h-0 items-center justify-center" style={{ color: theme.node.text }}>
            <div
                className="relative flex h-full w-full min-h-0 flex-1 items-center justify-center"
                data-director-open={preview.kind === "image" ? "preview" : "empty"}
            >
                {/* 卡片必须撑满节点外壳：连线算的是外壳边缘，卡片一旦被内容撑成小方块，
                    线头就会停在卡片外面，看上去像断线。 */}
                <div
                    className="relative h-full w-full overflow-hidden rounded-2xl border"
                    style={{ background: theme.node.fill, borderColor: theme.node.stroke }}
                >
                    {preview.kind === "image"
                        ? <img src={preview.url} alt={`${node.title} 导演台场景封面`} className="h-full w-full object-contain" draggable={false} onError={() => setFailedUrl(preview.url)} />
                        : <DirectorPreviewState
                            kind={preview.kind}
                            theme={theme}
                            action={<DirectorOpenAction theme={theme} professional={professional} onOpen={onOpen} />}
                        />}
                </div>
                {/* 封面态把入口浮在画面上；空态里的入口交给 DirectorPreviewState 排版，
                    否则居中的按钮会压住说明文字。 */}
                {preview.kind === "image" ? <DirectorOpenAction theme={theme} professional={professional} onOpen={onOpen} centered /> : null}
            </div>
        </div>
    );
}

/**
 * 诚实空态/准备态：不绘制地面、地平线、机位或任何伪 3D 物体。
 * 颜色全部走画布主题 token；层级靠字号/字重区分，不靠降低对比度。
 */
function DirectorPreviewState({ kind, theme, action }: { kind: "loading" | "empty"; theme: CanvasTheme; action?: ReactNode }) {
    const loading = kind === "loading";
    return (
        <div className="flex h-full w-full flex-col items-center justify-center gap-4 px-3 text-center">
            <Move3d className="size-7" style={{ color: theme.node.muted }} aria-hidden />
            <span className="text-xs leading-5" style={{ color: theme.node.muted }}>{loading ? "正在准备场景" : "在3D空间中搭建场景并进行多视角截图"}</span>
            {action}
        </div>
    );
}

/** 导演台入口按钮：封面态浮在画面中央，空态由说明文字下方承接。 */
function DirectorOpenAction({ theme, professional, onOpen, centered = false }: { theme: CanvasTheme; professional: boolean; onOpen: () => void; centered?: boolean }) {
    return (
        <button
            type="button"
            data-canvas-no-zoom
            className={`${centered ? "absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 " : ""}z-10 flex items-center gap-2 whitespace-nowrap rounded-xl px-6 py-4 text-base font-semibold shadow-lg transition hover:brightness-110 focus-visible:outline-none focus-visible:ring-2 disabled:cursor-default disabled:opacity-60`}
            style={{ background: theme.accent.primary, color: theme.accent.onPrimary, border: `1px solid ${theme.toolbar.border}` }}
            aria-label={professional ? "打开导演台" : "切换到专业模式后编辑导演台"}
            disabled={!professional}
            onMouseDown={(event) => event.stopPropagation()}
            onPointerDown={(event) => event.stopPropagation()}
            onClick={(event) => { event.stopPropagation(); onOpen(); }}
        >
            <Clapperboard className="size-8" aria-hidden />
            {professional ? "打开导演台" : "专业模式可编辑"}
        </button>
    );
}
