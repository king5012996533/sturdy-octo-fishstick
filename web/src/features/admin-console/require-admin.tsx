import type { ReactNode } from "react";
import { Navigate } from "react-router";

import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";
import { useUserStore } from "@/stores/use-user-store";

/**
 * 运营后台的入口守卫。
 *
 * 后台接口本身由服务端 requireAdminMiddleware 把关，这里的守卫只解决"看不到"：
 * 否则非管理员会拿到一个满屏的错误面板。会话尚未水合时停在加载态而不是跳转——
 * 本地/桌面形态的拥有者角色是 admin，提前跳转会让刷新后台页面时被踢回首页。
 */
export function RequireAdmin({ children }: { children: ReactNode }) {
    const user = useUserStore((state) => state.user);
    const hydrated = useUserStore((state) => state.hydrated);
    if (!hydrated) return <FullScreenLoader label="正在校验后台权限" detail="读取当前账号角色" />;
    if (!user || user.role !== "admin") return <Navigate to="/" replace />;
    return <>{children}</>;
}
