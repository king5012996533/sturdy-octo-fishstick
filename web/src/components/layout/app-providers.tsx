import type { ReactNode } from "react";
import { lazy, Suspense, useLayoutEffect, type ComponentType } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App, ConfigProvider } from "antd";
import zhCN from "antd/locale/zh_CN";

import { WorkspaceBootstrapHydrator } from "@/components/workspace/workspace-bootstrap-hydrator";
import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";
import { getAntThemeConfig } from "@/lib/app-theme";
import { applySkinTheme } from "@/lib/skin-themes";
import { appQueryClient } from "@/lib/query-client";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";
import { applyAppearanceMetadata, useAppearanceStore } from "@/stores/use-appearance-store";
import { useUserStore } from "@/stores/use-user-store";

const ClientRootInit = lazy(() => import("@/components/layout/client-root-init").then((module) => ({ default: module.ClientRootInit })));

/**
 * 托管登录门的生产接法。
 *
 * 常量在构建期被替换：`BEEFTV_HOSTED_AUTH` 未开启时本函数返回 null，函数体内的动态
 * import 随之为死代码并被摇树删除，登录界面不会进入本地/桌面产物。这与 devRoutes
 * 处理实验路由的方式一致，区别是开关来自构建环境而不是 import.meta.env.DEV。
 */
function resolveHostedAuthGate(): ComponentType<{ children: ReactNode }> | null {
    if (!__BEEFTV_HOSTED_AUTH__) return null;
    return lazy(() => import("@/features/hosted-auth").then((module) => ({ default: module.HostedAuthGate })));
}

const HostedAuthGate = resolveHostedAuthGate();

/** 未启用托管登录时完全透传，桌面启动路径不受影响。 */
function HostedAuthBoundary({ children }: { children: ReactNode }) {
    if (!HostedAuthGate) return <>{children}</>;
    return (
        <Suspense fallback={<FullScreenLoader label="正在检查登录状态" detail="准备账号会话" />}>
            <HostedAuthGate>{children}</HostedAuthGate>
        </Suspense>
    );
}

function ClientRootBoundary({ children }: { children: ReactNode }) {
    const authenticated = useUserStore((state) => Boolean(state.user));
    if (!authenticated) return children;
    return (
        <Suspense fallback={<FullScreenLoader label="正在准备创作环境" detail="连接本地能力与模型配置" />}>
            <ClientRootInit>{children}</ClientRootInit>
        </Suspense>
    );
}

export function AppProviders({ children }: { children: ReactNode }) {
    const theme = useActiveTheme();
    const dark = theme === "dark";
    const appearance = useAppearanceStore((state) => state.appearance);

    useLayoutEffect(() => {
        document.documentElement.classList.toggle("dark", dark);
        document.documentElement.style.colorScheme = theme;
        applySkinTheme(appearance.activeSkin, theme);
        applyAppearanceMetadata(appearance);
    }, [appearance, dark, theme]);

    // DEV 复现台必须是同源本地确定性场景：WorkspaceBootstrapHydrator 会打 /api/workspace/bootstrap，
    // ClientRootInit 会打 /api/model-catalog，没有后端时产生真实 502，与导演台无关却会污染判据。
    // 只精确匹配该路径；生产构建中 import.meta.env.DEV 为 false，本分支被摇树删除。
    const isolateDevRepro = import.meta.env.DEV && typeof window !== "undefined" && window.location.pathname === "/dev/director-repro";

    return (
        <ConfigProvider locale={zhCN} theme={getAntThemeConfig(dark, appearance.activeSkin)}>
            <App message={{ duration: 3, maxCount: 3 }} notification={{ duration: 4.5, maxCount: 3, placement: "topRight" }}>
                <QueryClientProvider client={appQueryClient}>
                    {isolateDevRepro ? (
                        children
                    ) : (
                        <HostedAuthBoundary>
                            <WorkspaceBootstrapHydrator>
                                <ClientRootBoundary>{children}</ClientRootBoundary>
                            </WorkspaceBootstrapHydrator>
                        </HostedAuthBoundary>
                    )}
                </QueryClientProvider>
            </App>
        </ConfigProvider>
    );
}
