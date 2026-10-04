import { Link, useParams } from "react-router";

import { capabilityLabel } from "./presentation";
import { ModelReadme } from "./model-readme";
import { ShowcasePriceTable } from "./price-table";
import { ShowcaseSpecTable } from "./spec-table";
import { ShowcaseShell } from "./showcase-shell";
import { useShowcaseModel } from "./use-showcase-models";
import { useShowcaseTitle } from "./use-showcase-title";

/**
 * 模型介绍页（公开，单个模型）。
 *
 * 这一页回答的是"这个模型是干什么的"，所以自述文件是主体、占最大篇幅，定价与参数
 * 是它下面的补充。刻意不做卡片墙、不做筛选、不做能力分组：模型页要给的是能读完的
 * 一段介绍，不是一堆需要点进去才知道内容的缩略卡。
 *
 * 也不用标签页切换内容：标签页会把"自述文件"藏在一个默认不选中的页签后面，而访客
 * 从搜索引擎进来时，命中的正是自述文件里的文字。
 */
export function ModelShowcaseDetailPage() {
    const slug = normalizeSlug(useParams()["*"]);
    const { model, loading, error, reload } = useShowcaseModel(slug);

    useShowcaseTitle(model ? `${model.displayName} · 模型介绍` : "模型介绍");

    return (
        <ShowcaseShell>
            {loading && !model ? <p className="doc-status">正在加载模型介绍…</p> : null}

            {!loading && error ? (
                <div className="doc-status" role="alert">
                    <strong>没能加载出来</strong>
                    <p>{error}</p>
                    <button type="button" className="doc-button" onClick={reload}>
                        重试
                    </button>
                </div>
            ) : null}

            {!loading && !error && !model ? (
                <div className="doc-status">
                    <strong>模型不存在</strong>
                    <p>这个模型可能已经下架，或者链接里的标识不完整。</p>
                    <Link to="/models" className="doc-button">
                        看看全部模型
                    </Link>
                </div>
            ) : null}

            {model ? (
                <article className="doc">
                    <header className="doc-head">
                        <p className="doc-breadcrumb">
                            <Link to="/models">模型</Link>
                            <span aria-hidden> / </span>
                            <span className="doc-breadcrumb-slug">{model.slug}</span>
                        </p>
                        <h1 className="doc-title">{model.displayName}</h1>
                        <p className="doc-lead">{model.summary || model.tagline}</p>
                        <div className="doc-head-meta">
                            <span className="doc-chip">{capabilityLabel(model.capability)}</span>
                        </div>
                    </header>

                    <section className="doc-section">
                        <h2 className="doc-section-title">自述文件</h2>
                        {model.readme ? <ModelReadme markdown={model.readme} /> : <p className="doc-muted">这个模型的介绍正在整理，暂时只有上面的简介。</p>}
                    </section>

                    <section className="doc-section">
                        <h2 className="doc-section-title">定价</h2>
                        <ShowcasePriceTable prices={model.prices} />
                    </section>

                    <section className="doc-section">
                        <h2 className="doc-section-title">参数</h2>
                        <ShowcaseSpecTable model={model} />
                    </section>
                </article>
            ) : null}
        </ShowcaseShell>
    );
}

/** 详情路由用通配匹配，拿到的是一段可能带前导斜杠的剩余路径。 */
function normalizeSlug(raw: string | undefined): string {
    return (raw || "").replace(/^\/+/, "").trim();
}
