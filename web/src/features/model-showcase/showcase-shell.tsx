import { Link } from "react-router";
import { ArrowRight } from "lucide-react";
import type { ReactNode } from "react";

import { BrandLogoFrame } from "@/components/brand/brand-logo";
import { SiteComplianceFooter } from "@/components/layout/site-compliance-footer";
import { useAppearanceStore } from "@/stores/use-appearance-store";

import "./model-showcase.css";

/**
 * 公开页外壳：顶栏 + 主区 + 合规页脚。
 *
 * 不复用 WorkspaceLayout：那套外壳挂着侧栏、账号菜单与水合逻辑，都为登录用户准备；
 * 未登录访客进来只会看到一排点不动的入口。这里只保留两件事——品牌，和唯一的动作
 * 「进入创作台」（未登录时该路由由登录门接管）。
 */
export function ShowcaseShell({ children }: { children: ReactNode }) {
    const brandName = useAppearanceStore((state) => state.appearance.brandName);

    return (
        <div className="showcase-scene">
            <header className="showcase-topbar">
                <div className="showcase-topbar-inner">
                    <Link to="/models" className="showcase-brand" aria-label={`${brandName} 模型广场`}>
                        <BrandLogoFrame className="grid size-8 place-items-center" logoClassName="size-5 object-contain" alt="" fallback={<span aria-hidden>K</span>} />
                        <span className="showcase-brand-wordmark">{brandName}</span>
                    </Link>
                    <span className="showcase-topbar-divider" aria-hidden />
                    <span className="showcase-topbar-section">模型广场</span>
                    <Link to="/" className="showcase-enter">
                        进入创作台
                        <ArrowRight className="size-3.5" aria-hidden />
                    </Link>
                </div>
            </header>
            <main className="showcase-main">{children}</main>
            <SiteComplianceFooter className="showcase-footer" />
        </div>
    );
}
