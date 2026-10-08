import type { ModelCapabilityConfig } from "@/lib/model-capabilities";
import type { ModelProtocolDefinition } from "@/lib/model-protocols";
import type { PublicAppearance } from "@/services/api/appearance";
import { ApiError, http } from "@/services/api/request";

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
    /**
     * 分档上游 SKU。resolution 与 videoSeconds 必须一起读回来并原样提交：
     * 服务端在收到空档位时会把它们重置成一条「任意分辨率 / 任意时长」的默认记录，
     * 而档位正是"这个模型卖哪几档"的定义，丢了不会报错，只会让模型少几档。
     */
    variants?: Array<{ id: string; selector?: Record<string, string>; resolution?: string; videoSeconds?: number; providerModelKey: string; enabled: boolean }>;
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

/**
 * 把读回来的档位换成提交用的形状。
 *
 * 更新已有模型走的是全量覆盖语义（PUT），所以每个调用方都必须把自己没编辑过的档位
 * 原样带回去——只提交被改动的字段会把其余字段清空。这里收敛成一个函数，避免两处
 * 调用点各自记得"要带上 variants"，而漏掉的那一处只会表现为档位悄悄消失。
 */
export function toChannelModelVariantInputs(model: AdminChannelModel): AdminChannelModelVariantInput[] {
    return (model.variants ?? []).map((variant) => ({
        selector: variant.selector,
        resolution: variant.resolution,
        videoSeconds: variant.videoSeconds,
        providerModelKey: variant.providerModelKey,
        enabled: variant.enabled,
    }));
}

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
    /** 积分账户：列表就带着余额，运营先看"还剩多少"，再去查明细。 */
    credit?: AdminCreditWallet;
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

export type AdminAgreementDocument = {
    type: string;
    title: string;
    body: string;
};

export type AdminAgreementVersion = {
    version: string;
    documents: AdminAgreementDocument[];
    publishedAt: string;
    publishedBy?: string;
    current: boolean;
};

export type AdminAgreements = {
    version: string;
    documents: AdminAgreementDocument[];
    /** false 表示还在用内置骨架正文，上线前必须替换。 */
    configured: boolean;
    publishedAt?: string;
    publishedBy?: string;
    /** 尚未同意当前版本的账号数：发布会把这些账号全部变成"需要重新同意"。 */
    pendingUsers: number;
    history: AdminAgreementVersion[];
};

export type AdminAgreementSignature = {
    id: string;
    userId: string;
    email: string;
    phone: string;
    name: string;
    agreementType: string;
    version: string;
    acceptedAt: string;
    ipAddress: string;
    userAgent: string;
};

export type AdminAgreementSignaturePage = {
    signatures: AdminAgreementSignature[];
    total: number;
    page: number;
    pageSize: number;
};

export function getAdminAgreements() {
    return http.get<AdminAgreements>("/admin/agreements");
}

/** 发布即强制重签：所有账号都需要在新版本上重新同意一次。 */
export function publishAdminAgreements(input: { termsTitle: string; termsBody: string; privacyTitle: string; privacyBody: string }) {
    return http.put<AdminAgreements>("/admin/agreements", input);
}

export function listAdminAgreementSignatures(options: { version?: string; type?: string; keyword?: string; page?: number; pageSize?: number } = {}) {
    return http.get<AdminAgreementSignaturePage>("/admin/agreements/signatures", {
        params: {
            version: options.version || undefined,
            type: options.type || undefined,
            keyword: options.keyword?.trim() || undefined,
            page: options.page,
            pageSize: options.pageSize,
        },
    });
}

export type AdminAppearanceSkinTheme = {
    id: string;
    name: string;
    description: string;
    /** 系统内置主题（经典黑白）不可修改，也不可删除。 */
    locked: boolean;
    tokens: Record<string, unknown>;
};

export type AdminAppearanceSetting = {
    schemaVersion: number;
    brandName: string;
    brandSlug: string;
    authHeroTitle: string;
    authHeroDescription: string;
    logoResourceId: string;
    darkLogoResourceId: string;
    logoFrameEnabled: boolean;
    authVideoResourceId: string;
    authVideoPosterResourceId: string;
    authVideoAutoplay: boolean;
    /** 充值支付弹窗收款二维码的资源 ID；留空表示弹窗里不展示二维码。 */
    paymentQrResourceId: string;
    skinId: string;
    skinThemes: AdminAppearanceSkinTheme[];
    seoTitle: string;
    seoDescription: string;
    seoKeywords: string;
    footerCopyright: string;
    icpFilingEnabled: boolean;
    icpFilingNumber: string;
    /** 前台实际拿到的投影，用来做保存后的实时预览。 */
    public: AdminAppearancePublic;
    configured: boolean;
    updatedBy?: string;
    createdAt?: string;
    updatedAt?: string;
};

/** 管理端读到的前台投影：结构与公开外观接口完全一致，可直接用于预览。 */
export type AdminAppearancePublic = PublicAppearance;

/** 保存请求只接受资源 ID，不接受裸 URL：站外地址不进首屏。 */
export type AdminAppearanceInput = Omit<AdminAppearanceSetting, "schemaVersion" | "public" | "configured" | "updatedBy" | "createdAt" | "updatedAt">;

export type AdminAppearanceAssetSlot = "logo" | "logo-dark" | "video" | "poster" | "payment-qr";

export function getAdminAppearance() {
    return http.get<AdminAppearanceSetting>("/admin/settings/appearance");
}

export function updateAdminAppearance(input: AdminAppearanceInput) {
    return http.patch<AdminAppearanceSetting>("/admin/settings/appearance", input);
}

export function resetAdminAppearance() {
    return http.delete<AdminAppearanceSetting>("/admin/settings/appearance");
}

/** 上传后返回资源 ID，保存外观时把这些 ID 回填进对应字段。 */
export async function uploadAdminAppearanceAsset(slot: AdminAppearanceAssetSlot, file: File) {
    const data = new FormData();
    data.append("file", file);
    // 不能手写 multipart Content-Type：boundary 由运行时生成，写死会让后端解析失败。
    const payload = await http.post<{ resource: { id: string } }>(`/admin/settings/appearance/assets/${slot}`, data);
    return payload.resource;
}

/** database = 后台配置；environment = 环境变量；console = 没有真实通道，验证码只写日志。 */
export type AdminGatewaySource = "database" | "environment" | "console";

export type AdminSMTPGateway = {
    host: string;
    port: number;
    username: string;
    /** 只写不读：读接口不回传密钥，留空保存表示保持原值。 */
    password?: string;
    hasPassword?: boolean;
    from: string;
    fromName: string;
};

export type AdminSMSGateway = {
    accessKeyId: string;
    accessKeySecret?: string;
    hasAccessKeySecret?: boolean;
    signName: string;
    templateCode: string;
    templateParamKey: string;
    regionId: string;
    endpoint: string;
};

export type AdminGatewayChannel = {
    channel: string;
    enabled: boolean;
    source: AdminGatewaySource;
    ready: boolean;
    detail: string;
    updatedAt?: string;
    updatedBy?: string;
    smtp?: AdminSMTPGateway;
    sms?: AdminSMSGateway;
};

export type AdminGateways = {
    smtp: AdminGatewayChannel;
    sms: AdminGatewayChannel;
};

export type AdminGatewayChannelKey = "SMTP" | "SMS";

export type AdminGatewayUpdateInput = {
    enabled: boolean;
    smtp?: AdminSMTPGateway;
    sms?: AdminSMSGateway;
};

export function getAdminGateways() {
    return http.get<AdminGateways>("/admin/gateways");
}

export function updateAdminGateway(channel: AdminGatewayChannelKey, input: AdminGatewayUpdateInput) {
    return http.put<AdminGateways>(`/admin/gateways/${channel}`, input);
}

/** 真发一条验证码，超时由服务端控制；返回值是要展示给运营的真实结果。 */
export function testAdminGateway(channel: AdminGatewayChannelKey, target: string) {
    return http.post<{ message: string }>(`/admin/gateways/${channel}/test`, { target });
}

// ---------- 计费：套餐 / 订单 / 优惠券 ----------
//
// 金额一律用"分"，格式化只在展示层做。这些接口与用户端 billing.ts 共用同一套后端
// 视图，字段名保持一致，避免两边各自解释一次金额与状态。

export type AdminBillingPlan = {
    id: string;
    code: string;
    name: string;
    description: string;
    sortOrder: number;
    enabled: boolean;
    priceFen: number;
    periodDays: number;
    /** 购买后到账的积分（分，不含赠送）。 */
    credits: number;
    /** 平台额外赠送的积分（分）。 */
    giftCredits: number;
    quotaCalls: number;
    quotaStorageMb: number;
    quotaMembers: number;
    createdAt: string;
    updatedAt: string;
};

export type AdminBillingPlanInput = {
    code: string;
    name: string;
    description: string;
    sortOrder: number;
    enabled: boolean;
    priceFen: number;
    periodDays: number;
    credits: number;
    giftCredits: number;
    quotaCalls: number;
    quotaStorageMb: number;
    quotaMembers: number;
};

export type AdminBillingOrder = {
    id: string;
    orderNo: string;
    userId: string;
    userName: string;
    userEmail: string;
    userPhone: string;
    planId: string;
    planCode: string;
    planName: string;
    amountFen: number;
    discountFen: number;
    payableFen: number;
    couponCode: string;
    status: "PENDING" | "PAID" | "CANCELED" | "REFUNDED" | "FAILED";
    provider: string;
    providerOrderNo: string;
    paidAt?: string;
    expiresAt: string;
    remark: string;
    createdAt: string;
};

export type AdminBillingRevenue = {
    paidOrders: number;
    paidAmountFen: number;
    pendingOrders: number;
    refundedFen: number;
};

export type AdminBillingOrderPage = {
    orders: AdminBillingOrder[];
    total: number;
    page: number;
    pageSize: number;
    /** 全量经营读数（不受当前筛选影响），后台顶部指标卡直接用这一份。 */
    revenue: AdminBillingRevenue;
};

export type AdminBillingCoupon = {
    id: string;
    code: string;
    name: string;
    kind: "AMOUNT" | "PERCENT";
    value: number;
    minAmountFen: number;
    totalQuota: number;
    usedCount: number;
    perUserLimit: number;
    startsAt: string;
    expiresAt: string;
    enabled: boolean;
    createdAt: string;
    updatedAt: string;
    remainingQuota?: number;
};

export type AdminBillingCouponInput = {
    code: string;
    name: string;
    kind: "AMOUNT" | "PERCENT";
    value: number;
    minAmountFen: number;
    totalQuota: number;
    perUserLimit: number;
    startsAt: string;
    expiresAt: string;
    enabled: boolean;
};

export type AdminCouponRedemption = {
    id: string;
    couponId: string;
    couponCode: string;
    userId: string;
    orderId: string;
    discountFen: number;
    redeemedAt: string;
};

export type AdminCouponRedemptionPage = {
    redemptions: AdminCouponRedemption[];
    total: number;
    page: number;
    pageSize: number;
};

export type AdminPaymentChannel = {
    channel: string;
    enabled: boolean;
    /** database = 后台配置；environment = 环境变量；console = 未配置（下单会失败）。 */
    source: string;
    ready: boolean;
    detail: string;
    updatedAt?: string;
    updatedBy?: string;
    /** 只写不读：配置里凡是密钥字段都不会回传，只给 hasSecret 标记。 */
    config: Record<string, string>;
    hasSecret: boolean;
};

export type AdminPaymentChannels = {
    channels: AdminPaymentChannel[];
};

export function listAdminBillingPlans() {
    return http.get<{ plans: AdminBillingPlan[] }>("/admin/plans");
}

export function createAdminBillingPlan(input: AdminBillingPlanInput) {
    return http.post<{ plan: AdminBillingPlan }>("/admin/plans", input);
}

export function updateAdminBillingPlan(id: string, input: AdminBillingPlanInput) {
    return http.put<{ plan: AdminBillingPlan }>(`/admin/plans/${encodeURIComponent(id)}`, input);
}

export function deleteAdminBillingPlan(id: string) {
    return http.delete<{ plans: AdminBillingPlan[] }>(`/admin/plans/${encodeURIComponent(id)}`);
}

export function listAdminBillingOrders(options: { status?: string; keyword?: string; planCode?: string; page?: number; pageSize?: number } = {}) {
    return http.get<AdminBillingOrderPage>("/admin/orders", {
        params: {
            status: options.status || undefined,
            keyword: options.keyword?.trim() || undefined,
            planCode: options.planCode || undefined,
            page: options.page,
            pageSize: options.pageSize,
        },
    });
}

/** 手工补单：渠道掉单时按真实到账记录补，不能靠改库。 */
export function markAdminBillingOrderPaid(id: string, remark: string) {
    return http.post<{ order: AdminBillingOrder }>(`/admin/orders/${encodeURIComponent(id)}/mark-paid`, { remark });
}

export function refundAdminBillingOrder(id: string, reason: string) {
    return http.post<{ order: AdminBillingOrder }>(`/admin/orders/${encodeURIComponent(id)}/refund`, { reason });
}

export function listAdminBillingCoupons() {
    return http.get<{ coupons: AdminBillingCoupon[] }>("/admin/coupons");
}

export function createAdminBillingCoupon(input: AdminBillingCouponInput) {
    return http.post<{ coupon: AdminBillingCoupon }>("/admin/coupons", input);
}

export function updateAdminBillingCoupon(id: string, input: AdminBillingCouponInput) {
    return http.put<{ coupon: AdminBillingCoupon }>(`/admin/coupons/${encodeURIComponent(id)}`, input);
}

export function deleteAdminBillingCoupon(id: string) {
    return http.delete<{ coupons: AdminBillingCoupon[] }>(`/admin/coupons/${encodeURIComponent(id)}`);
}

export function listAdminCouponRedemptions(couponId: string, options: { page?: number; pageSize?: number } = {}) {
    return http.get<AdminCouponRedemptionPage>(`/admin/coupons/${encodeURIComponent(couponId)}/redemptions`, {
        params: { page: options.page, pageSize: options.pageSize },
    });
}

export function listAdminPaymentChannels() {
    return http.get<AdminPaymentChannels>("/admin/billing/payment-channels");
}

export function updateAdminPaymentChannel(channel: string, input: { enabled: boolean; config: Record<string, string> }) {
    return http.put<AdminPaymentChannels>(`/admin/billing/payment-channels/${encodeURIComponent(channel)}`, input);
}

// ---------- 积分：账户 / 流水 / 手工调整 ----------
//
// 与用户端 credit.ts 同一口径：金额一律是整数「分」，而且这个整数就是积分本身，
// 展示层只做千分位格式化，不做元/分换算。换算一次就会在对账时丢一次精度，而积分
// 是本平台的现金等价物，对不上账的代价比少一个好看的小数点大得多。

export type AdminCreditAccount = {
    userId: string;
    name: string;
    username: string;
    email: string;
    phone: string;
    balance: number;
    /** 累计获得 / 累计消耗：只增不减，用于回答"这个号一共用了多少"。 */
    lifetimeIn: number;
    lifetimeOut: number;
    updatedAt: string;
};

export type AdminCreditAccountPage = {
    accounts: AdminCreditAccount[];
    total: number;
    page: number;
    pageSize: number;
};

export type AdminCreditKind = "TASK_CHARGE" | "TASK_REFUND" | "TASK_SETTLE" | "TOPUP" | "TOPUP_GIFT" | "ADMIN_ADJUST";

export type AdminCreditLedgerEntry = {
    id: string;
    kind: AdminCreditKind;
    /** 带符号：正数入账、负数出账。方向由符号承担，不依赖配色。 */
    amount: number;
    balanceAfter: number;
    /** 业务引用类型（TASK / ORDER），无外部单据时为 SELF。 */
    refType: string;
    refId: string;
    note: string;
    createdAt: string;
};

export type AdminCreditLedgerPage = {
    entries: AdminCreditLedgerEntry[];
    total: number;
    page: number;
    pageSize: number;
};

export type AdminCreditWallet = {
    userId: string;
    balance: number;
    lifetimeIn: number;
    lifetimeOut: number;
    updatedAt: string;
};

export function listAdminCreditAccounts(options: { keyword?: string; page?: number; pageSize?: number } = {}) {
    return http.get<AdminCreditAccountPage>("/admin/credits/accounts", {
        params: { keyword: options.keyword?.trim() || undefined, page: options.page, pageSize: options.pageSize },
    });
}

/** 后台流水必须点名账号：不带 userId 服务端会直接 400，所以这里把它设成必填。 */
export function listAdminCreditLedger(userId: string, options: { kind?: string; page?: number; pageSize?: number } = {}) {
    return http.get<AdminCreditLedgerPage>("/admin/credits/ledger", {
        params: { userId, kind: options.kind || undefined, page: options.page, pageSize: options.pageSize },
    });
}

/** 手工调整：amount 可正可负，note 即审计依据，服务端强制必填。 */
export function adjustAdminCredits(input: { userId: string; amount: number; note: string }) {
    return http.post<{ entry: AdminCreditLedgerEntry; wallet: AdminCreditWallet }>("/admin/credits/adjust", input);
}

/**
 * 读单个账号的积分账户。
 *
 * 用户管理的积分抽屉用它刷新余额：调整完一次、或者换一个人看，都不该逼运营回去
 * 翻一遍分页列表才能看到新的数。
 */
export function getAdminCreditAccount(userId: string) {
    return http.get<AdminCreditWallet>(`/admin/credits/accounts/${encodeURIComponent(userId)}`);
}

/**
 * 把余额扣成负数是 402 + reason=insufficient_credits。
 *
 * 这个分支必须在调用处单独认出来：否则它会被当成普通失败，运营看到的是"系统处理失败"，
 * 而真正的原因（"这个人只剩 3 分，扣不掉 100 分"）恰好是唯一需要被看见的信息。
 */
export function isInsufficientCreditsError(error: unknown) {
    if (!(error instanceof ApiError)) return false;
    return error.reason === "insufficient_credits" || error.code === 40201 || error.status === 402;
}
