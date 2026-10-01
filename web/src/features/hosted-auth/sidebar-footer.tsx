import { useCallback, useState } from "react";
import { ChevronsUpDown, CircleUserRound, LogOut, UserRound } from "lucide-react";
import { Popover } from "antd";
import { Link } from "react-router";

import { UserAvatar } from "@/components/layout/user-avatar";
import { useUserStore } from "@/stores/use-user-store";

import { logoutHostedAuth } from "./api";

const LOGIN_ENTRY_PATH = "/";

/**
 * 退出登录：先让服务端吊销会话，再清本地状态，最后整页回到入口。
 *
 * 抽成可注入的纯流程是为了让"先吊销再跳转"和"吊销失败也要清干净"这两条顺序可测——
 * 只切 UI 状态会把上一个账号的画布/素材留在内存里，因此这里必须整页重载。
 */
export async function performHostedAuthLogout({ logout, clearLocalSession, redirect }: { logout: () => Promise<unknown>; clearLocalSession: () => void; redirect: (url: string) => void }) {
    try {
        await logout();
    } catch {
        // 服务端吊销失败也要继续清理：本地残留的账号状态比"会话没吊销"更危险，
        // 而且服务端会话本来就有绝对过期兜底。
    } finally {
        clearLocalSession();
    }
    redirect(LOGIN_ENTRY_PATH);
}

/**
 * 身份副标题。
 *
 * 三种账号的可用标识不一样：邮箱注册的有邮箱，手机号注册的只有绑定标识，
 * 两者都缺时才回落到用户名。顺序写反会让手机号用户看到一个空的第二行。
 */
export function hostedAuthIdentityLabel(user: { username?: string; email?: string; identityId?: string } | null): string {
    if (!user) return "";
    if (user.email) return user.email;
    if (user.identityId) return user.identityId;
    return user.username ? `@${user.username}` : "";
}

/**
 * 账户面板。
 *
 * 从 Popover 里抽出来是为了让它能在不打开浮层的情况下被断言 —— 浮层内容在收起时
 * 根本不渲染，否则「退出登录到底还在不在」这件事就只能靠手工点开确认。
 */
export function HostedAuthAccountPanel({ onLogout, pending, onNavigate }: { onLogout: () => void; pending: boolean; onNavigate?: () => void }) {
    const user = useUserStore((state) => state.user);
    const label = user ? user.displayName || user.username : "账户";
    const identity = hostedAuthIdentityLabel(user);

    return (
        <div className="w-[264px] p-1" data-testid="hosted-auth-account-panel">
            <div className="flex items-center gap-3 px-1.5 pb-3.5 pt-1">
                {user ? <UserAvatar user={user} className="size-10 shrink-0 rounded-[13px] bg-foreground/[.07]" /> : <CircleUserRound className="size-10 shrink-0 rounded-[13px] bg-foreground/[.07] p-2" aria-hidden />}
                <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <b className="truncate text-[14px] font-semibold">{label}</b>
                    {identity ? <span className="truncate text-[11px] text-foreground/50">{identity}</span> : null}
                </div>
                <em className="shrink-0 rounded-full bg-foreground/[.07] px-2 py-0.5 text-[10px] not-italic text-foreground/55">{user?.role === "admin" ? "管理员" : "创作者"}</em>
            </div>

            <div className="mx-1 mb-1 h-px bg-foreground/10" role="presentation" />

            {/* 用户中心（/settings）在托管形态下没有侧栏入口：模型配置被摇掉之后，
                这一页就从工作区导航里消失了。放进账户菜单而不是侧栏，是因为它答的是
                "我是谁、我的账号什么状态"，与工作区里的创作入口不同类。
                点击后由调用方关掉浮层，否则路由换了浮层还悬在新页面上面。 */}
            <Link
                to="/settings"
                onClick={onNavigate}
                data-testid="hosted-auth-account-center"
                className="flex h-9 w-full items-center gap-2.5 rounded-[10px] px-2.5 text-[13px] text-foreground/80 transition-colors hover:bg-foreground/5 focus-visible:outline focus-visible:outline-2"
            >
                <UserRound className="size-4 shrink-0" />
                <span>用户中心</span>
            </Link>

            {/* 退出登录是低频破坏性操作：放在面板最底部、用危险色、与身份区隔一条线，
                而不是侧栏里一个常驻的大按钮。 */}
            <button
                type="button"
                onClick={onLogout}
                disabled={pending}
                data-testid="hosted-auth-logout"
                className="flex h-9 w-full items-center gap-2.5 rounded-[10px] px-2.5 text-[13px] text-[var(--color-status-error)] transition-colors hover:bg-[color-mix(in_srgb,var(--color-status-error)_12%,transparent)] focus-visible:outline focus-visible:outline-2 disabled:opacity-60"
            >
                <LogOut className="size-4 shrink-0" />
                <span>退出登录</span>
            </button>
        </div>
    );
}

/**
 * 退出登录的本地状态。
 *
 * 侧栏底部和首页右上角是同一件事的两个入口，共用一套流程，避免两处实现各自漂移。
 */
function useHostedAuthLogout() {
    const [pending, setPending] = useState(false);
    const logout = useCallback(() => {
        setPending(true);
        void performHostedAuthLogout({
            logout: logoutHostedAuth,
            clearLocalSession: () => useUserStore.getState().clearSession(),
            redirect: (url) => window.location.assign(url),
        }).finally(() => setPending(false));
    }, []);
    return { pending, logout };
}

/**
 * 首页右上角的账户入口。
 *
 * 首页没有页头，右上角是唯一不压内容的位置；与侧栏那枚共用同一个面板，
 * 只把触发器换成头像 chip。
 */
export function HostedAuthTopbarAccount() {
    const [open, setOpen] = useState(false);
    const user = useUserStore((state) => state.user);
    const { pending, logout } = useHostedAuthLogout();
    const label = user ? user.displayName || user.username : "账户";

    return (
        <Popover trigger="click" open={open} onOpenChange={setOpen} placement="bottomRight" content={<HostedAuthAccountPanel onLogout={logout} pending={pending} onNavigate={() => setOpen(false)} />}>
            <button
                type="button"
                aria-label="账户菜单与退出登录"
                aria-expanded={open}
                data-testid="hosted-auth-topbar-account"
                className="creation-top-action is-account"
            >
                {user ? <UserAvatar user={user} className="size-6 shrink-0 rounded-full" /> : <CircleUserRound className="size-4" aria-hidden />}
                <span className="creation-top-account-name">{label}</span>
            </button>
        </Popover>
    );
}

/**
 * 侧栏底部的账户入口。
 *
 * 取代原先裸露的「退出登录」按钮：身份条点开才是菜单，收起态只留头像。
 * 侧栏底部同时挂着主题切换与版本入口，多一个同等重量的按钮会让这一栏失去主次。
 */
export function HostedAuthSidebarFooter({ collapsed }: { collapsed: boolean }) {
    const [open, setOpen] = useState(false);
    const user = useUserStore((state) => state.user);
    const { pending, logout: handleLogout } = useHostedAuthLogout();

    // 收起态不渲染任何 span：宽度只剩 40px，除图标外的一切都只会被裁掉半截。
    if (collapsed) {
        return (
        <Popover trigger="click" open={open} onOpenChange={setOpen} placement="rightBottom" content={<HostedAuthAccountPanel onLogout={handleLogout} pending={pending} onNavigate={() => setOpen(false)} />}>
                <button
                    type="button"
                    aria-label="账户菜单与退出登录"
                    aria-expanded={open}
                    data-testid="hosted-auth-account-chip"
                    className="flex min-h-9 w-full items-center justify-center rounded-lg text-foreground/65 transition-colors hover:bg-foreground/5 hover:text-foreground focus-visible:outline focus-visible:outline-2"
                >
                    <CircleUserRound className="size-4" />
                </button>
            </Popover>
        );
    }

    const label = user ? user.displayName || user.username : "账户";
    const identity = hostedAuthIdentityLabel(user);

    return (
        <Popover trigger="click" open={open} onOpenChange={setOpen} placement="topLeft" content={<HostedAuthAccountPanel onLogout={handleLogout} pending={pending} onNavigate={() => setOpen(false)} />}>
            <button
                type="button"
                aria-label="账户菜单与退出登录"
                aria-expanded={open}
                data-testid="hosted-auth-account-chip"
                className="flex min-h-11 w-full items-center gap-2.5 rounded-[11px] border border-transparent px-2 py-1.5 text-left transition-colors hover:border-[var(--color-border)] hover:bg-foreground/5 focus-visible:outline focus-visible:outline-2"
            >
                {user ? <UserAvatar user={user} className="size-7 shrink-0 rounded-[9px] bg-foreground/[.07]" /> : <CircleUserRound className="size-7 shrink-0 rounded-[9px] bg-foreground/[.07] p-1.5" aria-hidden />}
                <span className="flex min-w-0 flex-1 flex-col gap-px">
                    <b className="truncate text-[13px] font-medium text-foreground">{label}</b>
                    {identity ? <i className="truncate text-[11px] not-italic text-foreground/50">{identity}</i> : null}
                </span>
                <ChevronsUpDown className="size-3.5 shrink-0 text-foreground/40" aria-hidden />
            </button>
        </Popover>
    );
}
