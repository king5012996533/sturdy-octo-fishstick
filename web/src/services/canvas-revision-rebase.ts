import { ApiError, http } from "@/services/api/request";
import { saveCanvasConflictDraft } from "@/services/canvas-conflict-draft";
import { useCanvasStore, type CanvasProject } from "@/stores/canvas/use-canvas-store";

/**
 * 云端画布的版本冲突（服务端乐观锁拒绝覆盖）。
 *
 * 服务端对每次画布写入都要求请求里的 revision 等于库里当前值，否则 409。同一账号在
 * 两台设备或两个标签页打开同一张画布就会撞上。
 *
 * 冲突口径是「云端为主」：服务端那一版是权威版本，本地不再收敛 revision 重试覆盖，
 * 而是把云端内容接回本地，同时把本地这份另存为冲突草稿供用户找回。这样两台设备之间
 * 不会再互相盖掉对方——代价是本地这次改动需要用户主动取回，所以必须留下草稿并如实
 * 告知，不能假装保存成功。
 */
const CANVAS_REVISION_CONFLICT_REASON = "canvas_revision_conflict";

/**
 * 冲突已按云端为主处理完毕的信号。
 *
 * 与普通失败分开是因为上层有「失败就回滚本次 patch」的兜底：这里本地已经换成了云端
 * 内容，再拿旧内容回滚等于又把云端覆盖掉。调用方必须跳过回滚，只把消息透给用户。
 */
export class CanvasCloudAuthoritativeError extends Error {
    readonly canvasId: string;
    readonly draftSaved: boolean;

    constructor(canvasId: string, draftSaved: boolean) {
        super(draftSaved ? "云端画布已有更新，已采用云端版本；你本地这一版已另存为冲突草稿" : "云端画布已有更新，已采用云端版本");
        this.name = "CanvasCloudAuthoritativeError";
        this.canvasId = canvasId;
        this.draftSaved = draftSaved;
    }
}

export function isCanvasCloudAuthoritativeError(error: unknown): error is CanvasCloudAuthoritativeError {
    return error instanceof CanvasCloudAuthoritativeError;
}

/**
 * 判断这次失败是不是"版本落后"，而不是"这次请求本身有问题"。
 *
 * 优先认服务端的稳定 reason；老版本服务端只回笼统的 conflict，所以保留按文案的兜底，
 * 让新前端在灰度期间也能对上旧后端。
 */
export function isCanvasRevisionConflict(error: unknown): boolean {
    if (!(error instanceof ApiError) || error.status !== 409) return false;
    if (error.reason === CANVAS_REVISION_CONFLICT_REASON) return true;
    return /画布已有更新/.test(error.message || "");
}

export type CanvasCloudAdoption = {
    /** 是否真的把云端内容接回了本地。 */
    adopted: boolean;
    /** 本地那份是否成功留档；false 时用户手里这版已经无法找回，必须如实告知。 */
    draftSaved: boolean;
};

/**
 * 采用云端版本：本地这份先留档，再用服务端内容替换本地画布。
 *
 * adopted 为 false 表示远端已不存在（例如画布被删）或读不到版本——这时不能假装成功，
 * 调用方应把原始错误抛出去，而不是当作"已按云端处理"。
 */
export async function adoptRemoteCanvasProject(id: string): Promise<CanvasCloudAdoption> {
    const remote = await fetchRemoteCanvasProject(id);
    if (!remote) return { adopted: false, draftSaved: false };

    const local = useCanvasStore.getState().openProject(id);
    let draftSaved = false;
    if (local) {
        // 留档失败不该阻止采用云端版本，但必须如实反映出来。
        draftSaved = await saveCanvasConflictDraft(local).then(() => true).catch((error) => {
            console.warn("保存画布冲突草稿失败，本地版本未能留档", { id, error });
            return false;
        });
    }
    applyRemoteCanvasProject(remote, local);
    return { adopted: true, draftSaved };
}

async function fetchRemoteCanvasProject(id: string): Promise<CanvasProject | undefined> {
    try {
        const response = await http.get<{ project: CanvasProject }>(`/canvas-projects/${encodeURIComponent(id)}`);
        return typeof response.project?.revision === "number" ? response.project : undefined;
    } catch {
        return undefined;
    }
}

/**
 * 用云端内容替换本地画布。
 *
 * viewport 是纯查看状态、不属于文档内容，保留本地的可以让画布不跳位；
 * 画布不在当前 store 里时直接插入，保证「已采用云端版本」这句提示不是空话。
 */
function applyRemoteCanvasProject(remote: CanvasProject, local: CanvasProject | null) {
    useCanvasStore.setState((state) => {
        const exists = state.projects.some((project) => project.id === remote.id);
        const merged = { ...remote, viewport: local?.viewport ?? remote.viewport };
        return {
            projects: exists
                ? state.projects.map((project) => project.id === remote.id ? merged : project)
                : [...state.projects, merged],
        };
    });
}
