import { useMemo, useState } from "react";
import { ArrowRight, ArrowUp, ChevronDown, Sparkles } from "lucide-react";
import { Link } from "react-router";

import { creationFeaturedWorks, type CreationInspiration } from "@/pages/create/creation-inspirations";

/** 首页只做展示与跳转，标签单独定义，避免把创作页的运行时依赖拖进首页 chunk。 */
const inspirationModeLabels = { text: "文本", image: "图片", video: "视频" } as const;

const INITIAL_LIMIT = 8;
const PAGE_SIZE = 8;

type InspirationFilter = "all" | CreationInspiration["mode"];

const filters: InspirationFilter[] = ["all", "video", "image", "text"];

/**
 * 灵感卡要能一键复刻，因此把模式与提示词一起带进创作页。
 *
 * 用户点卡片是「我也想做一个这样的」，不是「我要去看这个页面」；不带提示词过去，
 * 到了创作页还得自己重新描述一遍，等于这块推荐只做了半件事。
 */
export function inspirationCreationPath(item: CreationInspiration): string {
    return `/create?mode=${item.mode}&prompt=${encodeURIComponent(item.prompt)}`;
}

function filterLabel(filter: InspirationFilter): string {
    return filter === "all" ? "全部灵感" : inspirationModeLabels[filter];
}

export function HomeInspirations() {
    const [filter, setFilter] = useState<InspirationFilter>("all");
    const [limit, setLimit] = useState(INITIAL_LIMIT);
    const visible = useMemo(() => creationFeaturedWorks.filter((item) => filter === "all" || item.mode === filter), [filter]);
    const counts = useMemo(
        () => new Map(filters.map((value) => [value, creationFeaturedWorks.filter((item) => value === "all" || item.mode === value).length])),
        [],
    );

    return (
        <section className="beeftv-home-section beeftv-inspirations" aria-labelledby="beeftv-inspirations-title">
            <header className="beeftv-section-heading">
                <h2 id="beeftv-inspirations-title">精选灵感</h2>
                <Link to="/create">去创作 <ArrowRight /></Link>
            </header>
            <div className="beeftv-inspiration-filters" role="group" aria-label="灵感类型">
                {filters.map((value) => (
                    <button
                        key={value}
                        type="button"
                        aria-pressed={filter === value}
                        onClick={() => {
                            setFilter(value);
                            setLimit(INITIAL_LIMIT);
                        }}
                    >
                        {filterLabel(value)}
                        <span>{counts.get(value)}</span>
                    </button>
                ))}
            </div>
            <div className="creation-featured-layout">
                {visible.slice(0, limit).map((item, index) => (
                    // 用链接而不是按钮：新标签页打开、复制链接都该能用，创作页也能直接吃下这两个参数。
                    <Link key={item.title} to={inspirationCreationPath(item)} className={`creation-featured-card ${index === 0 ? "is-featured-hero" : ""}`}>
                        <span className="creation-featured-media">
                            <img src={item.image} alt="" loading="lazy" />
                            <span className="beeftv-inspiration-overlay"><ArrowUp />使用这个创意</span>
                        </span>
                        <span className="creation-featured-copy">
                            <strong>{item.title}</strong>
                            <span>{item.description}</span>
                            <em><Sparkles />{item.source ? "开源改编 · CC0" : "原创提示词"} · {inspirationModeLabels[item.mode]}</em>
                        </span>
                    </Link>
                ))}
            </div>
            <footer className="beeftv-inspiration-footer">
                {limit < visible.length ? (
                    <button type="button" className="beeftv-inspiration-more" onClick={() => setLimit((current) => current + PAGE_SIZE)}>
                        展开更多灵感<ChevronDown />
                    </button>
                ) : <span>已展示全部 {visible.length} 个创意</span>}
            </footer>
        </section>
    );
}
