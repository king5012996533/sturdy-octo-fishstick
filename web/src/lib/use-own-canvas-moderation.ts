import { useQuery } from "@tanstack/react-query";

import { getMyCanvasModeration, type OwnCanvasModeration } from "@/services/api/canvas-moderation";

export type OwnCanvasModerationIndex = {
    byId: Map<string, OwnCanvasModeration>;
    /** 托管形态下清单还没回来：此时既不能判定"没被下架"，也不能抢先打开画布。 */
    pending: boolean;
};

/**
 * 当前账号被处置过的画布，按画布 ID 索引。
 *
 * 只在托管形态请求：本地/桌面构建里没有这个接口，请求它会把打开画布拖成失败。
 */
export function useOwnCanvasModeration(): OwnCanvasModerationIndex {
    const query = useQuery({
        queryKey: ["canvas-moderation", "mine"],
        queryFn: ({ signal }) => getMyCanvasModeration(signal),
        enabled: __BEEFTV_HOSTED_AUTH__,
        // 处置是低频动作，画布之间来回切换时没必要每次都问一次。
        staleTime: 30_000,
    });
    const byId = new Map<string, OwnCanvasModeration>();
    for (const item of query.data?.canvases ?? []) byId.set(item.canvasId, item);
    return { byId, pending: __BEEFTV_HOSTED_AUTH__ && query.isPending };
}
