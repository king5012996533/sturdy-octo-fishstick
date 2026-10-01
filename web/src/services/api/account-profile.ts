import { http } from "@/services/api/request";

/**
 * 用户中心「个人资料」接口。
 *
 * 与 /finance/account（只读总览）分开：那个接口要合并用量与计费口径，这一个只答
 * "我叫什么、我的头像是什么"。合成一个之后，改一次昵称就要顺带重算一遍 30 天用量。
 */

export type AccountProfile = {
    userId: string;
    name: string;
    /** 为空表示没有头像，界面回落到首字母占位。 */
    avatarUrl: string;
    email: string;
    phone: string;
    /** 是否已设置密码；决定密码卡片走「设置」还是「修改」分支。 */
    hasPassword: boolean;
};

export async function getAccountProfile() {
    return http.get<AccountProfile>("/finance/account/profile");
}

export async function updateAccountProfile(input: { name: string; avatarUrl: string }) {
    return http.patch<AccountProfile>("/finance/account/profile", input);
}
