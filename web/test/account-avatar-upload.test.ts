import { afterEach, describe, expect, test } from "bun:test";

import { AVATAR_MAX_BYTES, validateAvatarFile } from "../src/pages/settings/account-avatar-uploader";
import { clearAccountAvatar, uploadAccountAvatar } from "../src/services/api/account-profile";
import { apiClient } from "../src/services/api/request";

const originalAdapter = apiClient.defaults.adapter;

afterEach(() => {
    apiClient.defaults.adapter = originalAdapter;
});

type Captured = { url?: string; method?: string; data?: unknown; timeout?: number };

function captureRequest(payload: unknown) {
    const captured: Captured[] = [];
    apiClient.defaults.adapter = async (config) => {
        captured.push({ url: config.url, method: config.method, data: config.data, timeout: config.timeout });
        return { data: { code: 0, data: payload, msg: "ok" }, status: 200, statusText: "", headers: {}, config } as never;
    };
    return captured;
}

const profile = { userId: "u1", name: "光启云科", avatarUrl: "https://example.com/api/public/avatars/u1?v=1", email: "", phone: "", hasPassword: true };

function uploadOf(name: string, type: string, size: number) {
    return new File([new Uint8Array(size)], name, { type });
}

describe("头像上传", () => {
    test("选文件前先按服务端同一套规则挡一次", () => {
        expect(validateAvatarFile(uploadOf("a.png", "image/png", 1024))).toBe("");
        expect(validateAvatarFile(uploadOf("a.jpg", "image/jpeg", 1024))).toBe("");
        expect(validateAvatarFile(uploadOf("a.webp", "image/webp", 1024))).toBe("");
        // 超限与格式不对都在本地就说清楚，不让用户等一次往返之后再收到同一句话。
        expect(validateAvatarFile(uploadOf("a.png", "image/png", AVATAR_MAX_BYTES + 1))).toContain("2MB");
        expect(validateAvatarFile(uploadOf("a.gif", "image/gif", 1024))).toContain("PNG");
        expect(validateAvatarFile(uploadOf("a.svg", "image/svg+xml", 1024))).toContain("PNG");
        // 系统没上报类型时不拦：放行给服务端嗅探，比在这里猜错更好。
        expect(validateAvatarFile(uploadOf("a", "", 1024))).toBe("");
    });

    test("上传走 multipart，字段名与后端 c.FormFile(\"file\") 对齐", async () => {
        const captured = captureRequest(profile);
        const file = uploadOf("me.png", "image/png", 32);
        const result = await uploadAccountAvatar(file);

        expect(captured[0]?.url).toBe("/finance/account/avatar");
        expect(captured[0]?.method).toBe("post");
        const body = captured[0]?.data as FormData;
        expect(body).toBeInstanceOf(FormData);
        const sent = body.get("file") as File;
        expect(sent.name).toBe("me.png");
        expect(sent.type).toBe("image/png");
        expect(sent.size).toBe(32);
        // 默认那 4 秒是给 JSON 请求定的：一张 2MB 的图在弱网下会先超时再被判失败，
        // 而服务端其实已经收下了。
        expect(captured[0]?.timeout).toBeGreaterThan(10_000);
        expect(result.avatarUrl).toBe(profile.avatarUrl);
    });

    test("移除头像用 DELETE，回落的资料视图里没有头像地址", async () => {
        const captured = captureRequest({ ...profile, avatarUrl: "" });
        const result = await clearAccountAvatar();
        expect(captured[0]?.url).toBe("/finance/account/avatar");
        expect(captured[0]?.method).toBe("delete");
        expect(result.avatarUrl).toBe("");
    });
});
