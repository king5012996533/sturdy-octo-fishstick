import { http } from "@/services/api/request";

/**
 * 生成产物对账接口（/api/admin/resources）。
 *
 * 读的是 resources 全量，而不是「素材管理」里的 assets：后者依赖客户端回写，产物没进
 * 素材库或画布时后台就完全看不见。对账要捞的正是这批「上游有结果、用户没拿到」的产物。
 */

/** 扣费状态：未关联任务 / 计费上线前 / 已扣费 / 计费后仍无扣费（漏单）。 */
export type AdminResourceChargeState = "untracked" | "prebilling" | "charged" | "uncharged";

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
    /** 产出这条产物的任务；空表示用户上传，或回填后仍对不上的历史数据。 */
    taskId?: string;
    /** 来路：generation / upload / import / render / legacy。 */
    source?: string;
    taskType?: string;
    taskStatus?: string;
    taskModel?: string;
    providerRequestId?: string;
    /** 该任务的净扣费（扣费减退回），0 表示没扣或已全额退回。 */
    chargedCredits: number;
    chargeState: AdminResourceChargeState;
    error?: string;
    createdAt: string;
    updatedAt: string;
};

export type AdminResourceTotals = {
    total: number;
    /** 未被任何素材库或画布引用的条数，口径恒为全量。 */
    unreferenced: number;
    /** 没有关联任务的产物数：上传的素材与回填后仍对不上的历史数据。 */
    untracked: number;
    totalBytes: number;
    users: number;
};

/** 扣了费、任务也跑了，却一条产物都没有的媒体任务。 */
export type AdminTaskCharge = {
    taskId: string;
    userId: string;
    userName: string;
    net: number;
    chargedAt: string;
    taskType?: string;
    taskStatus?: string;
    taskError?: string;
};

export type AdminResourceReconciliation = {
    /** 空串表示账号库里还没有任何扣费，此时不做漏单判定。 */
    billingStart?: string;
    untracked: number;
    uncharged: number;
    chargedWithoutResource: number;
    unchargedResources: AdminResource[];
    chargedTasks: AdminTaskCharge[];
};

export type AdminResourceBackfillResult = {
    tasksScanned: number;
    resourcesMissing: number;
    linked: number;
    unmatched: number;
    applied: boolean;
};

export type AdminResourcePage = {
    resources: AdminResource[];
    total: number;
    page: number;
    pageSize: number;
    totals: AdminResourceTotals;
    reconciliation?: AdminResourceReconciliation;
};

export function listAdminResources(
    params: {
        keyword?: string;
        kind?: string;
        userId?: string;
        since?: string;
        until?: string;
        unreferenced?: boolean;
        untracked?: boolean;
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
            untracked: params.untracked ? "true" : undefined,
            page: params.page,
            pageSize: params.pageSize,
        },
    });
}

/**
 * 把历史产物关联回它们的生成任务。
 *
 * dryRun=true 只回报"能对上多少"，不写库：回填改的是全部历史数据的方向，
 * 先演练一次再确认，比写完了发现口径不对便宜得多。可重复执行，只补空的 task_id。
 */
export function backfillAdminResourceProvenance(dryRun: boolean) {
    return http.post<{ result: AdminResourceBackfillResult }>(
        `/admin/resources/backfill${dryRun ? "?dryRun=true" : ""}`,
        {},
    );
}
