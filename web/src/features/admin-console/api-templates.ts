import { http } from "@/services/api/request";

/**
 * 画布模板运营接口（/api/admin/templates）。
 *
 * 这一层只做「类型 + 路径」映射，不放业务判断：标识字符集、名称长度与上下架状态
 * 的校验都在服务端一处收敛，前端重复一份只会出现「前端放行、后端拒绝」的不一致。
 */

/** 画布模板状态（冻结）：ONLINE 已上架 / OFFLINE 已下架。 */
export type CanvasTemplateStatus = "ONLINE" | "OFFLINE";

export type CanvasTemplate = {
    id: string;
    code: string;
    name: string;
    description: string;
    category: string;
    coverUrl: string;
    /** 画布快照内容，原样搬运，后台不做解析。 */
    payloadJson: string;
    status: CanvasTemplateStatus;
    featured: boolean;
    sortOrder: number;
    createdAt: string;
    updatedAt: string;
};

export type CanvasTemplateInput = {
    code: string;
    name: string;
    description: string;
    category: string;
    coverUrl: string;
    payloadJson: string;
    status: CanvasTemplateStatus;
    featured: boolean;
    sortOrder: number;
};

export function listAdminCanvasTemplates() {
    return http.get<{ templates: CanvasTemplate[] }>("/admin/templates");
}

export function createAdminCanvasTemplate(input: CanvasTemplateInput) {
    return http.post<{ template: CanvasTemplate }>("/admin/templates", input);
}

export function updateAdminCanvasTemplate(id: string, input: CanvasTemplateInput) {
    return http.put<{ template: CanvasTemplate }>(`/admin/templates/${encodeURIComponent(id)}`, input);
}

/** 删除后服务端直接回全量列表，前端不用再补一次 GET。 */
export function deleteAdminCanvasTemplate(id: string) {
    return http.delete<{ templates: CanvasTemplate[] }>(`/admin/templates/${encodeURIComponent(id)}`);
}
