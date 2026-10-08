import { afterEach, describe, expect, test } from "bun:test";
import { AxiosError } from "axios";

import { apiClient } from "@/services/api/request";
import { getResourceBlob } from "@/services/api/resources";

const originalAdapter = apiClient.defaults.adapter;
afterEach(() => {
    apiClient.defaults.adapter = originalAdapter;
});

// 必须在实例上换 adapter：apiClient 是 axios.create 出来的独立实例，改 axios.defaults 对它无效，
// 否则请求会落到真实 http adapter 上（测试里会因无 baseURL 直接报 Invalid URL）。
// 非 2xx 必须显式抛 AxiosError：自定义 adapter 直接返回响应会绕过 axios 的 settle，
// 那样 401/500 会被当成成功，测试就失去意义。
function stubStatus(status: number, data: unknown = new Blob(["x"])) {
    apiClient.defaults.adapter = async (config) => {
        const response = { config, data, status, statusText: status === 200 ? "OK" : "Error", headers: {} } as never;
        if (status >= 200 && status < 300) return response;
        throw new AxiosError(`Request failed with status code ${status}`, String(status), config as never, {}, response);
    };
}

describe("getResourceBlob 的错误语义", () => {
    test("资源不存在时返回 null（调用方把 null 当作没有可物化字节）", async () => {
        stubStatus(404);
        expect(await getResourceBlob("resource:missing")).toBeNull();
    });

    test("会话失效时抛错，而不是伪装成素材丢失", async () => {
        stubStatus(401);
        await expect(getResourceBlob("resource:owned")).rejects.toBeInstanceOf(Error);
    });

    test("无权限时抛错", async () => {
        stubStatus(403);
        await expect(getResourceBlob("resource:owned")).rejects.toBeInstanceOf(Error);
    });

    test("服务端错误时抛错", async () => {
        stubStatus(500);
        await expect(getResourceBlob("resource:owned")).rejects.toBeInstanceOf(Error);
    });

    test("成功时返回 Blob", async () => {
        stubStatus(200, new Blob(["bytes"], { type: "image/png" }));
        const blob = await getResourceBlob("resource:owned");
        expect(blob).toBeInstanceOf(Blob);
        expect(await blob!.text()).toBe("bytes");
    });

    test("非法 storageKey 直接返回 null，不发请求", async () => {
        let called = 0;
        apiClient.defaults.adapter = async (config) => { called += 1; return { config, data: new Blob(["x"]), status: 200, statusText: "OK", headers: {} }; };
        expect(await getResourceBlob("not-a-resource-key")).toBeNull();
        expect(called).toBe(0);
    });
});
