/**
 * 当前产物是不是托管形态（带账号、后台与云端资源库的 SaaS 构建）。
 *
 * 用 typeof 包一层是必须的：这个常量由 Vite 在构建时注入，本地/桌面构建与单元测试
 * 环境里根本不存在。裸引用会抛 ReferenceError，把"不是托管"直接变成崩溃。
 */
export function isHostedBuild() {
    return typeof __BEEFTV_HOSTED_AUTH__ !== "undefined" && __BEEFTV_HOSTED_AUTH__;
}
