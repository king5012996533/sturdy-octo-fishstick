/**
 * 未登录即可访问的公开路径。
 *
 * 托管登录门在应用最外层，未登录访问任何路径都会先落到登录页；因此每新增一个公开页，
 * 都必须在这里显式登记。判定按完整路径段比对，不做包含匹配——否则以后出现
 * `/models-archive` 这类同前缀路径，会被静默判成公开页。
 *
 * 当前只有模型广场：它是获客页，要把未登录访客和搜索引擎放进来。其余路径的鉴权行为不变。
 */
export const MODEL_SHOWCASE_PATH = "/models";

export function isPublicRoutePath(pathname: string): boolean {
    const normalized = normalizePathname(pathname);
    return normalized === MODEL_SHOWCASE_PATH || normalized.startsWith(`${MODEL_SHOWCASE_PATH}/`);
}

/**
 * 规范化用于判定的路径：去掉查询串与哈希，丢掉空段，让 `/models/` 与 `/models` 是同一页。
 *
 * 含 `.` / `..` 段的路径直接判成"非公开"：这类路径最终落到哪里由浏览器与代理的归一化
 * 规则决定，在这里替它们解析一次，等于把安全边界的判定权交给一个我们控制不到的规则。
 */
function normalizePathname(pathname: string): string {
    const withoutQuery = (pathname || "").split("?")[0].split("#")[0];
    const segments = withoutQuery.split("/").filter((segment) => segment !== "");
    if (segments.some((segment) => segment === "." || segment === "..")) return "";
    return segments.length ? `/${segments.join("/")}` : "/";
}
