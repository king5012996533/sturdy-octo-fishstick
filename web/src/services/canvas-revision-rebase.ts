import { ApiError, http } from "@/services/api/request";
import { useCanvasStore, type CanvasProject } from "@/stores/canvas/use-canvas-store";

/**
 * 云端画布的版本冲突（服务端乐观锁拒绝覆盖）。
 *
 * 服务端对每次画布写入都要求请求里的 revision 等于库里当前值，否则 409。这个守卫本身是
 * 对的，问题是客户端以前只把错误丢进 console：本地 revision 停在旧值上，之后每次自动保存
 * 都会被拒；而本地 updatedAt 因为乐观写入一直在前进，会持续压过服务端的 updatedAt，连
 * 「取远端」的轮询也救不回来（轮询只在远端更新时才接管）。于是这份画布永久无法保存，
 * 用户看到的却是"本地已保存"。
 *
 * 触发条件不需要多特殊：同一账号在两台设备或两个标签页打开同一张画布就会撞上。
 */
const CANVAS_REVISION_CONFLICT_REASON = "canvas_revision_conflict";

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

/**
 * 把服务端当前 revision 收回到本地画布。
 *
 * 只改 revision——它是服务端持有的乐观锁，不是内容；本地草稿一个字节都不动，所以收敛完
 * 直接重试写入就是把用户手上这份内容提交上去。被覆盖的那一版由服务端留成历史快照
 * （见 canvas.SaveDocumentWithHistory），可以在「版本记录」里找回。
 *
 * 返回 false 表示远端已经不存在（例如画布被删）或读不到版本，这时不该盲目重试。
 */
export async function adoptRemoteCanvasRevision(id: string): Promise<boolean> {
    let remote: CanvasProject | undefined;
    try {
        const response = await http.get<{ project: CanvasProject }>(`/canvas-projects/${encodeURIComponent(id)}`);
        remote = response.project;
    } catch {
        return false;
    }
    const revision = remote?.revision;
    if (typeof revision !== "number") return false;
    useCanvasStore.setState((state) => ({
        projects: state.projects.map((project) => project.id === id ? { ...project, revision } : project),
    }));
    return true;
}
