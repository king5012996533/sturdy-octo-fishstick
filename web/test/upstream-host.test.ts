import { describe, expect, test } from "bun:test";

import { isSameUpstreamHost, upstreamHostname } from "@/lib/upstream-host";

describe("upstreamHostname", () => {
    test("归一化为小写主机名并忽略路径与端口", () => {
        expect(upstreamHostname("https://GenerativeLanguage.GoogleAPIs.com/v1beta/files")).toBe("generativelanguage.googleapis.com");
        expect(upstreamHostname("https://example.test:8443/a/b?c=1")).toBe("example.test");
    });

    test("无法解析时返回空串，而不是抛错", () => {
        expect(upstreamHostname("")).toBe("");
        expect(upstreamHostname("not a url")).toBe("");
        expect(upstreamHostname(undefined)).toBe("");
    });
});

describe("isSameUpstreamHost", () => {
    test("同主机判为真，路径与端口无关", () => {
        expect(isSameUpstreamHost("https://api.test", "https://api.test/v1/x")).toBe(true);
        expect(isSameUpstreamHost("https://api.test:443", "https://api.test/other")).toBe(true);
        expect(isSameUpstreamHost("HTTPS://API.TEST", "https://api.test/x")).toBe(true);
    });

    test("不同主机判为假", () => {
        expect(isSameUpstreamHost("https://api.test", "https://attacker.invalid/x")).toBe(false);
    });

    test("子域不算同源", () => {
        expect(isSameUpstreamHost("https://api.test", "https://api.test.attacker.invalid/x")).toBe(false);
        expect(isSameUpstreamHost("https://api.test", "https://evil-api.test/x")).toBe(false);
    });

    test("协议降级不算同源，避免密钥走明文", () => {
        expect(isSameUpstreamHost("https://api.test", "http://api.test/x")).toBe(false);
        expect(isSameUpstreamHost("http://api.test", "https://api.test/x")).toBe(false);
    });

    test("同主机的非默认端口算另一个端点", () => {
        expect(isSameUpstreamHost("https://api.test", "https://api.test:8443/x")).toBe(false);
        expect(isSameUpstreamHost("https://api.test:8443", "https://api.test/x")).toBe(false);
        expect(isSameUpstreamHost("https://api.test:8443", "https://api.test:8443/x")).toBe(true);
    });

    test("自建 http 渠道仍可下载，只要协议主机端口都对得上", () => {
        expect(isSameUpstreamHost("http://127.0.0.1:8080", "http://127.0.0.1:8080/v1beta/files/x:download")).toBe(true);
    });

    test("baseUrl 缺失或非法时失败关闭", () => {
        expect(isSameUpstreamHost("", "https://api.test/x")).toBe(false);
        expect(isSameUpstreamHost(undefined, "https://api.test/x")).toBe(false);
        expect(isSameUpstreamHost("not a url", "https://api.test/x")).toBe(false);
    });

    test("目标非法时判为假", () => {
        expect(isSameUpstreamHost("https://api.test", "")).toBe(false);
        expect(isSameUpstreamHost("https://api.test", "javascript:alert(1)")).toBe(false);
    });
});
