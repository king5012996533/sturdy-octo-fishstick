import { useMemo, useState } from "react";

import { filterShowcaseModels, type ShowcaseCapability } from "./presentation";
import { ShowcaseCard } from "./showcase-card";
import { ShowcaseFilters } from "./showcase-filters";
import { ShowcaseEmptyState, ShowcaseErrorState, ShowcaseSkeletonGrid } from "./showcase-states";
import { ShowcaseShell } from "./showcase-shell";
import { useShowcaseModels } from "./use-showcase-models";
import { useShowcaseTitle } from "./use-showcase-title";

/**
 * 模型广场列表页（公开）。
 *
 * 整份目录一次取回，筛选与搜索在本地完成——目录是几十条量级，服务端筛选只会让每次
 * 输入都等一个来回。首次加载显示骨架；已有数据后的刷新（重试按钮）保留列表，不闪屏。
 */
export function ModelShowcaseListPage() {
    useShowcaseTitle("模型广场");
    const { models, loading, error, reload } = useShowcaseModels();
    const [capability, setCapability] = useState<ShowcaseCapability>("all");
    const [keyword, setKeyword] = useState("");

    const visible = useMemo(() => filterShowcaseModels(models ?? [], { capability, keyword }), [models, capability, keyword]);

    return (
        <ShowcaseShell>
            <section className="showcase-hero">
                <span className="showcase-eyebrow">Model Gallery</span>
                <h1>模型广场</h1>
                <p>平台已接入的生成模型。能力与参数来自创作台的同一份配置，价格与下单时实际扣费的同一套价目。</p>
            </section>

            {loading && !models ? <ShowcaseSkeletonGrid /> : null}

            {!loading && error && !models ? <ShowcaseErrorState message={error} onRetry={reload} /> : null}

            {models ? (
                <>
                    <ShowcaseFilters capability={capability} keyword={keyword} onCapabilityChange={setCapability} onKeywordChange={setKeyword} />
                    <p className="showcase-count">共 {visible.length} 个模型</p>
                    {visible.length ? (
                        <div className="showcase-grid">
                            {visible.map((model) => (
                                <ShowcaseCard key={`${model.slug}-${model.protocol}`} model={model} />
                            ))}
                        </div>
                    ) : (
                        <ShowcaseEmptyState
                            title="没有匹配的模型"
                            description="换个能力分组或关键词试试。也可能这些模型还在准备中，稍后再来看看。"
                            action={
                                <button
                                    type="button"
                                    className="showcase-retry"
                                    onClick={() => {
                                        setCapability("all");
                                        setKeyword("");
                                    }}
                                >
                                    清除筛选
                                </button>
                            }
                        />
                    )}
                </>
            ) : null}
        </ShowcaseShell>
    );
}
