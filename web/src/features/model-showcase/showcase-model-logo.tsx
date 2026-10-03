import { ModelLogo } from "@/components/model-logo";
import { modelBrandIconId } from "@/lib/model-brand-icon";

/**
 * 广场上的模型图标：后台配了 icon 就以配置为准，没配则按模型标识兜底到品牌图标。
 * 兜底规则与创作端模型选择器同一份，避免同一个模型在两个页面显示成两个样子。
 */
export function ShowcaseModelLogo({ icon, name, slug, size = 22 }: { icon?: string; name: string; slug: string; size?: number }) {
    const resolved = (icon || "").trim() || modelBrandIconId(`${slug} ${name}`);
    return <ModelLogo icon={resolved} size={size} />;
}
