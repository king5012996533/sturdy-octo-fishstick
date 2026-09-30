export type MediaMimeKind = "image" | "video" | "audio";

const DEFAULT_MEDIA_MIME_TYPE: Record<MediaMimeKind, string> = {
    image: "image/png",
    video: "video/mp4",
    audio: "audio/mpeg",
};

/**
 * 生成结果里的 MIME 必须收敛成与素材种类一致的确定值：
 * 上游 CDN 常对 mp4 返回传输层占位类型（如 binary/octet-stream），或干脆不返回类型。
 * 这类值既不是具体媒体类型，也不是素材合同允许的 application/octet-stream，
 * 直接落库会让已经成功的结果被判成「mimeType 与视频类型不匹配」而丢失。
 */
export function normalizeMediaMimeType(mimeType: string | undefined | null, kind: MediaMimeKind): string {
    const value = (mimeType || "").trim().toLowerCase();
    if (value.startsWith(`${kind}/`)) return value;
    // application/octet-stream 是合同允许的「诚实的未知类型」，不篡改。
    if (value === "application/octet-stream") return value;
    // 缺省与传输层占位类型（*/octet-stream）都只说明「类型未知」，收敛到本管线容器；
    // 具体但与种类冲突的值原样保留，让素材合同明确拒绝，不在数据层掩盖上游错配。
    if (!value || /^[^/]+\/octet-stream$/.test(value)) return DEFAULT_MEDIA_MIME_TYPE[kind];
    return value;
}
