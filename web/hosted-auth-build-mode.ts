/**
 * 托管登录是 SaaS 形态的能力，本地/桌面构建必须把它排除在产物之外。
 *
 * 默认关闭：桌面发布、本地发布校验、公开导出等既有构建路径都保持"本地-only"，
 * 只有显式打开开关的构建（SaaS 镜像、Vercel、`dev:hosted`）才包含登录界面。
 */
export function resolveHostedAuthEnabled(value: string | undefined) {
    return value === "1" || value === "true";
}
