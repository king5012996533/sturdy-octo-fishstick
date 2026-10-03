import { Search } from "lucide-react";

import { SHOWCASE_CAPABILITY_FILTERS, type ShowcaseCapability } from "./presentation";

/**
 * 列表页筛选栏：能力分组 + 关键词。
 *
 * 关键词筛选在本地做：整份目录一次就取全了（几十条），把每次输入都变成一次请求既不必要，
 * 也会让输入框在弱网下卡顿。
 */
export function ShowcaseFilters({ capability, keyword, onCapabilityChange, onKeywordChange }: { capability: ShowcaseCapability; keyword: string; onCapabilityChange: (next: ShowcaseCapability) => void; onKeywordChange: (next: string) => void }) {
    return (
        <div className="showcase-toolbar">
            <div className="showcase-chips" role="tablist" aria-label="按能力筛选">
                {SHOWCASE_CAPABILITY_FILTERS.map((item) => (
                    <button key={item.key} type="button" role="tab" aria-selected={capability === item.key} className={capability === item.key ? "showcase-chip is-active" : "showcase-chip"} onClick={() => onCapabilityChange(item.key)}>
                        {item.label}
                    </button>
                ))}
            </div>
            <label className="showcase-search">
                <Search className="size-4" aria-hidden />
                <input type="search" value={keyword} placeholder="搜索模型名称或标识" aria-label="搜索模型" onChange={(event) => onKeywordChange(event.target.value)} />
            </label>
        </div>
    );
}
