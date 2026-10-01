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

/**
 * 上传头像图片。
 *
 * 走上传而不是让用户填地址：头像字段里的地址由服务端签发（见后端 platformAvatarURL），
 * 用户填的外链迟早会因为对方删图、防盗链或换域名变成一排破图，而这一页的头像同时出现
 * 在侧栏、广场卡片和评论里。
 *
 * 超时单独放宽：默认那 4 秒是给 JSON 请求定的，一张 2MB 的图在弱网下会先超时再被判失败，
 * 而服务端其实已经收下了——用户看到"上传失败"，刷新之后头像却变了。
 */
export async function uploadAccountAvatar(file: File) {
    const form = new FormData();
    form.append("file", file);
    return http.post<AccountProfile>("/finance/account/avatar", form, { timeout: 60_000 });
}

/** 清除头像，界面回落到昵称首字母。 */
export async function clearAccountAvatar() {
    return http.delete<AccountProfile>("/finance/account/avatar");
}
