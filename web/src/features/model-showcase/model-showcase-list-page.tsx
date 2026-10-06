import { Link } from "react-router";

import { capabilityLabel } from "./presentation";
import { ShowcaseShell } from "./showcase-shell";
import { useShowcaseMeta } from "./use-showcase-meta";
import { useShowcaseModels } from "./use-showcase-models";

/**
 * 模型目录（公开）。
 *
 * 刻意做成一行一个的清单，而不是卡片墙：访客来这里是为了找"某个模型是干什么的"，
 * 一行足够放名称、模型标识与一句话定位，扫读比卡片快；卡片墙的留白与配图在没有
 * 真实截图之前只会让页面显得空。
 */
export function ModelShowcaseListPage() {
    useShowcaseMeta("模型介绍与定价", "平台已接入的生成模型清单：能力、参数与价格，与创作台使用同一份配置，页面上的价就是账单上的价。");
    const { models, loading, error, reload } = useShowcaseModels();

    return (
        <ShowcaseShell>
            <section className="doc-list-head">
                <h1 className="doc-title">模型</h1>
                <p className="doc-lead">平台已接入的生成模型。参数与价格来自创作台的同一份配置，页面上的价就是账单上的价。</p>
            </section>

            {loading && !models ? <p className="doc-status">正在加载模型列表…</p> : null}

            {!loading && error && !models ? (
                <div className="doc-status" role="alert">
                    <strong>没能加载出来</strong>
                    <p>{error}</p>
                    <button type="button" className="doc-button" onClick={reload}>
                        重试
                    </button>
                </div>
            ) : null}

            {models?.length ? (
                <ul className="doc-list">
                    {models.map((model) => (
                        <li key={model.slug}>
                            <Link className="doc-list-row" to={`/models/${model.slug}`}>
                                <span className="doc-list-main">
                                    <span className="doc-list-name">{model.displayName}</span>
                                    <span className="doc-list-slug">{model.slug}</span>
                                </span>
                                <span className="doc-list-tagline">{model.tagline || model.summary}</span>
                                <span className="doc-chip">{capabilityLabel(model.capability)}</span>
                            </Link>
                        </li>
                    ))}
                </ul>
            ) : null}

            {models && models.length === 0 ? <p className="doc-muted">当前没有已开放的模型。</p> : null}
        </ShowcaseShell>
    );
}
