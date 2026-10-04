import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "@fontsource-variable/space-grotesk";
import { isPublicRoutePath } from "@/lib/public-routes";
import { bootstrapAppearance } from "@/services/appearance-bootstrap";
import { bootstrapDesktopRuntime } from "@/services/desktop-runtime";
import { hydrateLocalCanvasProjectsFromBackend } from "@/services/local-workspace-repository";

async function startApplication() {
    await bootstrapDesktopRuntime();
    // 公开页（模型介绍）不碰工作区数据。
    //
    // 这一步拉的是 /api/canvas-projects，未登录访客必然拿到 401：一个给搜索引擎看的
    // 页面不该发一个注定失败的鉴权请求，而且它会把上个账号留在 IndexedDB 里的画布
    // 数据重新灌回 store。放行范围与登录门共用同一份白名单，避免两处各判一套。
    if (!isPublicRoutePath(window.location.pathname)) {
        await hydrateLocalCanvasProjectsFromBackend();
    }
    await bootstrapAppearance().finally(() => import("./application"));
}

void startApplication();
