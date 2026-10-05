import { AutoComplete, Tag } from "antd";

import { modelPriceTargetName, modelPriceTargetSearchText, type ModelPriceTarget } from "./model-price-targets";

/**
 * 模型标识选择器。
 *
 * 可搜、可选，也允许继续手填：模型刚建好还没进目录、或者目录请求失败时，手填是唯一的
 * 退路，所以在 AutoComplete 上做而不是 Select。
 *
 * 选中后把整条目录项回调出去，让调用方一次把能力、计价单位都填好。只回一个字符串的话，
 * 运营选完模型还得自己判断"这个模型按秒还是按次"，而猜错的代价是一次真实的计费错误。
 */
export function ModelKeyPicker({
    id,
    targets,
    value,
    onChange,
    onPick,
    placeholder = "搜索渠道、模型名或标识",
}: {
    /** Form.Item 注入：必须落到内层 input 上，否则 label 的 for 指不到控件。 */
    id?: string;
    targets: ModelPriceTarget[];
    value?: string;
    onChange?: (value: string) => void;
    onPick?: (target: ModelPriceTarget) => void;
    placeholder?: string;
}) {
    const options = targets.map((target) => ({
        value: target.fullKey,
        target,
        searchText: modelPriceTargetSearchText(target),
        label: (
            <span className="flex min-w-0 items-center gap-2">
                {/* 名字一行、完整标识一行：拼成一行会被输入框的宽度截成「Replicate · 主账…」。 */}
                <span className="flex min-w-0 flex-col">
                    <span className="truncate">{modelPriceTargetName(target)}</span>
                    <span className="admin-user-sub truncate">{target.fullKey}</span>
                </span>
                {target.enabled ? null : <Tag>{`已停用`}</Tag>}
            </span>
        ),
    }));

    return (
        <AutoComplete
            id={id}
            value={value}
            options={options}
            // 只按 value（完整标识）过滤的话，搜"配乐"或"Replicate"会一条都出不来：
            // 运营记的是模型名与渠道名，不是 CHANNEL_000003::minimax/music-2.5。
            filterOption={(input, option) => ((option?.searchText as string) ?? "").includes(input.trim().toLowerCase())}
            onChange={(next) => onChange?.(next)}
            onSelect={(_next, option) => onPick?.((option as { target: ModelPriceTarget }).target)}
            placeholder={placeholder}
            // 下拉跟着内容走：模型标识比输入框长，按输入框宽度截断等于没显示。
            popupMatchSelectWidth={false}
            // 目录为空（所有渠道都停用、或目录请求失败）时，手填仍是退路，所以要说清格式。
            notFoundContent={<span className="admin-user-sub">没有匹配的模型；也可以直接手填「渠道 ID::平台模型标识」。</span>}
            allowClear
        />
    );
}
