import { http } from "@/services/api/request";

/**
 * 精选灵感广场目录（/api/inspirations）。
 *
 * 这一层只做「类型 + 路径」映射，不放展示判断：署名口径（示例素材 / 开源改编 / 原创）
 * 由页面按 sourceUrl 与 source 推导，服务端不在这里拼文案，否则同一句话会有前后端
 * 两处来源。返回的字段是纯数据，页面需要什么形状由页面自己映射。
 */

/** 创作模式与前台 CreationMode 同域，这里用字符串承接，由页面收敛成联合类型。 */
export type CreationInspirationRecord = {
    id: string;
    title: string;
    description: string;
    coverUrl: string;
    /** 成片的可播地址（上游 HLS 播放列表，没有 HLS 时是 mp4）；没有成片时为空串。 */
    videoUrl: string;
    prompt: string;
    mode: string;
    category: string;
    /** 卡片右下角的时长角标（形如 01:42）；图片与文本条目为空串。 */
    duration: string;
    /** 主推荐卡底部的题材标签排；小卡不使用。 */
    tags: string[];
    author: string;
    likes: number;
    sourceUrl: string;
    source: string;
    status: string;
    featured: boolean;
    sortOrder: number;
    createdAt: string;
    updatedAt: string;
};

/** 拉取前台可见的精选灵感（仅已上架）。 */
export function listCreationInspirations() {
    return http.get<{ inspirations: CreationInspirationRecord[] }>("/inspirations");
}
