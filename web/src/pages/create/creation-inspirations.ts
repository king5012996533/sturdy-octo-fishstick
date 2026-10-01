import type { CreationMode } from "./creation-types";
import type { CreationInspirationRecord } from "@/services/api/creation-inspirations";

/**
 * category / duration / tags 都是"卡片自带的展示字段"，缺一个就少渲染一块：
 *
 * - category 是卡片左上角的小标（题材），为空时前台回落到署名文案（示例素材 / 原创）；
 * - duration 是右下角的时长角标，图片与文本条目本来就没有时长，为空即不渲染；
 * - tags 只给首页主推荐用（参考页里主卡带一排题材标签，小卡不带），没有就整排不渲染。
 *
 * 三个字段全部可选，是为了让"库里还没填"和"这条内容天然没有"走同一条降级路径，
 * 不必区分数据迁移前后两种状态。
 */
export type CreationInspiration = { title: string; description: string; image: string; mode: CreationMode; prompt: string; featured?: boolean; source?: string; author?: string; likes?: number; sourceUrl?: string; category?: string; duration?: string; tags?: string[] };

const creationModes: CreationMode[] = ["text", "image", "video"];

/**
 * 把后台目录条目收敛成卡片数据；模式不认识或缺少必要字段时返回 null 由调用方丢弃。
 *
 * 这里必须做一次过滤而不是类型断言：后台的 mode 是自由字符串（接口层不锁定枚举，
 * 免得加一种模式就要同步发一次前端），断言会让一个错值直接落到卡片的渲染分支上，
 * 表现为点开卡片后什么都没发生。
 */
export function inspirationFromRecord(record: CreationInspirationRecord): CreationInspiration | null {
    const mode = creationModes.find((value) => value === record.mode);
    if (!mode) return null;
    if (!record.title || !record.coverUrl || !record.prompt) return null;
    return {
        title: record.title,
        description: record.description,
        image: record.coverUrl,
        mode,
        prompt: record.prompt,
        featured: record.featured,
        source: record.source || undefined,
        author: record.author || undefined,
        likes: record.likes || undefined,
        sourceUrl: record.sourceUrl || undefined,
        category: record.category || undefined,
        duration: record.duration || undefined,
        tags: record.tags?.length ? record.tags : undefined,
    };
}
