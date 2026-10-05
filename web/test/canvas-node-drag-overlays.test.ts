import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, test } from "bun:test";

const projectSource = readFileSync(resolve(import.meta.dir, "../src/pages/canvas/project.tsx"), "utf8");
const selectionControllerSource = readFileSync(resolve(import.meta.dir, "../src/pages/canvas/use-canvas-selection-controller.ts"), "utf8");
const flat = (text: string) => text.replace(/\s+/g, " ");

describe("canvas node drag overlays", () => {
    test("keeps node composer panels mounted during drag while selection controls stay hidden", () => {
        expect(projectSource).toContain("const isCanvasNodeMoving = isNodeDragging || Boolean(dragPreview?.nodeIds.size);");
        // 节点下方的输入面板自己订阅拖拽预览跟手，拖拽期间不能卸载：卸载再挂载会让输入框
        // 在每次拖拽时闪出闪回。断言整段条件而不是某一行字面量，避免上游拆行或新增类型排除
        // （Panorama 等）后把排版变化报成契约失效。
        const flatSource = flat(projectSource);
        const overlayStart = flatSource.indexOf("{dialogNode &&");
        const overlayCondition = flatSource.slice(overlayStart, flatSource.indexOf("<CanvasNodePanelOverlay", overlayStart));
        expect(overlayCondition).toContain("dialogNode.type !== CanvasNodeType.Drawing");
        expect(overlayCondition).toContain("!selectionBox");
        expect(overlayCondition).not.toContain("isCanvasNodeMoving");
        expect(projectSource).toContain("angleNode?.metadata?.content ? (");
        expect(projectSource).toContain("dragOffset={dragPreview?.nodeIds.has(angleNode.id)");
        expect(projectSource).toContain("emotionNode?.metadata?.content ? (");
        // 选中态浮层仍然按 isCanvasNodeMoving 收起：它们靠选中包围盒定位，拖拽中跟着动只会打架。
        expect(projectSource).toContain("selectedNodeBounds && !selectionBox && !isCanvasNodeMoving");
        expect(projectSource).toContain(
            "node={assistantOpen || isCanvasNodeMoving || nodeImageSettingsOpen || annotationNodeId || maskEditNodeId || emotionNodeId || angleNodeId || (dialogNode && !isCanvasMediaResultNode(dialogNode)) || textEditorNodeId ? null : toolbarNode}",
        );
        expect(projectSource).toContain("onNodeDragEnd: handleNodeDragEnd");
        expect(projectSource).toContain("setDialogNodeId(node.id);");
        expect(selectionControllerSource).toContain("if (clickedNodeId) onNodeDragEnd?.(clickedNodeId);");
    });
});
