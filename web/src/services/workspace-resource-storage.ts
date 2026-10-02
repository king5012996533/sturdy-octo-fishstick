import { isHostedBuild } from "@/lib/hosted-build";
import { isLocalRuntimeMode, isNativeDesktopRuntime } from "@/lib/runtime-mode";

/**
 * Resource storage policy for the two local runtimes:
 * - native desktop: Go resource service is the durable local store;
 * - browser local: IndexedDB is the durable local store.
 * Hosted mode always attempts the remote resource API first.
 */
export function usesBrowserLocalResourceStore() {
    // 托管构建里账号是服务端账号，浏览器存储只能是缓存而不是归宿。
    // 少了这个判断，托管站会把用户上传的图片只写进当前浏览器的 IndexedDB：
    // 换台机器、换个浏览器素材就凭空消失，服务端资源库一无所知。
    // 本地/桌面构建（__BEEFTV_HOSTED_AUTH__ 为 false）保持原有本地优先策略。
    if (isHostedBuild()) return false;
    return isLocalRuntimeMode() && !isNativeDesktopRuntime();
}

export function usesNativeLocalResourceStore() {
    return isLocalRuntimeMode() && isNativeDesktopRuntime();
}
