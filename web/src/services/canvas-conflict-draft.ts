import localforage from "localforage";

import { getActiveUserScope } from "@/lib/user-scope";
import type { CanvasProject } from "@/stores/canvas/use-canvas-store";

/**
 * 版本冲突时被云端顶下来的那份本地画布。
 *
 * 冲突策略是「云端为主」：本地不再覆盖服务端，但也不能把用户手里这份直接扔掉。
 * 每次冲突都把本地整份快照落到这里，用户可以在本机找回自己的那一版。
 * 只存本地：这份内容恰恰是服务端不认的，写到服务端等于又制造一次冲突。
 */

const store = localforage.createInstance({ name: "infinite-canvas", storeName: "canvas_conflict_drafts" });

export type CanvasConflictDraft = {
    id: string;
    title: string;
    savedAt: string;
    nodeCount: number;
    project: CanvasProject;
};

function draftKey(scope: string, id: string) {
    return `${scope}:${id}`;
}

export async function saveCanvasConflictDraft(project: CanvasProject, scope = getActiveUserScope()) {
    const draft: CanvasConflictDraft = {
        id: project.id,
        title: project.title || "未命名画布",
        savedAt: new Date().toISOString(),
        nodeCount: project.nodes?.length || 0,
        project,
    };
    await store.setItem(draftKey(scope, project.id), draft);
    return draft;
}

export async function readCanvasConflictDraft(id: string, scope = getActiveUserScope()): Promise<CanvasConflictDraft | null> {
    return store.getItem<CanvasConflictDraft>(draftKey(scope, id));
}

export async function listCanvasConflictDrafts(scope = getActiveUserScope()): Promise<CanvasConflictDraft[]> {
    const drafts: CanvasConflictDraft[] = [];
    await store.iterate<CanvasConflictDraft, void>((value, key) => {
        if (key.startsWith(`${scope}:`)) drafts.push(value);
    });
    return drafts.sort((left, right) => right.savedAt.localeCompare(left.savedAt));
}

export async function dropCanvasConflictDraft(id: string, scope = getActiveUserScope()) {
    await store.removeItem(draftKey(scope, id));
}
