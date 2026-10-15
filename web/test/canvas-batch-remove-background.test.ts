import { describe, expect, test } from "bun:test";

import { resolveToolbarEntries, type ToolContext, type ToolbarHandlers } from "@/lib/canvas/tool-registry";

/**
 * 批量抠图是多选工具栏上的一条命令，因此这里直接校验它在"什么时候出现、什么时候不能按"，
 * 而不是去渲染整块画布——出现条件错一次，用户拿到的就是一个永远失败的按钮。
 */
function selectionContext(patch: Partial<ToolContext> = {}): ToolContext {
    return {
        selectedCount: 2,
        selectedNodeTypes: new Set(),
        selectedVideoCount: 0,
        selectedImageCount: 2,
        canvasTool: "move",
        workspaceMode: "professional",
        isProjectLinked: false,
        canUndo: false,
        canRedo: false,
        extractingVideoFrames: false,
        extractingAudio: false,
        trimmingVideo: false,
        mergingVideos: false,
        batchRemovingBackground: false,
        addPanelOpen: false,
        appearancePanelOpen: false,
        settingsPanelOpen: false,
        handlers: {} as ToolbarHandlers,
        ...patch,
    };
}

function entry(ctx: ToolContext) {
    return resolveToolbarEntries("selection", ctx, null).find((item) => item.id === "selection-remove-background");
}

describe("canvas batch remove background", () => {
    test("only shows up when at least two image nodes are selected", () => {
        // 一张图的场景由节点工具栏上的单张入口负责，多选工具栏再摆一个就是重复。
        expect(entry(selectionContext({ selectedImageCount: 0 }))).toBeUndefined();
        expect(entry(selectionContext({ selectedImageCount: 1 }))).toBeUndefined();
        expect(entry(selectionContext({ selectedImageCount: 2 }))).toBeDefined();
    });

    test("counts the images it is about to charge for", () => {
        const command = entry(selectionContext({ selectedImageCount: 7 }));
        expect(command?.kind).toBe("command");
        expect(command?.kind === "command" ? command.label : "").toBe("批量移除背景（7）");
        expect(command?.kind === "command" ? command.disabled : true).toBe(false);
    });

    test("refuses a second click while the batch is running", () => {
        const command = entry(selectionContext({ selectedImageCount: 3, batchRemovingBackground: true }));
        expect(command?.kind === "command" ? command.disabled : false).toBe(true);
    });

    test("hands the click to the toolbar handler", () => {
        let runs = 0;
        const command = entry(selectionContext({ selectedImageCount: 2, handlers: { onBatchRemoveBackground: () => { runs += 1; } } as Partial<ToolbarHandlers> as ToolbarHandlers }));
        if (command?.kind !== "command") throw new Error("批量移除背景应当是一条命令");
        command.onClick({} as never);
        expect(runs).toBe(1);
    });
});
