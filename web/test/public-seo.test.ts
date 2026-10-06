import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "../..");

/**
 * 公开页能不能被搜索到，靠的是三样东西同时成立：robots.txt 真的存在、sitemap 指向
 * 的域名和生成脚本一致、落地页的正文在静态 HTML 里。任何一样单独丢掉都不会报错，
 * 只会安静地少掉收录，所以在测试里钉住。
 */
describe("公开页的搜索引擎可见性", () => {
    test("robots.txt 是真实文件，放行公开页并声明 sitemap", () => {
        const robots = readFileSync(resolve(root, "web/public/robots.txt"), "utf8");

        expect(robots).toContain("User-agent: *");
        expect(robots).toContain("Allow: /");
        expect(robots).toContain("Disallow: /api/");
        const sitemap = robots.match(/Sitemap:\s*(\S+)/)?.[1];
        expect(sitemap).toBeDefined();
        expect(sitemap?.endsWith("/sitemap.xml")).toBe(true);
    });

    test("生成脚本与 robots.txt 指向同一个站点前缀", () => {
        const script = readFileSync(resolve(root, "web/scripts/prerender-seo.mjs"), "utf8");
        const robots = readFileSync(resolve(root, "web/public/robots.txt"), "utf8");
        const sitemap = robots.match(/Sitemap:\s*(\S+)\/sitemap\.xml/)?.[1];

        expect(sitemap).toBeDefined();
        // 脚本默认值写死在这里；两处不一致时，提交给搜索引擎的 sitemap 会指向另一个域名。
        expect(script).toContain(`"${sitemap}"`);
    });

    test("首页在静态 HTML 里就有标题与描述，不能停留在加载占位", () => {
        const html = readFileSync(resolve(root, "web/index.html"), "utf8");
        const title = html.match(/<title>([^<]*)<\/title>/)?.[1] ?? "";
        const description = html.match(/<meta name="description" content="([^"]*)"/)?.[1] ?? "";

        expect(title).not.toContain("正在加载");
        expect(title).toContain("KinoTV");
        expect(description.length).toBeGreaterThan(10);
    });

    test("生成脚本挂在构建流程里，产物目录由它自己清理", () => {
        const packageJson = JSON.parse(readFileSync(resolve(root, "web/package.json"), "utf8")) as {
            scripts?: Record<string, string>;
        };
        expect(packageJson.scripts?.["seo:prerender"]).toBe("bun scripts/prerender-seo.mjs");
        expect(existsSync(resolve(root, "web/scripts/prerender-seo.mjs"))).toBe(true);

        const script = readFileSync(resolve(root, "web/scripts/prerender-seo.mjs"), "utf8");
        // 上一次生成的页面必须先删掉，否则预览服务会命中旧文件，抓回来的就是上一轮的内容。
        expect(script).toContain('rmSync(resolve(distDir, "models"), { recursive: true, force: true })');
        expect(script).toContain("writeFileSync(resolve(distDir, \"sitemap.xml\")");
    });
});
