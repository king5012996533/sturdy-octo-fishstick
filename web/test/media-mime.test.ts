import { describe, expect, test } from "bun:test";

import { normalizeMediaMimeType } from "@/lib/media-mime";

describe("normalizeMediaMimeType", () => {
    test("上游返回传输层占位类型时收敛到本管线容器", () => {
        expect(normalizeMediaMimeType("binary/octet-stream", "video")).toBe("video/mp4");
        expect(normalizeMediaMimeType("BINARY/OCTET-STREAM", "audio")).toBe("audio/mpeg");
        expect(normalizeMediaMimeType("binary/octet-stream", "image")).toBe("image/png");
    });

    test("缺省或空值走同一条兜底，不把占位类型当成具体类型", () => {
        expect(normalizeMediaMimeType(undefined, "video")).toBe("video/mp4");
        expect(normalizeMediaMimeType("  ", "audio")).toBe("audio/mpeg");
    });

    test("保留正确类型与合同允许的诚实未知类型", () => {
        expect(normalizeMediaMimeType("video/webm", "video")).toBe("video/webm");
        expect(normalizeMediaMimeType("image/jpeg", "image")).toBe("image/jpeg");
        expect(normalizeMediaMimeType("application/octet-stream", "video")).toBe("application/octet-stream");
    });

    test("具体但跨类型的值原样保留，交给素材合同拒绝", () => {
        expect(normalizeMediaMimeType("image/webp", "video")).toBe("image/webp");
        expect(normalizeMediaMimeType("video/mp4", "audio")).toBe("video/mp4");
    });
});
