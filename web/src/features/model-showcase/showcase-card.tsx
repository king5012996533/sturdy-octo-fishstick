import { Link } from "react-router";

import type { ShowcaseModel } from "./api";
import { capabilityLabel, cardSubtitle } from "./presentation";
import { ShowcaseModelLogo } from "./showcase-model-logo";

/**
 * 列表卡片：身份 + 能力 + 一句介绍。
 *
 * 卡片上不标积分：广场是"挑模型"的地方，价格是"决定用不用"时才需要的信息，摆在卡片上
 * 会让每张卡都变成一个报价单，反而看不清这个模型能干什么。价格在详情页的价目表里，
 * 与创作台的实扣同源。
 */
export function ShowcaseCard({ model }: { model: ShowcaseModel }) {
    const subtitle = cardSubtitle(model);

    return (
        <Link to={`/models/${model.slug}`} className="showcase-card">
            <div className="showcase-card-head">
                <span className="showcase-card-mark">
                    <ShowcaseModelLogo icon={model.icon} name={model.displayName} slug={model.slug} />
                </span>
                <span className="showcase-card-identity">
                    <span className="showcase-card-name">{model.displayName}</span>
                    <span className="showcase-card-slug">{model.slug}</span>
                </span>
                <span className="showcase-tag">{capabilityLabel(model.capability)}</span>
            </div>
            {subtitle ? <p className="showcase-card-tagline">{subtitle}</p> : null}
        </Link>
    );
}
