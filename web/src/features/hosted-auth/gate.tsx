import { useCallback, useEffect, useState, type ReactNode } from "react";

import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";

import { detectHostedAuth, getHostedAuthSession, type HostedAuthMethod, type HostedAuthUser } from "./api";
import { HostedAuthLoginPage } from "./login-page";
import { useHostedSystemModels } from "./system-models";

export type HostedAuthGatePhase =
    /** 本地/桌面形态：后端没有登录路由，整个门失效。 */
    { phase: "local" } | { phase: "anonymous"; methods: HostedAuthMethod[] } | { phase: "authenticated"; user: HostedAuthUser };

export type HostedAuthProbe = { methods: HostedAuthMethod[] | null; session: HostedAuthUser | null };

/**
 * 探测结果 → 门的阶段。返回 null 表示探测本身失败，调用方必须保持加载态。
 *
 * 这里刻意没有"失败就放行"的分支：探测失败时若透传 children，托管部署会在后端故障期间
 * 变成一个无鉴权入口；停在加载态最多是此时不可用。
 */
export function resolveHostedAuthGatePhase(probe: HostedAuthProbe | null): HostedAuthGatePhase | null {
    if (!probe) return null;
    if (probe.methods === null) return { phase: "local" };
    if (probe.session) return { phase: "authenticated", user: probe.session };
    return { phase: "anonymous", methods: probe.methods };
}

/**
 * 托管登录门。
 *
 * 位置在应用最外层、工作区水合之前：本地水合逻辑在拿不到 bootstrap 时会**回落成本地工作区**，
 * 因此在它之前拦下未登录请求，才能避免托管形态静默退化成一个无账号的本地会话。
 * 探测结果为"本地形态"时本组件等同于透传 children，桌面行为不变。
 */
export function HostedAuthGate({ children }: { children: ReactNode }) {
    const [phase, setPhase] = useState<HostedAuthGatePhase | null>(null);

    // 平台模型目录跟着登录态走：未登录/本地形态时这个 hook 自己空转。
    useHostedSystemModels();

    useEffect(() => {
        let cancelled = false;
        void (async () => {
            try {
                const methods = await detectHostedAuth();
                const session = methods === null ? null : ((await getHostedAuthSession())?.user ?? null);
                if (cancelled) return;
                setPhase(resolveHostedAuthGatePhase({ methods, session }));
            } catch (error) {
                // 保持加载态：既不放行成无鉴权入口，也不误判成本地形态。
                console.error("hosted auth probe failed", error);
            }
        })();
        return () => {
            cancelled = true;
        };
    }, []);

    const handleAuthenticated = useCallback((user: HostedAuthUser) => {
        setPhase({ phase: "authenticated", user });
    }, []);

    if (!phase) return <FullScreenLoader label="正在检查登录状态" detail="准备账号会话" />;
    if (phase.phase === "local" || phase.phase === "authenticated") return <>{children}</>;
    return <HostedAuthLoginPage methods={phase.methods} onAuthenticated={handleAuthenticated} />;
}
