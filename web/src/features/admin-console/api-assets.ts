import { http } from "@/services/api/request";

/**
 * 素材资源管理接口（/api/admin/assets）。
 *
 * 与画布审核不同：素材与审核状态同在画布库，列表一次就能把处置结论读回来，不需要
 * 前端再做状态换算。
 */

/** 处置状态：正常 / 已隐藏（前台不展示，可恢复）/ 已删除（判定违规，语义更重）。 */
export type AdminAssetModerationStatus = "NORMAL" | "HIDDEN" | "REMOVED";

export type AdminAsset = {
    id: string;
    userId: string;
    userName: string;
    kind: string;
    category: string;
    status: string;
    title: string;
    versionCount: number;
    /**
     * 占用近似值：payload_json 与各版本 definition_json 的字符长度之和。
     * 它不是对象存储真实用量（不做物理对象去重），只用于排序与概览。
     */
    payloadBytes: number;
    /**
     * 素材本体：assets 只存定义，本体是 payload 引用的 resources。没有可播本体时
     * 四个字段都为空——用「无预览」区分纯文本素材与引用失效，而不是留空白框。
     */
    resourceId?: string;
    /** 资源本体的媒体类型（image/video/audio），决定用图片还是播放器渲染。 */
    mediaKind?: string;
    mimeType?: string;
    /** 现场签发的只读预览地址，12 小时后过期；签不出来时为空串。 */
    previewUrl?: string;
    moderationStatus: AdminAssetModerationStatus;
    moderationReason?: string;
    createdAt: string;
    updatedAt: string;
};

export type AdminAssetTotals = {
    total: number;
    hidden: number;
    removed: number;
    /** 与 AdminAsset.payloadBytes 同口径的近似值。 */
    totalBytes: number;
    users: number;
};

export type AdminAssetPage = {
    assets: AdminAsset[];
    total: number;
    page: number;
    pageSize: number;
    totals: AdminAssetTotals;
};

export function listAdminAssets(
    params: { keyword?: string; status?: string; kind?: string; userId?: string; page?: number; pageSize?: number } = {},
) {
    return http.get<AdminAssetPage>("/admin/assets", {
        params: {
            keyword: params.keyword?.trim() || undefined,
            status: params.status || undefined,
            kind: params.kind || undefined,
            userId: params.userId || undefined,
            page: params.page,
            pageSize: params.pageSize,
        },
    });
}

export function getAdminAsset(id: string) {
    return http.get<{ asset: AdminAsset }>(`/admin/assets/${encodeURIComponent(id)}`);
}

/** 隐藏与删除必须带理由：没有理由的处置既无法向用户解释，也无法复盘。 */
export function moderateAdminAsset(id: string, input: { status: AdminAssetModerationStatus; reason: string }) {
    return http.post<{ asset: AdminAsset }>(`/admin/assets/${encodeURIComponent(id)}/moderation`, input);
}
