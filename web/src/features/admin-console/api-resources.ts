import { http } from "@/services/api/request";

/**
 * 生成产物对账接口（/api/admin/resources）。
 *
 * 读的是 resources 全量，而不是「素材管理」里的 assets：后者依赖客户端回写，产物没进
 * 素材库或画布时后台就完全看不见。对账要捞的正是这批「上游有结果、用户没拿到」的产物。
 */

export type AdminResource = {
    id: string;
    userId: string;
    userName: string;
    kind: string;
    status: string;
    provider: string;
    /** 存储里的相对路径，对账时用来跟磁盘目录一一对上。 */
    objectKey: string;
    mimeType: string;
    size: number;
    width: number;
    height: number;
    durationMs: number;
    playbackStatus: string;
    /** 是否被用户侧引用过（进了素材库或画布）；false 就是用户没拿到的那批。 */
    referenced: boolean;
    /** 现场签发的只读预览地址，12 小时后过期；签不出来时为空串。 */
    previewUrl: string;
    error?: string;
    createdAt: string;
    updatedAt: string;
};

export type AdminResourceTotals = {
    total: number;
    /** 未被任何素材库或画布引用的条数，口径恒为全量。 */
    unreferenced: number;
    totalBytes: number;
    users: number;
};

export type AdminResourcePage = {
    resources: AdminResource[];
    total: number;
    page: number;
    pageSize: number;
    totals: AdminResourceTotals;
};

export function listAdminResources(
    params: {
        keyword?: string;
        kind?: string;
        userId?: string;
        since?: string;
        until?: string;
        unreferenced?: boolean;
        page?: number;
        pageSize?: number;
    } = {},
) {
    return http.get<AdminResourcePage>("/admin/resources", {
        params: {
            keyword: params.keyword?.trim() || undefined,
            kind: params.kind || undefined,
            userId: params.userId || undefined,
            since: params.since || undefined,
            until: params.until || undefined,
            unreferenced: params.unreferenced ? "true" : undefined,
            page: params.page,
            pageSize: params.pageSize,
        },
    });
}
