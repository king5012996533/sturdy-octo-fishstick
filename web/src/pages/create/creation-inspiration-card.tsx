import { ArrowUp, Play } from "lucide-react";

import type { CreationInspiration } from "./creation-inspirations";

/**
 * 精选灵感卡：整张图打底、文字压在图上。
 *
 * 单独成一个文件，是因为这张卡有三层信息（题材小标 / 标题与描述 / 时长角标）外加主推荐
 * 与小卡两种排布，继续塞进 workspace 只会让那份已经上千行的文件更难看懂；卡片只吃
 * item 与一个回调，不碰任何状态，是能独立阅读、独立修改的一块。
 */

/** 署名口径：带原始链接的是外部示例素材，带来源标注的是开源改编，两者都没有才是原创。 */
export function inspirationCredit(item: CreationInspiration) {
    if (item.sourceUrl) return item.author ? `示例素材 · ${item.author}` : "示例素材";
    return item.source ? "开源改编 · CC0" : "原创提示词";
}

/**
 * 卡片左上角的小标：填了题材就用题材，没填才回落成署名。
 *
 * 原型稿这里放的是「CINEMATIC」「情感 · 叙事」这类题材；题材是运营维护的内容分类，
 * 署名回答的是"这条内容从哪来"。两者只能占同一个位置，优先题材——署名在来源说明里
 * 还找得回来，题材一旦不显示就没有别的地方能看到了。
 */
function inspirationEyebrow(item: CreationInspiration) {
    return item.category || inspirationCredit(item);
}

/**
 * 有成片地址的条目，点击是"看这个作品"；没有地址的（图片条目、上游未转码）保持原来的
 * "使用这个创意"。整张卡仍然只是一个按钮：把播放器嵌进卡片会让广场一次挂出几十个
 * <video>，那是成片而不是封面，一条就是几百兆。
 */
export function CreationInspirationCard({ item, hero, onStart, onPlay }: { item: CreationInspiration; hero: boolean; onStart: () => void; onPlay?: () => void }) {
    const playable = Boolean(item.videoUrl && onPlay);
    return (
        <button type="button" className={`product-collection-card creation-featured-card ${hero ? "is-featured-hero" : ""}`} onClick={playable ? onPlay : onStart} aria-label={playable ? `播放作品《${item.title}》` : undefined}>
            <span className="creation-featured-media">
                <img src={item.image} alt="" loading="lazy" referrerPolicy={item.sourceUrl ? "no-referrer" : undefined} />
                <span className="creation-inspiration-overlay">{playable ? <><Play />播放作品</> : <><ArrowUp />使用这个创意</>}</span>
            </span>
            {/* 小标（em）在 DOM 里排在标题之后，靠 CSS 的 order 提到最上：这样无障碍读出来
                的是"标题 → 描述"，而不是先把分类念一遍。 */}
            <span className="creation-featured-copy">
                <strong>{item.title}</strong>
                <span>{item.description}</span>
                {/* 小标只放文字：这个位置是题材（「情感 · 叙事」），加图标会让它读成按钮或标签，
                    而它其实是标题的上一级说明。 */}
                <em>{inspirationEyebrow(item)}</em>
                {hero && item.tags?.length ? <span className="creation-featured-tags">{item.tags.map((tag) => <i key={tag}>{tag}</i>)}</span> : null}
            </span>
            {item.duration ? <span className="creation-featured-duration"><Play aria-hidden="true" />{item.duration}</span> : null}
        </button>
    );
}
