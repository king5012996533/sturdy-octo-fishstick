import { http } from "@/services/api/request";

/**
 * 用户中心「身份绑定」接口。
 *
 * 换绑分两步：先给**新地址**发验证码，再带着码确认。中间没有"直接改"的捷径——
 * 少了新地址这一步，一次手误就能把账号绑到一个永远收不到验证码的邮箱上。
 */

export type AccountBinding = {
    methodType: string;
    /** 登录方式的中文名，由服务端给出，前端不再维护第二份映射表。 */
    label: string;
    identifier: string;
    verified: boolean;
    boundAt?: string;
    /** 是否同时是账号的联系地址（验证码登录与安全通知走它）。 */
    primary: boolean;
};

export type AccountBindings = {
    email: string;
    phone: string;
    bindings: AccountBinding[];
};

export type BindingCodeResult = {
    channel: string;
    target: string;
    expiresAt: string;
    cooldown: number;
    /** 仅在本地投递通道下回显，生产为空。 */
    devCode?: string;
};

export async function getAccountBindings() {
    return http.get<AccountBindings>("/finance/account/bindings");
}

export async function sendBindingCode(input: { channel: "EMAIL" | "PHONE"; target: string }) {
    return http.post<BindingCodeResult>("/finance/account/bindings/code", input);
}

export async function confirmBinding(input: { channel: "EMAIL" | "PHONE"; target: string; code: string }) {
    return http.post<AccountBindings>("/finance/account/bindings", input);
}
