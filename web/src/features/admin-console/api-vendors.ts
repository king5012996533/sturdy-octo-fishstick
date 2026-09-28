import { http } from "@/services/api/request";

import type { AdminChannelModel } from "./api";

/**
 * 模型厂商与凭证接口（/api/admin/vendors）。
 *
 * 与 api-rbac.ts / api-templates.ts 一样，这一层只做「类型 + 路径」映射：凭证能否连通、
 * 密钥格式对不对、内置厂商能不能删，全部由服务端判定；前端能做的只是把服务端返回的
 * 原因原样展示出来。
 *
 * 厂商（Vendor，平台级的上游品牌）与凭证（Credential，一个厂商下的一组 key + 接入地址）
 * 是两层：同一个厂商可以有主备两条线路，所以「测连通性」「拉模型」都落在凭证上，
 * 而不是厂商上。
 */

/** 内置厂商由平台预置、不可删除；自建厂商由运营手工接入。 */
export type VendorKind = "BUILTIN" | "CUSTOM";

export type Vendor = {
    id: string;
    code: string;
    name: string;
    kind: VendorKind;
    enabled: boolean;
    sortOrder: number;
    /** 能力枚举固定为 TEXT / IMAGE / VIDEO / AUDIO，服务端校验。 */
    capabilities: string[];
    protocols: string[];
    docsUrl: string;
    credentialCount: number;
    modelCount: number;
    createdAt: string;
    updatedAt: string;
};

/** 目录项是「可选厂商」的模板：新建厂商时 code/name/能力从这里带出，避免手抄拼错。 */
export type VendorCatalogItem = {
    code: string;
    name: string;
    capabilities: string[];
    protocols: string[];
    docsUrl: string;
};

export type Credential = {
    id: string;
    vendorId: string;
    name: string;
    channelId: string;
    /** key 尾号，用来区分同厂商的多条 key；原文永不下发到前端。 */
    keyHint: string;
    baseUrl: string;
    apiFormat: string;
    enabled: boolean;
    weight: number;
    modelCount: number;
    hasApiKey: boolean;
    hasSecretKey: boolean;
    /** 最近一次探测/调用的错误摘要，空串表示没有记录。 */
    lastError: string;
    lastCheckedAt: string;
    createdAt: string;
    updatedAt: string;
};

/**
 * 凭证下的模型直接复用既有渠道模型的投影：后端把 channel_models 按厂商/凭证维度
 * 折叠出来，字段名以 api.ts 的 AdminChannelModel 为准。这里再新造一个「差不多」的类型，
 * 只会在两处字段名漂移。
 */
export type VendorModel = AdminChannelModel;

export type VendorInput = {
    code: string;
    name: string;
    /** 缺省由服务端判定：目录内的 code 视为 BUILTIN，其余为 CUSTOM。 */
    kind?: VendorKind;
    capabilities?: string[];
    protocols?: string[];
    docsUrl?: string;
    sortOrder?: number;
    enabled?: boolean;
};

/** 更新不含 code / kind：标识与厂商归属是稳定标识，改名不改码。 */
export type VendorUpdateInput = Partial<Omit<VendorInput, "code" | "kind">>;

export type CredentialInput = {
    name: string;
    baseUrl: string;
    /**
     * 密钥只写不读：前端拿不到原文，因此编辑时留空表示保持原值，
     * 服务端也按「字段缺省即不修改」处理。
     */
    apiKey?: string;
    secretKey?: string;
    /** 并发上限，创建凭证时用于初始化底层渠道；缺省由服务端给默认值。 */
    concurrencyLimit?: number;
    enabled?: boolean;
    weight?: number;
    /** 初始模型标识，每行一个；服务端做去重与格式校验。 */
    models?: string[];
};

/**
 * 探测与导入是同步阻塞上游的：这里放宽到 45s，与 api.ts 的渠道探测保持一致，
 * 否则一次较慢的上游目录拉取会被默认 4s 超时打断，看起来像"凭证不可用"。
 */
const remoteProbeTimeoutMs = 45_000;

export function listAdminVendors() {
    return http.get<{ vendors: Vendor[] }>("/admin/vendors");
}

export function listAdminVendorCatalog() {
    return http.get<{ catalog: VendorCatalogItem[] }>("/admin/vendors/catalog");
}

export function createAdminVendor(input: VendorInput) {
    return http.post<{ vendor: Vendor }>("/admin/vendors", input);
}

export function updateAdminVendor(id: string, input: VendorUpdateInput) {
    return http.put<{ vendor: Vendor }>(`/admin/vendors/${encodeURIComponent(id)}`, input);
}

export function deleteAdminVendor(id: string) {
    return http.delete<{ deleted: true }>(`/admin/vendors/${encodeURIComponent(id)}`);
}

export function listAdminVendorCredentials(vendorId: string) {
    return http.get<{ credentials: Credential[] }>(`/admin/vendors/${encodeURIComponent(vendorId)}/credentials`);
}

export function createAdminVendorCredential(vendorId: string, input: CredentialInput) {
    return http.post<{ credential: Credential }>(`/admin/vendors/${encodeURIComponent(vendorId)}/credentials`, input);
}

export function updateAdminVendorCredential(vendorId: string, credentialId: string, input: CredentialInput) {
    return http.put<{ credential: Credential }>(
        `/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}`,
        input,
    );
}

export function deleteAdminVendorCredential(vendorId: string, credentialId: string) {
    return http.delete<{ deleted: true }>(`/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}`);
}

export function listAdminVendorCredentialModels(vendorId: string, credentialId: string) {
    return http.get<{ models: VendorModel[] }>(
        `/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}/models`,
    );
}

/** 导入前先探测一次：上游新增的模型只作为候选，管理员勾选后才写库。 */
export function importAdminVendorCredentialModels(vendorId: string, credentialId: string, models: string[]) {
    return http.post<{ added: number; models: VendorModel[] }>(
        `/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}/models/import`,
        { models },
        { timeout: remoteProbeTimeoutMs },
    );
}

/** 连通性探测在服务端执行，密钥不出库；前端只收回执（可用的模型标识列表）。 */
export function probeAdminVendorCredential(vendorId: string, credentialId: string) {
    return http.post<{ models: string[] }>(
        `/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}/probe`,
        {},
        { timeout: remoteProbeTimeoutMs },
    );
}
