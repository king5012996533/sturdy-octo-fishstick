import { isHostedBuild } from "@/lib/hosted-build";

/**
 * 平台级模型配置在托管形态下只有管理员能写。
 *
 * 这份配置整个进程共用一份，普通账号写入会被后端 403。前端如果照旧提交，用户只会
 * 看到反复的"保存失败"，所以读写两侧的判断收在这一个函数里，避免以后只改一边。
 * 读取不受影响：普通账号拿到的是后端裁剪过的可用模型目录，前端仍要用它填模型下拉。
 */
export function canPersistModelConfig(role: "admin" | "user" | null | undefined) {
    if (!isHostedBuild()) return true;
    return role === "admin";
}
