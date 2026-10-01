import { http } from "@/services/api/request";

/**
 * 用户投稿（/api/posts、/api/me/posts）。
 *
 * 投稿与广场目录（creation-inspirations.ts）刻意分成两份：目录是只读的公开数据，
 * 投稿是"我的"写操作，两者的失败语义完全不同——目录取不到可以回落本地列表，
 * 投稿失败必须让用户看见原因，混在一个模块里很容易把后者的错误也吞掉。
 *
 * 这里不传封面地址：封面由服务端按 resourceId 现场签发，客户端提交链接等于给广场
 * 开一个外链口子（可以指向任意域名，也可以事后换成别的内容）。
 */

/** 审核结论：平台条目恒为 APPROVED。 */
export type CreationPostReviewStatus = "APPROVED" | "PENDING" | "REJECTED";

export type CreationPostRecord = {
    id: string;
    title: string;
    description: string;
    coverUrl: string;
    prompt: string;
    mode: string;
    category: string;
    author: string;
    likes: number;
    status: string;
    featured: boolean;
    sortOrder: number;
    createdAt: string;
    updatedAt: string;
    origin: string;
    resourceId: string;
    reviewStatus: string;
    reviewNote: string;
    reviewedAt: string | null;
    reuseCount: number;
};

export type CreationPostInput = {
    /** 要发布的生成产物；必须是当前账号自己的、已就绪的图片或视频资源。 */
    resourceId: string;
    title: string;
    description?: string;
    /** 可复用的提示词：广场卡片被套用时落进创作框的就是它。 */
    prompt: string;
    mode: string;
    category?: string;
};

/** 提交一条投稿；返回的条目带上审核结论（可能已被关键词预筛直接驳回）。 */
export function publishCreationPost(input: CreationPostInput) {
    return http.post<{ post: CreationPostRecord }>("/posts", input);
}

/** 拉取自己的全部投稿（含待审与被驳回）。 */
export function listMyCreationPosts() {
    return http.get<{ posts: CreationPostRecord[] }>("/me/posts");
}

/** 撤回自己的投稿。 */
export function withdrawCreationPost(id: string) {
    return http.delete<{ posts: CreationPostRecord[] }>(`/me/posts/${encodeURIComponent(id)}`);
}
