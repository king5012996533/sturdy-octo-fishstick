import { http } from "@/services/api/request";

/**
 * 用户投稿审核接口（/api/admin/posts）。
 *
 * 这一层只做「类型 + 路径」映射：通过与否、驳回理由怎么落库、上架状态怎么变，全部在
 * 服务端一处收敛。前端不复制一份状态机，否则「后端驳回但前端显示已通过」这类不一致
 * 迟早会出现。
 */

export type AdminCreationPost = {
    id: string;
    title: string;
    description: string;
    /** 平台签发的封面地址：带有效期，不能用它做长期缓存键。 */
    coverUrl: string;
    prompt: string;
    mode: string;
    category: string;
    author: string;
    status: string;
    origin: string;
    resourceId: string;
    reviewStatus: string;
    reviewNote: string;
    reviewedAt: string | null;
    reuseCount: number;
    createdAt: string;
    updatedAt: string;
};

/** 待人队列：只含 origin=USER 且 review_status=PENDING 的条目。 */
export function listAdminCreationPosts() {
    return http.get<{ posts: AdminCreationPost[] }>("/admin/posts");
}

export function approveAdminCreationPost(id: string) {
    return http.post<{ post: AdminCreationPost }>(`/admin/posts/${encodeURIComponent(id)}/approve`, {});
}

export function rejectAdminCreationPost(id: string, note: string) {
    return http.post<{ post: AdminCreationPost }>(`/admin/posts/${encodeURIComponent(id)}/reject`, { note });
}
