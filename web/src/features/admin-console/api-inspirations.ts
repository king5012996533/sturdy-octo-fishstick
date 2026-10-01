import { http } from "@/services/api/request";

/**
 * 精选灵感广场运营接口（/api/admin/inspirations）。
 *
 * 这一层只做「类型 + 路径」映射，不放业务判断：标题长度、模式取值与上下架状态的校验
 * 都在服务端一处收敛，前端重复一份只会出现「前端放行、后端拒绝」的不一致。
 */

/** 灵感状态（冻结）：ONLINE 已上架 / OFFLINE 已下架。 */
export type CreationInspirationStatus = "ONLINE" | "OFFLINE";

/** 灵感对应的创作模式，与前台卡片上的筛选档一一对应。 */
export type CreationInspirationMode = "video" | "image" | "text";

export type AdminCreationInspiration = {
    id: string;
    title: string;
    description: string;
    coverUrl: string;
    prompt: string;
    mode: string;
    category: string;
    /** 卡片右下角的时长角标；图片与文本条目留空。 */
    duration: string;
    /** 主推荐卡底部的题材标签排；服务端按数组收发。 */
    tags: string[];
    author: string;
    likes: number;
    /** 原始作品链接：非空表示这是外部示例素材，前台据此显示「示例素材 · 作者」。 */
    sourceUrl: string;
    /** 提示词来源标注（如 awesome-chatgpt-prompts 的角色名）：非空表示开源改编。 */
    source: string;
    status: CreationInspirationStatus;
    featured: boolean;
    sortOrder: number;
    createdAt: string;
    updatedAt: string;
};

export type AdminCreationInspirationInput = {
    title: string;
    description: string;
    coverUrl: string;
    prompt: string;
    mode: CreationInspirationMode;
    category: string;
    duration: string;
    tags: string[];
    author: string;
    likes: number;
    sourceUrl: string;
    source: string;
    status: CreationInspirationStatus;
    featured: boolean;
    sortOrder: number;
};

export function listAdminCreationInspirations() {
    return http.get<{ inspirations: AdminCreationInspiration[] }>("/admin/inspirations");
}

export function createAdminCreationInspiration(input: AdminCreationInspirationInput) {
    return http.post<{ inspiration: AdminCreationInspiration }>("/admin/inspirations", input);
}

export function updateAdminCreationInspiration(id: string, input: AdminCreationInspirationInput) {
    return http.put<{ inspiration: AdminCreationInspiration }>(`/admin/inspirations/${encodeURIComponent(id)}`, input);
}

/** 删除后服务端直接回全量列表，前端不用再补一次 GET。 */
export function deleteAdminCreationInspiration(id: string) {
    return http.delete<{ inspirations: AdminCreationInspiration[] }>(`/admin/inspirations/${encodeURIComponent(id)}`);
}
