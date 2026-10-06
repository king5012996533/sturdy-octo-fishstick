import { useEffect } from "react";

import { setDocumentMeta, useAppearanceStore } from "@/stores/use-appearance-store";

/** 需要按页覆写的标签；顺序即还原顺序，不依赖对象键序。 */
const META_SLOTS = [
    { attribute: "name", key: "description" },
    { attribute: "property", key: "og:title" },
    { attribute: "property", key: "og:description" },
] as const;

type MetaSnapshot = { attribute: "name" | "property"; key: string; content: string | null }[];

/**
 * 公开页要有自己那一份标题与描述。
 *
 * 站点的默认文案来自平台外观配置，说的是产品整体；而模型介绍页要回答的是"这个模型
 * 是干什么的"。两者共用一份的话，每个模型页在搜索结果里长得一模一样，等于白白浪费
 * 一个能命中具体模型名的入口。这里在挂载时覆写、离开时还原——静态抓取拿到的正是
 * 覆写后的那一份。
 *
 * 覆写范围刻意只到标题与描述：og:site_name、canonical、图标属于站点级，仍由
 * applyAppearanceMetadata 负责，免得两个地方各写一套。
 */
export function useShowcaseMeta(title: string, description: string) {
    const brandName = useAppearanceStore((state) => state.appearance.brandName);
    const fullTitle = brandName && !title.includes(brandName) ? `${title} · ${brandName}` : title;

    useEffect(() => {
        const previousTitle = document.title;
        const previousMeta = captureMeta(document);
        document.title = fullTitle;
        setDocumentMeta(document, "name", "description", description);
        setDocumentMeta(document, "property", "og:title", fullTitle);
        setDocumentMeta(document, "property", "og:description", description);
        return () => {
            document.title = previousTitle;
            for (const slot of previousMeta) setDocumentMeta(document, slot.attribute, slot.key, slot.content ?? "");
        };
    }, [fullTitle, description]);
}

function captureMeta(targetDocument: Document): MetaSnapshot {
    return META_SLOTS.map(({ attribute, key }) => {
        const element = targetDocument.querySelector<HTMLMetaElement>(`meta[${attribute}="${key}"]`);
        return { attribute, key, content: element ? element.content : null };
    });
}
