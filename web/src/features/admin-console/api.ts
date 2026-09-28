import type { ModelCapabilityConfig } from "@/lib/model-capabilities";
import type { ModelProtocolDefinition } from "@/lib/model-protocols";
import { http } from "@/services/api/request";

/**
 * 平台运营后台接口（/api/admin/*）。
 *
 * 这一层只做「类型 + 路径」的映射，不放业务判断：渠道禁用、协议校验、密钥加密、
 * 审计全部在服务端一处收敛。前端能算的东西（例如按能力过滤协议）必须由服务端
 * 再算一次才算数，所以这里把服务端目录原样暴露出来而不是本地另建一份常量表。
 */

export type AdminChannelHeader = { name: string; value: string };

export type AdminChannelModelProfile = {
    model: string;
    displayName: string;
    icon: string;
    capability: string;
    protocol: string;
    capabilityConfig?: ModelCapabilityConfig;
};

export type AdminChannel = {
    id: string;
    userId: string;
    scope: string;
    enabled: boolean;
    name: string;
    publicAlias?: string;
    sortOrder: number;
    baseUrl: string;
    /** 管理视图只回显密钥本身给管理员编辑用；非管理员视图恒为空。 */
    apiKey: string;
    apiFormat: string;
    concurrencyLimit: number;
    models: string[];
    modelProfiles: AdminChannelModelProfile[];
    headers?: AdminChannelHeader[];
    hasApiKey: boolean;
    hasSecretKey: boolean;
    createdAt: string;
    updatedAt: string;
};

export type AdminChannelPage = {
    channels: AdminChannel[];
    total: number;
    page: number;
    pageSize: number;
};

export type AdminChannelModel = {
    id: string;
    channelId: string;
    modelKey: string;
    providerModelKey: string;
    displayName: string;
    sortOrder: number;
    icon: string;
    capability: string;
    protocol: string;
    enabled: boolean;
    capabilityVersion: number;
    capabilityConfig?: ModelCapabilityConfig;
    variants?: Array<{ id: string; selector?: Record<string, string>; providerModelKey: string; enabled: boolean }>;
    createdAt: string;
    updatedAt: string;
};

export type AdminChannelModelVariantInput = {
    selector?: Record<string, string>;
    resolution?: string;
    videoSeconds?: number;
    providerModelKey: string;
    enabled?: boolean;
};

export type AdminChannelModelInput = {
    modelKey: string;
    providerModelKey?: string;
    displayName?: string;
    icon?: string;
    capability: string;
    protocol: string;
    enabled?: boolean;
    capabilityConfig?: ModelCapabilityConfig;
    variants?: AdminChannelModelVariantInput[];
};

export type AdminChannelInput = {
    name: string;
    publicAlias?: string;
    sortOrder?: number;
    baseUrl: string;
    apiKey?: string;
    secretKey?: string;
    concurrencyLimit?: number;
    models?: string[];
    headers?: AdminChannelHeader[];
    enabled?: boolean;
};

export type AdminFeatureAvailability = {
    shortDramaEnabled: boolean;
    taskCenterEnabled: boolean;
    customChannelsEnabled: boolean;
    frontendModelsEnabled: boolean;
    pluginCenterEnabled: boolean;
    systemPluginsVisibleToUsers: boolean;
    timelineTranscriptionEnabled: boolean;
    configured?: boolean;
    updatedBy?: string;
    updatedAt?: string;
};

export type AdminRuntimePolicySetting = {
    resource: Record<string, number>;
    task: Record<string, number>;
    request: Record<string, number>;
};

export type AdminProtocolCatalogEntry = {
    id: string;
    version: string;
    name: string;
    vendor: string;
    categories: string[];
    scopes: string[];
    create?: string;
    poll?: string;
    contentType?: string;
    baseUrl?: string;
    enabled: boolean;
    unavailableReason?: string;
};

export type AdminOrderItem = { id: string; name: string; enabled: boolean };

/** 上游拉取与连通性测试要走真实网络，远超默认 4s，必须按请求放宽超时。 */
const remoteProbeTimeoutMs = 45_000;

export function listAdminChannels(options: { keyword?: string; status?: string; page?: number; pageSize?: number } = {}) {
    return http.get<AdminChannelPage>("/admin/channels", {
        params: {
            keyword: options.keyword?.trim() || undefined,
            status: options.status || undefined,
            page: options.page,
            pageSize: options.pageSize,
        },
    });
}

export function getAdminChannel(id: string) {
    return http.get<AdminChannel>(`/admin/channels/${encodeURIComponent(id)}`);
}

export function createAdminChannel(input: AdminChannelInput) {
    return http.post<AdminChannel>("/admin/channels", input);
}

export function updateAdminChannel(id: string, input: AdminChannelInput) {
    return http.put<AdminChannel>(`/admin/channels/${encodeURIComponent(id)}`, input);
}

export function deleteAdminChannel(id: string) {
    return http.delete<{ deleted: boolean }>(`/admin/channels/${encodeURIComponent(id)}`);
}

export function duplicateAdminChannel(id: string) {
    return http.post<AdminChannel>(`/admin/channels/${encodeURIComponent(id)}/duplicate`, {});
}

export function listAdminChannelModels(channelId: string) {
    return http.get<{ models: AdminChannelModel[] }>(`/admin/channels/${encodeURIComponent(channelId)}/models`);
}

export function createAdminChannelModel(channelId: string, input: AdminChannelModelInput) {
    return http.post<AdminChannelModel>(`/admin/channels/${encodeURIComponent(channelId)}/models`, input);
}

export function updateAdminChannelModel(channelId: string, modelId: string, input: AdminChannelModelInput) {
    return http.put<AdminChannelModel>(`/admin/channels/${encodeURIComponent(channelId)}/models/${encodeURIComponent(modelId)}`, input);
}

export function deleteAdminChannelModel(channelId: string, modelId: string) {
    return http.delete<{ deleted: boolean }>(`/admin/channels/${encodeURIComponent(channelId)}/models/${encodeURIComponent(modelId)}`);
}

export function batchDeleteAdminChannelModels(channelId: string, ids: string[]) {
    return http.post<{ deleted: number }>(`/admin/channels/${encodeURIComponent(channelId)}/models/batch-delete`, { ids });
}

/** 上游目录只读预览：管理员挑完再 import，避免一次拉取就把未定价的 SKU 写进库。 */
export function previewAdminChannelUpstreamModels(channelId: string) {
    return http.get<{ models: string[] }>(`/admin/channels/${encodeURIComponent(channelId)}/models/upstream`, { timeout: remoteProbeTimeoutMs });
}

export function importAdminChannelUpstreamModels(channelId: string, models: string[]) {
    return http.post<{ models: string[]; added: number }>(`/admin/channels/${encodeURIComponent(channelId)}/models/import`, { models }, { timeout: remoteProbeTimeoutMs });
}

/** 连通性测试在服务端执行，密钥不出库；前端只提交待测配置并收回执。 */
export function testAdminChannelModel(channelId: string, input: AdminChannelModelInput) {
    return http.post<{ durationMs: number }>(`/admin/channels/${encodeURIComponent(channelId)}/models/test`, input, { timeout: remoteProbeTimeoutMs });
}

export function fetchAdminProtocols(capability?: string) {
    return http.get<{ providers: AdminProtocolCatalogEntry[] }>("/admin/protocols", { params: { capability: capability || undefined } });
}

/** 服务端目录 → 前端模型编辑器共用的协议定义形状，避免两处各自解释字段。 */
export function toProtocolDefinition(entry: AdminProtocolCatalogEntry): ModelProtocolDefinition {
    return {
        value: entry.id,
        label: entry.name,
        vendor: entry.vendor,
        capability: (entry.categories[0] || "text") as ModelProtocolDefinition["capability"],
        create: entry.create || "",
        poll: entry.poll,
        contentType: entry.contentType || "application/json",
        media: `${entry.vendor} · ${entry.version}`,
        enabled: entry.enabled && !entry.unavailableReason,
        baseUrl: entry.baseUrl,
    };
}

export function getAdminFeatures() {
    return http.get<AdminFeatureAvailability>("/admin/features");
}

export function updateAdminFeatures(input: AdminFeatureAvailability) {
    return http.put<AdminFeatureAvailability>("/admin/features", input);
}

export function getAdminRuntimePolicy() {
    return http.get<AdminRuntimePolicySetting>("/admin/runtime-policy");
}

export function updateAdminRuntimePolicy(input: AdminRuntimePolicySetting) {
    return http.put<AdminRuntimePolicySetting>("/admin/runtime-policy", input);
}

export function resetAdminRuntimePolicy() {
    return http.delete<AdminRuntimePolicySetting>("/admin/runtime-policy");
}

export function getAdminChannelOrder() {
    return http.get<{ items: AdminOrderItem[] }>("/admin/order");
}

/** 仪表盘：账号计数与画布/调用读数合并后的结果。 */
export type AdminUserCounts = {
    total: number;
    active: number;
    disabled: number;
    admins: number;
    newUsers: number;
};

export type AdminTrendPoint = {
    day: string;
    calls: number;
    failedCalls: number;
    inputTokens: number;
    outputTokens: number;
};

export type AdminOverview = {
    users: AdminUserCounts;
    canvases: number;
    activeCanvases: number;
    assets: number;
    storedBytes: number;
    calls: number;
    failedCalls: number;
    inputTokens: number;
    outputTokens: number;
    channels: number;
    enabledChannels: number;
    models: number;
    enabledModels: number;
    trend: AdminTrendPoint[];
    days: number;
    generatedAt: string;
};

export type AdminUser = {
    id: string;
    username: string;
    name: string;
    email: string;
    phone: string;
    avatarUrl: string;
    /** 取值与账号库对齐（CanvasMind 的 Prisma enum 为大写）。 */
    role: "USER" | "ADMIN";
    status: "ACTIVE" | "DISABLED";
    createdAt: string;
    updatedAt: string;
    lastActiveAt?: string;
    /** 作品数来自画布库；统计失败时服务端退化为 0。 */
    canvases: number;
};

export type AdminUserPage = {
    users: AdminUser[];
    total: number;
    page: number;
    pageSize: number;
};

export type AdminLoginMethod = {
    methodType: string;
    category: string;
    displayName: string;
    description: string;
    sortOrder: number;
    isEnabled: boolean;
    isVisible: boolean;
    allowSignUp: boolean;
    allowAutoFill: boolean;
    /** 凭据是否齐备；未就绪时开启也不会生效。 */
    ready: boolean;
    unavailableReason?: string;
};

export type AdminAuditEvent = {
    id: string;
    actorUserId: string;
    action: string;
    targetType: string;
    targetId: string;
    summary: string;
    metadataJson: string;
    createdAt: string;
};

export type AdminAuditEventPage = {
    events: AdminAuditEvent[];
    total: number;
    page: number;
    pageSize: number;
};

export function getAdminOverview(days = 7) {
    return http.get<AdminOverview>("/admin/analytics/overview", { params: { days } });
}

export function listAdminUsers(options: { keyword?: string; status?: string; role?: string; page?: number; pageSize?: number } = {}) {
    return http.get<AdminUserPage>("/admin/users", {
        params: {
            keyword: options.keyword?.trim() || undefined,
            status: options.status || undefined,
            role: options.role || undefined,
            page: options.page,
            pageSize: options.pageSize,
        },
    });
}

export function updateAdminUserStatus(id: string, status: AdminUser["status"]) {
    return http.patch<{ updated: boolean; status: AdminUser["status"] }>(`/admin/users/${encodeURIComponent(id)}/status`, { status });
}

export function updateAdminUserRole(id: string, role: AdminUser["role"]) {
    return http.patch<{ updated: boolean; role: AdminUser["role"] }>(`/admin/users/${encodeURIComponent(id)}/role`, { role });
}

/** 重置密码后账号会被强制下线，旧会话不再可用。 */
export function resetAdminUserPassword(id: string, password: string) {
    return http.post<{ updated: boolean }>(`/admin/users/${encodeURIComponent(id)}/password`, { password });
}

export function forceAdminUserLogout(id: string) {
    return http.post<{ revokedSessions: number }>(`/admin/users/${encodeURIComponent(id)}/logout`, {});
}

export function listAdminLoginMethods() {
    return http.get<{ methods: AdminLoginMethod[] }>("/admin/login-methods");
}

export function updateAdminLoginMethod(methodType: string, input: { isEnabled?: boolean; isVisible?: boolean; allowSignUp?: boolean }) {
    return http.patch<AdminLoginMethod>(`/admin/login-methods/${encodeURIComponent(methodType)}`, input);
}

export function listAdminAuditEvents(options: { page?: number; pageSize?: number } = {}) {
    return http.get<AdminAuditEventPage>("/admin/audit-events", { params: { page: options.page, pageSize: options.pageSize } });
}

/**
 * 保存渠道顺序。
 *
 * 服务端要求同时提交 "期望看到的顺序"（expectedIds）做乐观并发：两个管理员同时拖动时，
 * 后提交的一方会拿到 409 而不是静默覆盖对方的排序。
 */
export function saveAdminChannelOrder(ids: string[], expectedIds: string[]) {
    return http.put<{ saved: boolean }>("/admin/order", { ids, expectedIds });
}

/** 内容审核状态：正常 / 已下架（前台不可见、用户不可读写）/ 已移除（视为删除）。 */
export type AdminCanvasModerationStatus = "NORMAL" | "HIDDEN" | "REMOVED";

export type AdminCanvas = {
    id: string;
    userId: string;
    projectId?: string;
    title: string;
    revision: number;
    /** 列表不返回画布正文，只回体积，避免审核列表把整库画布拖进浏览器。 */
    payloadBytes: number;
    createdAt: string;
    updatedAt: string;
    moderationStatus: AdminCanvasModerationStatus;
    moderationReason?: string;
    moderatedAt?: string;
    moderatedBy?: string;
    ownerName: string;
    ownerEmail: string;
    ownerPhone: string;
};

export type AdminCanvasPage = {
    canvases: AdminCanvas[];
    total: number;
    page: number;
    pageSize: number;
};

export type AdminCanvasNodeBrief = {
    id: string;
    type: string;
    title: string;
    content?: string;
};

export type AdminCanvasDetail = AdminCanvas & {
    nodeCount: number;
    connectionCount: number;
    nodeKinds: Record<string, number>;
    nodes: AdminCanvasNodeBrief[];
    nodesTruncated: boolean;
};

export function listAdminCanvases(options: { keyword?: string; userId?: string; status?: string; page?: number; pageSize?: number } = {}) {
    return http.get<AdminCanvasPage>("/admin/canvases", {
        params: {
            keyword: options.keyword?.trim() || undefined,
            userId: options.userId || undefined,
            status: options.status || undefined,
            page: options.page,
            pageSize: options.pageSize,
        },
    });
}

export function getAdminCanvas(id: string) {
    return http.get<AdminCanvasDetail>(`/admin/canvases/${encodeURIComponent(id)}`);
}

/** 下架与移除必须带理由：没有理由的处置既无法向用户解释，也无法复盘。 */
export function updateAdminCanvasModeration(id: string, status: AdminCanvasModerationStatus, reason: string) {
    return http.patch<AdminCanvas>(`/admin/canvases/${encodeURIComponent(id)}/moderation`, { status, reason });
}
