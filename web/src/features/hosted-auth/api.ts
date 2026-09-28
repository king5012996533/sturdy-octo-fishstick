import { ApiError, http } from "@/services/api/request";

/**
 * 托管登录（SaaS）接口。这一层只负责调用后端 /api/auth/*，不 import 任何本地工作区模块：
 * 本地/桌面形态下这些路由根本不存在，调用方通过 detectHostedAuth 判断形态。
 */

export type HostedAuthMethodType = "EMAIL_CODE" | "GITHUB_OAUTH" | "PHONE_CODE" | "PASSWORD" | "ADMIN_PASSWORD" | (string & {});

export type HostedAuthMethod = {
    methodType: HostedAuthMethodType;
    category: string;
    displayName: string;
    description: string;
    iconType: string;
    sortOrder: number;
    allowSignUp: boolean;
};

export type HostedAuthUser = {
    id: string;
    name: string;
    email: string;
    avatarUrl: string;
    role: string;
    status: string;
};

export type HostedAuthCodeChallenge = {
    target: string;
    channel: string;
    expiresAt: string;
    cooldownSeconds: number;
    /**
     * 仅本地联调回显：后端没有配 SMTP 且显式打开了开发回显时才会带这个字段，
     * 配了真实投递通道的生产环境不会出现。
     */
    devCode?: string;
};

/**
 * 会话里的协议状态。
 *
 * accepted=false 表示当前账号签署的是旧版本，需要在进入工作台前重新同意一次：
 * 强制重签必须由服务端判定，前端自己比版本号迟早会漂移。
 */
export type HostedAuthAgreementState = {
    currentVersion: string;
    acceptedVersion: string;
    accepted: boolean;
};

export type HostedAuthSessionPayload = {
    user: HostedAuthUser | null;
    agreements?: HostedAuthAgreementState | null;
};

export type HostedAuthAgreementDocument = {
    type: string;
    title: string;
    body: string;
};

export type HostedAuthAgreements = {
    version: string;
    documents: HostedAuthAgreementDocument[];
};

/**
 * 探测是否处于托管形态。
 *
 * 返回 null 表示这是本地/桌面形态（后端没有注册登录路由，会直接 404），
 * 调用方据此完全跳过登录门；只有 404 才当作"未启用"，其他错误照常抛出，
 * 避免后端故障时静默降级成无鉴权入口。
 */
export async function detectHostedAuth(): Promise<HostedAuthMethod[] | null> {
    try {
        const payload = await http.get<{ methods: HostedAuthMethod[] }>("/auth/methods");
        return payload?.methods ?? [];
    } catch (error) {
        if (error instanceof ApiError && error.status === 404) return null;
        throw error;
    }
}

export function getHostedAuthSession() {
    return http.get<HostedAuthSessionPayload>("/auth/session");
}

export function sendHostedAuthCode(methodType: HostedAuthMethodType, target: string) {
    return http.post<HostedAuthCodeChallenge>("/auth/verification-code", { methodType, target });
}

/**
 * 登录：验证码通道传 code，密码通道传 password。
 *
 * 两条通道共用一个入口是服务端的既有契约（由 methodType 决定读哪个字段），前端
 * 不再分叉成两个函数，避免两边各自漂移。
 */
export function loginHostedAuth(input: { methodType: HostedAuthMethodType; target: string; code?: string; password?: string }) {
    return http.post<{ user: HostedAuthUser; expiresAt: string }>("/auth/login", input);
}

/**
 * 拉取当前生效的协议版本与正文。
 *
 * 版本必须由服务端下发而不是前端写死：注册时要把它原样回传，服务端据此判断用户
 * 同意的是哪一版。前端自带的版本号一旦滞后，用户同意旧条款、落库的却是新版本。
 */
export function fetchHostedAuthAgreements() {
    return http.get<HostedAuthAgreements>("/auth/agreements");
}

export function registerHostedAuth(input: { methodType: HostedAuthMethodType; target: string; code?: string; password?: string; agreementVersion: string }) {
    return http.post<{ user: HostedAuthUser; expiresAt: string }>("/auth/register", input);
}

/**
 * 重新同意当前版本协议。
 *
 * 版本号由服务端下发，回传的是服务端认为的当前版本；服务端会再比对一次，避免
 * 停留在旧页面的用户把过期版本写进留痕。
 */
export function acceptHostedAuthAgreements(version: string) {
    return http.post<{ accepted: boolean }>("/auth/agreements/accept", { version });
}

export function logoutHostedAuth() {
    return http.post<{ success: boolean }>("/auth/logout", {});
}

export function requestHostedOAuthAuthorize(input: { methodType: HostedAuthMethodType; redirectUri: string }) {
    return http.post<{ authUrl: string; state: string }>("/auth/oauth/authorize", input);
}

/** 用回调参数换取会话；成功后会话 Cookie 由后端下发。 */
export function completeHostedOAuthCallback(input: { methodType: HostedAuthMethodType; code: string; state: string; redirectUri: string }) {
    return http.get<{ user: HostedAuthUser; expiresAt: string }>("/auth/oauth/callback", {
        params: {
            methodType: input.methodType,
            code: input.code,
            state: input.state,
            redirectUri: input.redirectUri,
        },
    });
}
