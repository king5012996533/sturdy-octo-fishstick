import { http } from "./request";

/**
 * 用户自己的画布处置清单（/api/canvas-moderation/mine）。
 *
 * 画布被平台下架后，单块读取会退化成 404 以免泄露别人的画布是否存在，用户因此
 * 只看到"画布不存在"，无法解释发生了什么。这个只读接口是那条提示的唯一来源。
 */
export type OwnCanvasModeration = {
    canvasId: string;
    status: "NORMAL" | "HIDDEN" | "REMOVED";
    reason?: string;
    updatedAt: string;
};

export function getMyCanvasModeration(signal?: AbortSignal) {
    return http.get<{ canvases: OwnCanvasModeration[] }>("/canvas-moderation/mine", { signal });
}
