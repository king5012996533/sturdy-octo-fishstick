import { Link, useParams } from "react-router";
import { ArrowLeft, Check } from "lucide-react";

import { capabilityLabel, detailLead } from "./presentation";
import { ShowcaseModelLogo } from "./showcase-model-logo";
import { ShowcasePriceTable } from "./price-table";
import { ShowcaseSpecTable } from "./spec-table";
import { ShowcaseEmptyState, ShowcaseErrorState, ShowcaseSkeletonGrid } from "./showcase-states";
import { ShowcaseShell } from "./showcase-shell";
import { useShowcaseModel } from "./use-showcase-models";
import { useShowcaseTitle } from "./use-showcase-title";

/**
 * 模型广场详情页（公开）。
 *
 * 顺序按用户的心智排：这是什么（名称 / 定位 / 摘要）→ 我能用它做什么（参数）→ 多少钱（价格）
 * → 去用（唯一主按钮）。模型不存在与加载失败分开呈现：前者要引导回列表，后者要能重试。
 */
export function ModelShowcaseDetailPage() {
    const slug = normalizeSlug(useParams()["*"]);
    const { model, loading, error, reload } = useShowcaseModel(slug);

    useShowcaseTitle(model ? `${model.displayName} · 模型广场` : "模型广场");

    return (
        <ShowcaseShell>
            <Link to="/models" className="showcase-back">
                <ArrowLeft className="size-4" aria-hidden />
                返回模型广场
            </Link>

            {loading && !model ? <ShowcaseSkeletonGrid count={3} /> : null}

            {!loading && error ? <ShowcaseErrorState message={error} onRetry={reload} /> : null}

            {!loading && !error && !model ? (
                <ShowcaseEmptyState
                    title="模型不存在"
                    description="这个模型可能已经下架，或者链接里的标识不完整。回列表看看当前在售的全部模型。"
                    action={
                        <Link to="/models" className="showcase-retry">
                            回到模型广场
                        </Link>
                    }
                />
            ) : null}

            {model ? (
                <>
                    <section className="showcase-detail-head">
                        <div className="showcase-detail-title">
                            <span className="showcase-detail-mark">
                                <ShowcaseModelLogo icon={model.icon} name={model.displayName} slug={model.slug} size={30} />
                            </span>
                            <div>
                                <h1>{model.displayName}</h1>
                                <p>{model.slug}</p>
                            </div>
                        </div>
                        <p className="showcase-lead">{detailLead(model)}</p>
                        <div className="showcase-actions">
                            <Link to="/" className="showcase-primary">
                                去创作台使用
                            </Link>
                            <span className="showcase-tag">{capabilityLabel(model.capability)}</span>
                            {/* 刻意不放"查看上游"外链：广场是站内的获客页，把用户送去 Replicate
                                等于替上游做导流。上游地址仍旧存在 sourceUrl 里，那是后台的溯源字段，
                                不进前台。 */}
                        </div>
                    </section>

                    <div className="showcase-detail-body">
                        <div>
                            {model.summary && model.summary !== model.tagline ? (
                                <section className="showcase-panel">
                                    <h2 className="showcase-panel-title">模型简介</h2>
                                    <p className="showcase-lead">{model.summary}</p>
                                </section>
                            ) : null}

                            {model.highlights.length ? (
                                <section className="showcase-panel">
                                    <h2 className="showcase-panel-title">能力亮点</h2>
                                    <ul className="showcase-highlights">
                                        {model.highlights.map((item) => (
                                            <li key={item}>
                                                <Check className="size-4" aria-hidden />
                                                <span>{item}</span>
                                            </li>
                                        ))}
                                    </ul>
                                </section>
                            ) : null}

                            <section className="showcase-panel">
                                <h2 className="showcase-panel-title">可选参数</h2>
                                <ShowcaseSpecTable model={model} />
                            </section>
                        </div>

                        <div className="showcase-side">
                            <section className="showcase-panel">
                                <h2 className="showcase-panel-title">价格</h2>
                                <ShowcasePriceTable prices={model.prices} />
                            </section>
                        </div>
                    </div>
                </>
            ) : null}
        </ShowcaseShell>
    );
}

/** 通配参数可能带着百分号编码而来（不同代理的透传习惯不一致），解不开就按原样查。 */
function normalizeSlug(raw: string | undefined): string {
    const value = (raw || "").replace(/^\/+/, "").trim();
    if (!value) return "";
    try {
        return decodeURIComponent(value);
    } catch {
        return value;
    }
}
