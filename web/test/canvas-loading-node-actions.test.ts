import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");

describe("canvas loading node actions", () => {
    test("exposes a cancel action only for queued or running tasks", () => {
        const content = readFileSync(resolve(root, "src/components/canvas/canvas-node-content.tsx"), "utf8");
        expect(content).toContain('aria-label="取消生成任务"');
        expect(content).toContain('(displayTask.status === "queued" || displayTask.status === "running")');
    });

    test("resolves node task ids through the page task list", () => {
        const project = readFileSync(resolve(root, "src/pages/canvas/project.tsx"), "utf8");
        expect(project).toContain("activeTasks.find((item) => item.id === target.metadata?.taskId)");
        expect(project).toContain("cancelCanvasTask(task)");
    });

    // 这些回调曾经直接写成 JSX 内联箭头函数，每次页面渲染都换新身份，透传给每个节点后
    // 节点的 memo 比较每拍失败：实测一次缩放下 72 个节点因此多渲染 8 次/节点。
    // 必须保持 useCallback 收敛，不能退回内联箭头。
    test("节点操作回调保持稳定引用，不退回内联箭头", () => {
        const project = readFileSync(resolve(root, "src/pages/canvas/project.tsx"), "utf8");
        const wiring: Array<[string, string]> = [
            ["onCancelTask", "cancelNodeTask"],
            ["onCancelImageCrop", "cancelImageCrop"],
            ["onConfirmImageCrop", "confirmImageCrop"],
            ["onCancelAnnotation", "cancelAnnotation"],
            ["onConfirmAnnotation", "confirmAnnotation"],
            ["onCancelMaskEdit", "cancelMaskEdit"],
            ["onConfirmMaskEdit", "confirmMaskEdit"],
            ["onCancelVideoCrop", "cancelVideoCrop"],
            ["onConfirmVideoCrop", "confirmVideoCrop"],
        ];
        for (const [prop, handler] of wiring) {
            expect(project).toContain(`${prop}={${handler}}`);
            expect(project).not.toContain(`${prop}={() =>`);
            expect(project).toContain(`const ${handler} = useCallback(`);
        }
    });
});
