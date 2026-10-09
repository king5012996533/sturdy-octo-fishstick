import { AnimatePresence, motion } from "motion/react";
import { X } from "lucide-react";

import { canvasThemes } from "@/lib/canvas-theme";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";

type CanvasEmptyViewportHintProps = {
    visible: boolean;
    onReturnToNodes: () => void;
    onDismiss?: () => void;
};

type CanvasEmptyViewportHintPillProps = {
    onReturnToNodes: () => void;
    onDismiss?: () => void;
};

// 提示本体：文案 + 两个按钮。它不碰动效，方便单独渲染和测试。
export function CanvasEmptyViewportHintPill({ onReturnToNodes, onDismiss }: CanvasEmptyViewportHintPillProps) {
    const theme = canvasThemes[useActiveTheme()];
    return (
        <div
            role="status"
            className="pointer-events-auto canvas-empty-viewport-hint inline-flex items-center gap-2 rounded-full border py-1 pl-3 pr-1 text-xs shadow-lg backdrop-blur-md"
            style={{ background: theme.spatial.elevated, borderColor: theme.toolbar.border, color: theme.node.text }}
        >
            <span>当前视窗没有节点</span>
            <button
                type="button"
                className="canvas-empty-viewport-hint-action rounded-full px-2.5 py-1 text-xs font-medium"
                style={{ background: theme.toolbar.itemHover, color: theme.node.text }}
                onClick={onReturnToNodes}
            >
                返回节点
            </button>
            {onDismiss ? (
                <button
                    type="button"
                    className="canvas-empty-viewport-hint-action rounded-full p-1"
                    aria-label="关闭提示"
                    onClick={onDismiss}
                >
                    <X className="size-3.5" />
                </button>
            ) : null}
        </div>
    );
}

// 视窗滑出内容时给一条"当前视窗没有节点 → 返回节点"的提示。
//
// 画布越稀疏，这条提示越有用：用户平移几下就滑进空白，很难判断是"这里本来就没东西"还是
// "我把内容弄丢了"。它是纯提示，不改变视图，只有按钮本身可点。
export function CanvasEmptyViewportHint({ visible, onReturnToNodes, onDismiss }: CanvasEmptyViewportHintProps) {
    return (
        <AnimatePresence>
            {visible ? (
                <motion.div
                    initial={{ opacity: 0, y: 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: 8 }}
                    transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
                    data-canvas-empty-viewport-hint
                    className="pointer-events-none absolute inset-x-0 bottom-[calc(var(--canvas-inset-y)+var(--space-16))] z-[var(--z-panel)] flex justify-center"
                >
                    <CanvasEmptyViewportHintPill onReturnToNodes={onReturnToNodes} onDismiss={onDismiss} />
                </motion.div>
            ) : null}
        </AnimatePresence>
    );
}
