import { uploadImage } from "@/services/image-storage";

import type { CreationAttachment } from "./creation-assets";
import type { CreationInspiration } from "./creation-inspirations";

/**
 * 复刻配方的搬运：把原作的参考图与参数搬到用户自己的这次创作里。
 *
 * 为什么需要它：上游的"模板"是整张画布，提示词只是其中一个文本节点。同一条提示词配上
 * 不同的参考图与模型是两个作品——库里那条《时尚服饰TVC》的提示词引用着 {{Portrait 1..4}}，
 * 只搬提示词就只能生成四张随机脸。所以"使用这个创意"必须把配方一起搬过来，否则那个按钮
 * 给的是一种错觉。
 */

/** 一次最多搬几张参考图，与后端 inspirationRecipeMaxImages 对齐。 */
export const creationRecipeMaxImages = 4;

export type CreationRecipePlan = {
    /** 原作使用的视频模型与模式：只用于如实告知，不参与我们这边的模型路由。 */
    sourceModel?: string;
    referenceImages: string[];
    ratio?: string;
    resolution?: string;
    seconds?: string;
};

/**
 * creationRecipePlan 读出一条灵感上"可以套用"的配方；没有任何配方信息时返回 null。
 *
 * 模型刻意不参与套用：原作用的是 star-video2 / kling 这类上游专有模型，本平台没有，
 * 硬把它写成我们的模型名只会让用户在模型选择器里看到一个不存在的值。所以这里只取
 * 参考图与时长比例这些跨模型可用的参数，模型名交给界面如实标注"原作使用 X"。
 */
export function creationRecipePlan(inspiration: Pick<CreationInspiration, "referenceImages" | "sourceVideoModel" | "sourceRatio" | "sourceResolution" | "sourceDurationSeconds">): CreationRecipePlan | null {
    const referenceImages = (inspiration.referenceImages || []).filter((url) => /^https?:\/\//i.test(url)).slice(0, creationRecipeMaxImages);
    const seconds = inspiration.sourceDurationSeconds && inspiration.sourceDurationSeconds > 0 ? String(inspiration.sourceDurationSeconds) : undefined;
    const ratio = inspiration.sourceRatio?.trim() || undefined;
    const resolution = inspiration.sourceResolution?.trim() || undefined;
    const sourceModel = inspiration.sourceVideoModel?.trim() || undefined;
    if (!referenceImages.length && !seconds && !ratio && !resolution && !sourceModel) return null;
    return { sourceModel, referenceImages, ratio, resolution, seconds };
}

/**
 * creationRecipeAttachments 把原作的参考图搬进用户自己的资源库并变成附件。
 *
 * 为什么必须搬而不是直接引用平台那条资源：生成请求只接受当前账号资源库里的素材
 * （后端会按 user_id 校验），平台资源对用户是只读的。搬过来之后，这些图就是用户自己的
 * 素材，可以被复用、也可以被删掉，语义与"用户自己上传的参考图"完全一致。
 *
 * 单张失败不影响其余：参考图少一张只是复刻得没那么像，让整个入口因此不可用才是更糟的
 * 结果。失败的张数由调用方决定怎么告知（见返回值里的 failed）。
 */
export async function creationRecipeAttachments(
    plan: CreationRecipePlan,
    options?: { upload?: typeof uploadImage; signal?: AbortSignal },
): Promise<{ attachments: CreationAttachment[]; failed: number }> {
    const upload = options?.upload || uploadImage;
    const attachments: CreationAttachment[] = [];
    let failed = 0;
    for (const [index, url] of plan.referenceImages.entries()) {
        if (options?.signal?.aborted) break;
        try {
            const response = await fetch(url, { signal: options?.signal });
            if (!response.ok) throw new Error(`参考图返回 ${response.status}`);
            const blob = await response.blob();
            const uploaded = await upload(blob);
            attachments.push(recipeAttachment(uploaded, index));
        } catch {
            failed += 1;
        }
    }
    return { attachments, failed };
}

/**
 * recipeAttachment 把一次上传结果组装成 composer 认得的附件。
 *
 * storageKey 必须是 resource: 前缀：生成提交那一侧据此判断"素材已经在账号资源库里"，
 * 没有这个前缀的会被按未落库的本地文件处理，进而要求用户重新上传一次。
 */
function recipeAttachment(uploaded: Awaited<ReturnType<typeof uploadImage>>, index: number): CreationAttachment {
    return {
        id: `inspiration-reference:${uploaded.storageKey}`,
        name: `原作参考图 ${index + 1}`,
        type: uploaded.mimeType || "image/jpeg",
        dataUrl: uploaded.url,
        url: uploaded.url,
        storageKey: uploaded.storageKey,
        bytes: uploaded.bytes,
        width: uploaded.width,
        height: uploaded.height,
        previewUrl: uploaded.url,
    };
}

/** creationRecipeSummary 给出一行"这次带过来了什么"，用于提示条与无障碍朗读。 */
export function creationRecipeSummary(plan: CreationRecipePlan): string {
    const parts: string[] = [];
    if (plan.referenceImages.length) parts.push(`${plan.referenceImages.length} 张参考图`);
    if (plan.seconds) parts.push(`${plan.seconds} 秒`);
    if (plan.ratio) parts.push(plan.ratio);
    if (plan.resolution) parts.push(plan.resolution);
    return parts.join(" · ");
}
