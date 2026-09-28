import { describe, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { creationFeaturedWorks, inspirationSource } from "../src/pages/create/creation-inspirations";
import { creationLibtvInspirations, libtvSampleSource } from "../src/pages/create/creation-inspirations-libtv";

describe("curated creation inspirations", () => {
    test("all templates have unique titles, usable prompts and local cover assets", () => {
        expect(creationFeaturedWorks.length).toBe(22);
        expect(new Set(creationFeaturedWorks.map((item) => item.title)).size).toBe(22);
        for (const item of creationFeaturedWorks) {
            expect(["image", "video", "text"]).toContain(item.mode);
            expect(item.prompt.length).toBeGreaterThan(35);
            expect(existsSync(resolve(import.meta.dir, "../public", item.image.slice(1)))).toBe(true);
        }
    });
    test("adapted prompts retain provenance and the data license", () => {
        expect(creationFeaturedWorks.filter((item) => item.source).length).toBe(8);
        expect(inspirationSource.license).toBe("CC0-1.0");
        expect(inspirationSource.revision).toMatch(/^[a-f0-9]{40}$/);
        expect(inspirationSource.notice).toContain("不代表实际生成结果");
    });
});

describe("LibTV sample inspirations", () => {
    test("每条示例素材都能直接使用：远程封面、可追溯来源、非空提示词", () => {
        expect(creationLibtvInspirations.length).toBeGreaterThan(20);
        expect(new Set(creationLibtvInspirations.map((item) => item.title)).size).toBe(creationLibtvInspirations.length);
        for (const item of creationLibtvInspirations) {
            expect(["image", "video", "text"]).toContain(item.mode);
            expect(item.prompt.length).toBeGreaterThan(10);
            // 封面仍在对方 CDN 上（两个域名都是 LibTV 自己的），必须带缩略参数，否则单张 1.7MB。
            expect(/^https:\/\/(libtv-res\.liblib\.art|liblibai-online\.liblib\.cloud)\//.test(item.image)).toBe(true);
            expect(item.image).toContain("x-oss-process=image/resize,w_960");
            expect(item.sourceUrl).toMatch(/^https:\/\/www\.liblib\.tv\/detail\/[a-f0-9]+$/);
            expect(item.author?.length).toBeGreaterThan(0);
        }
    });
    test("示例素材与原创列表分开维护，且不冒充原创或 CC0", () => {
        const curated = new Set(creationFeaturedWorks.map((item) => item.title));
        for (const item of creationLibtvInspirations) {
            expect(curated.has(item.title)).toBe(false);
            // 示例素材没有 source（那是"开源改编 · CC0"的标记），避免页脚把它标成 CC0。
            expect(item.source).toBeUndefined();
        }
        expect(libtvSampleSource.notice).toContain("上线前");
        expect(libtvSampleSource.site).toContain("liblib.tv");
    });
});
