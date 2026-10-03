import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { CreationInspirationCover } from "../src/pages/create/creation-inspiration-cover";

/**
 * 广场一次全出 58 条之后，封面不能再靠浏览器的 loading="lazy" —— 它会在快速网络下
 * 一口气把 58 张（约 6.7MB）全下完。这些用例钉住"进视口才挂 src"这条边界。
 */

describe("广场封面", () => {
    test("首帧不挂 src：视口外的卡片不该发请求", () => {
        const html = renderToStaticMarkup(<CreationInspirationCover src="https://cdn.example.com/a.jpg" alt="" loading="lazy" />);
        expect(html).not.toContain("cdn.example.com");
        expect(html).not.toContain("src=");
    });

    test("eager 例外：主推荐位要立刻出图，不受视口门控", () => {
        const html = renderToStaticMarkup(<CreationInspirationCover src="https://cdn.example.com/hero.jpg" alt="" eager />);
        expect(html).toContain('src="https://cdn.example.com/hero.jpg"');
    });

    test("透传 alt、loading 与 referrerPolicy：来源外链的防盗链口径不能在这里丢掉", () => {
        const html = renderToStaticMarkup(<CreationInspirationCover src="https://cdn.example.com/b.jpg" alt="" loading="lazy" referrerPolicy="no-referrer" />);
        expect(html).toContain('loading="lazy"');
        expect(html).toContain('referrerPolicy="no-referrer"');
    });
});
