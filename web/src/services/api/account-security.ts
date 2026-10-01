import { http } from "@/services/api/request";

/**
 * 用户中心「账号安全」接口：密码与登录设备。
 *
 * 两个主题放同一个文件，是因为它们共享同一条产品规则——任何一次凭据变更都以
 * 「保留当前设备、吊销其余」收尾。分到两个文件里，这条规则就会在两侧各写一遍。
 */

export type PasswordState = {
    hasPassword: boolean;
    /** 长度上下限由服务端下发，表单不硬编码——写死在界面上就会和服务端脱钩。 */
    minLength: number;
    maxLength: number;
};

export type PasswordUpdateResult = {
    state: PasswordState;
    /** 本次顺带吊销的其他设备会话数。 */
    revokedSessions: number;
};

export type AccountSession = {
    id: string;
    current: boolean;
    methodType: string;
    /** 登录标识（脱敏后的邮箱/手机号）。 */
    identifier: string;
    ipAddress: string;
    userAgent: string;
    /** 服务端归纳出的可读设备描述，如「Chrome · macOS」。 */
    device: string;
    createdAt: string;
    lastActiveAt?: string;
    expiresAt: string;
};

export async function getPasswordState() {
    return http.get<PasswordState>("/finance/account/password");
}

/** 首次设置密码：身份由账号绑定的邮箱/手机号验证码证明。 */
export async function setAccountPassword(input: { methodType: string; code: string; newPassword: string }) {
    return http.post<PasswordUpdateResult>("/finance/account/password", input);
}

/** 修改密码：身份由当前密码证明。 */
export async function changeAccountPassword(input: { currentPassword: string; newPassword: string }) {
    return http.put<PasswordUpdateResult>("/finance/account/password", input);
}

export async function listAccountSessions() {
    const payload = await http.get<{ sessions: AccountSession[] }>("/finance/account/sessions");
    return payload.sessions;
}

export async function revokeAccountSession(sessionId: string) {
    const payload = await http.delete<{ sessions: AccountSession[] }>(`/finance/account/sessions/${encodeURIComponent(sessionId)}`);
    return payload.sessions;
}

/** 下线除当前设备外的全部会话，返回剩余列表与本次吊销条数。 */
export async function revokeOtherAccountSessions() {
    return http.post<{ sessions: AccountSession[]; revoked: number }>("/finance/account/sessions/revoke-others");
}
