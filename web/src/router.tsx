import { lazy, Suspense, type ReactNode } from "react";
import { createBrowserRouter, Navigate, Outlet, useLocation, useParams } from "react-router";

import { FullScreenLoader, WorkspaceRouteLoader } from "@/components/ui/aceternity/full-screen-loader";
import { loadAssetsPage, loadCanvasPage, loadCanvasProjectPage, loadCreatePage, loadProjectDetailPage } from "@/lib/workspace-route-modules";
import { CanvasRefreshShell } from "@/pages/canvas/canvas-refresh-shell";
import RouteErrorPage from "@/pages/route-error";
import { isLocalWorkspaceMode } from "@/services/workspace-mode";

const AssetsPage = lazy(loadAssetsPage);
const WalletPage = lazy(() => import("@/pages/wallet").then((module) => ({ default: module.WalletPage })));
const CanvasPage = lazy(loadCanvasPage);
const CanvasProjectPage = lazy(loadCanvasProjectPage);
const CreatePage = lazy(loadCreatePage);
const NotFound = lazy(() => import("@/pages/not-found"));
const PluginsPage = lazy(() => import("@/pages/plugins"));
const EagleLibraryPage = lazy(() => import("@/pages/plugins/eagle"));
const ProjectDetailPage = lazy(loadProjectDetailPage);
const SettingsPage = lazy(() => import("@/pages/settings"));
const SupportPage = lazy(() => import("@/pages/support").then((module) => ({ default: module.SupportPage })));
const TestVoiceRecording = lazy(() => import("@/pages/test-voice-recording"));
const UserLayout = lazy(() => import("@/layouts/user-layout"));
const RequireFeature = lazy(() => import("@/components/workspace/require-feature").then((module) => ({ default: module.RequireFeature })));

function deferred(element: ReactNode) {
    return <Suspense fallback={<WorkspaceRouteLoader />}>{element}</Suspense>;
}

function fullScreenDeferred(element: ReactNode) {
    return <Suspense fallback={<FullScreenLoader label="正在打开创作空间" detail="准备当前页面" />}>{element}</Suspense>;
}

function WorkspaceLayout() {
    const { pathname } = useLocation();
    const isCanvasProjectRoute = pathname.startsWith("/canvas/");
    const fallback = isCanvasProjectRoute ? <CanvasRefreshShell /> : <FullScreenLoader label="正在打开创作空间" detail="准备当前页面" />;
    return (
        <Suspense fallback={fallback}>
            <UserLayout>
                <Outlet />
            </UserLayout>
        </Suspense>
    );
}

function LocalAwareProjectRoute() {
    const { projectId } = useParams();
    const localMode = isLocalWorkspaceMode();
    if (localMode && projectId) return <Navigate to={`/canvas/${projectId}`} replace />;
    return deferred(<ProjectDetailPage />);
}

function LegacyProjectAliasRoute() {
    const { projectId, "*": rest } = useParams();
    return <Navigate to={`/projects/${projectId}${rest ? `/${rest}` : ""}`} replace />;
}

/**
 * DEV 专用实验室路由。
 *
 * lazy(() => import(...)) 写在函数体内，而不是模块顶层常量：
 * 生产构建时 import.meta.env.DEV 被替换为 false，本函数随之不可达，
 * 摇树会连同其中的动态 import 一起删除，实验室代码不进入生产依赖图。
 * 若把 lazy 提到模块顶层，动态 import 会被静态分析成真实 chunk 并打进 dist。
 */
function devRoutes() {
    const FolderPreviewLab = lazy(() => import("@/pages/dev/folder-preview-lab"));
    const DirectorReproLab = lazy(() => import("@/pages/dev/director-repro-lab"));
    return [
        { path: "/dev/folders", element: fullScreenDeferred(<FolderPreviewLab />), errorElement: <RouteErrorPage /> },
        { path: "/dev/director-repro", element: fullScreenDeferred(<DirectorReproLab />), errorElement: <RouteErrorPage /> },
    ];
}

/**
 * 平台运营后台路由。
 *
 * 与 devRoutes/托管登录同一手法：常量在构建期被替换，本地/桌面构建返回空数组，
 * 函数体内的动态 import 变成死代码被摇树删除，运营后台不会进入本地产物。
 */
function hostedAdminRoutes() {
    if (!__BEEFTV_HOSTED_AUTH__) return [];
    const AdminConsolePage = lazy(() => import("@/features/admin-console").then((module) => ({ default: module.AdminConsolePage })));
    const RequireAdmin = lazy(() => import("@/features/admin-console/require-admin").then((module) => ({ default: module.RequireAdmin })));
    return [
        {
            path: "/admin",
            element: (
                <Suspense fallback={<FullScreenLoader label="正在打开管理后台" detail="准备平台配置" />}>
                    <RequireAdmin>
                        <AdminConsolePage />
                    </RequireAdmin>
                </Suspense>
            ),
            errorElement: <RouteErrorPage />,
        },
    ];
}

/**
 * 模型介绍（公开页）路由。
 *
 * 与运营后台同一手法：常量在构建期替换，本地/桌面构建返回空数组，函数体内的动态 import
 * 变成死代码被摇树删除——本地形态根本没有 /api/public/models。
 *
 * 刻意挂在工作区路由组之外：这一页要给未登录访客与搜索引擎看，不能继承 WorkspaceLayout
 * 那套需要账号的外壳与顶栏。
 */
function modelDocRoutes() {
    if (!__BEEFTV_HOSTED_AUTH__) return [];
    const ModelShowcaseListPage = lazy(() => import("@/features/model-showcase/model-showcase-list-page").then((module) => ({ default: module.ModelShowcaseListPage })));
    const ModelShowcaseDetailPage = lazy(() => import("@/features/model-showcase/model-showcase-detail-page").then((module) => ({ default: module.ModelShowcaseDetailPage })));
    const fallback = <FullScreenLoader label="正在打开模型介绍" detail="准备模型信息" />;
    return [
        {
            path: "/models",
            element: (
                <Suspense fallback={fallback}>
                    <ModelShowcaseListPage />
                </Suspense>
            ),
            errorElement: <RouteErrorPage />,
        },
        // 详情用通配：模型标识自带斜杠（openai/gpt-image-2.5-sunburst），路径参数会在
        // 不同代理上解出不同结果，通配让斜杠原样留在路径段里。
        {
            path: "/models/*",
            element: (
                <Suspense fallback={fallback}>
                    <ModelShowcaseDetailPage />
                </Suspense>
            ),
            errorElement: <RouteErrorPage />,
        },
    ];
}

export const router = createBrowserRouter([
    ...(import.meta.env.DEV ? devRoutes() : []),
    ...modelDocRoutes(),
    {
        element: <WorkspaceLayout />,
        errorElement: <RouteErrorPage />,
        children: [
            // 根路径就是创作台：首页仪表盘与创作台是同一件事的两个入口，只保留后者，
            // 用户点「首页」落地即可直接开始创作，不必先经过一层中转。
            { path: "/", element: deferred(<CreatePage />) },
            { path: "/create", element: deferred(<CreatePage />) },
            {
                path: "/tasks",
                // 任务页暂不开放，保留路由以避免旧链接进入半成品界面。
                element: <Navigate to="/" replace />,
            },
            { path: "/assets", element: deferred(<AssetsPage />) },
            // 积分中心是唯一的充值入口（余额、充值档位、订单、流水都来自托管路由组里的
            // /api/finance/*）：本地/桌面构建没有这些接口，入口被摇树删除，避免必然 404。
            ...(__BEEFTV_HOSTED_AUTH__ ? [{ path: "/wallet", element: deferred(<WalletPage />) }] : []),
            // 旧的订阅与充值页已下线，历史书签与外部跳转统一落到积分中心，不再 404。
            ...(__BEEFTV_HOSTED_AUTH__ ? [{ path: "/billing", element: <Navigate to="/wallet" replace /> }] : []),
            // 帮助与反馈同积分中心：工单接口只注册在托管路由组里，本地/桌面构建没有后端。
            ...(__BEEFTV_HOSTED_AUTH__ ? [{ path: "/support", element: deferred(<SupportPage />) }] : []),
            { path: "/skills", element: <Navigate to="/" replace /> },
            { path: "/skill", element: <Navigate to="/" replace /> },
            { path: "/skills/reference", element: <Navigate to="/" replace /> },
            {
                path: "/plugins",
                element: <RequireFeature feature="pluginCenterEnabled">{deferred(<PluginsPage />)}</RequireFeature>,
            },
            {
                path: "/plugins/eagle",
                element: <RequireFeature feature="pluginCenterEnabled">{deferred(<EagleLibraryPage />)}</RequireFeature>,
            },
            { path: "/settings", element: deferred(<SettingsPage />) },
            { path: "/test-voice-recording", element: deferred(<TestVoiceRecording />) },
            {
                path: "/projects",
                element: deferred(<CanvasPage />),
            },
            // LibTV 使用单数 `/project` 作为项目库入口；直接渲染本地项目库，
            // 保留原始 URL，避免像素复刻时出现一次重定向造成的布局/加载闪烁。
            {
                path: "/project",
                element: deferred(<CanvasPage />),
            },
            {
                path: "/projects/:projectId",
                element: <LocalAwareProjectRoute />,
            },
            { path: "/project/:projectId", element: <LegacyProjectAliasRoute /> },
            { path: "/project/:projectId/*", element: <LegacyProjectAliasRoute /> },
            {
                path: "/projects/:projectId/:view",
                element: <LocalAwareProjectRoute />,
            },
            {
                path: "/projects/:projectId/chapters/:chapterId",
                element: <LocalAwareProjectRoute />,
            },
            {
                path: "/projects/:projectId/workflow/:unitId/:stage",
                element: <LocalAwareProjectRoute />,
            },
            ...hostedAdminRoutes(),
            { path: "/canvas", element: deferred(<CanvasPage />) },
            { path: "/canvas/:id", element: <CanvasProjectPage /> },
        ],
    },
    { path: "*", element: fullScreenDeferred(<NotFound />) },
]);
