/** 比较用的端点身份：协议 + 主机 + 端口。 */
type UpstreamEndpoint = {
    protocol: string;
    hostname: string;
    port: string;
};

/**
 * 解析出端点身份；缺协议或主机（`data:`、`javascript:`、纯相对路径这类）视为无法比较。
 *
 * URL 会把默认端口规范化成空串，所以 `https://a:443` 与 `https://a` 在这里相等，而非默认
 * 端口（8443）会保留下来，不会被误判成同一个端点。
 */
function upstreamEndpoint(url: string | undefined): UpstreamEndpoint | null {
    try {
        const parsed = new URL((url || "").trim());
        if (!parsed.protocol || !parsed.hostname) return null;
        return { protocol: parsed.protocol.toLowerCase(), hostname: parsed.hostname.toLowerCase(), port: parsed.port };
    } catch {
        return null;
    }
}

/**
 * 归一化 URL 主机名；无法解析时返回空串。
 */
export function upstreamHostname(url: string | undefined): string {
    return upstreamEndpoint(url)?.hostname || "";
}

/**
 * 目标地址是否与渠道自己配置的上游同源（协议 + 主机 + 端口三者都一致）。
 *
 * 用来判断「能不能把渠道凭据发给这个地址」。上游返回的资源地址必须回到渠道自己配置的
 * 端点才附带密钥；一旦上游返回第三方地址就不带——否则一个自定义渠道只要在响应里塞一条
 * 构造的 uri，就能把用户的 API Key 骗到自己的服务器上。
 *
 * 只比主机名是不够的：`http://同主机/x` 会让密钥走明文，`https://同主机:8443/x` 会把密钥
 * 发到同主机的另一个服务上，两者都算另一个端点。
 *
 * baseUrl 缺失或非法时返回 false（fail closed）：宁可下载失败报错，也不静默泄漏密钥。
 */
export function isSameUpstreamHost(baseUrl: string | undefined, target: string | undefined): boolean {
    const base = upstreamEndpoint(baseUrl);
    const requested = upstreamEndpoint(target);
    if (!base || !requested) return false;
    return requested.hostname === base.hostname
        && requested.protocol === base.protocol
        && requested.port === base.port;
}
