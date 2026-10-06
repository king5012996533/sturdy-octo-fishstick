/**
 * 为公开页生成静态 HTML，并输出 sitemap.xml。
 *
 * 为什么需要这一步：站点是纯客户端渲染的 SPA，爬虫拿到的 index.html 只有「正在加载」
 * 和一个空的 #root，正文一个字都没有。模型介绍页是获客入口，抓不到内容等于没写。
 *
 * 为什么在构建产物里生成、而不是让后端渲染：nginx 的
 * `try_files $uri $uri/index.html /index.html` 已经把 /models/<slug> 指向
 * dist/models/<slug>/index.html。生成静态文件不用改 nginx，也不用给后端加一条与 API
 * 无关的路由。代价是后台改了广场文案要重跑一次本脚本（它不需要重新构建：产物里的
 * 页面内容全部来自接口）。
 *
 * 用法（需要先 `bun run build:hosted`）：
 *
 *     cd web && bun scripts/prerender-seo.mjs
 *     cd web && VITE_API_PROXY_TARGET=http://127.0.0.1:8080 bun scripts/prerender-seo.mjs
 *
 * 任何一页渲染不出正文都会让脚本以非零码退出——宁可少生成，也不要写出一个空页面
 * 让搜索引擎记成"这个站没内容"。
 */
import { spawn } from "node:child_process";
import { existsSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const distDir = resolve(webDir, "dist");
const apiTarget = (process.env.VITE_API_PROXY_TARGET || "").trim() || "https://kinotv.xingtudesign.com";
const baseURL = ((process.env.SEO_BASE_URL || "").trim() || "https://kinotv.xingtudesign.com").replace(/\/+$/, "");

const STARTUP_TIMEOUT_MS = 120_000;
const PAGE_TIMEOUT_MS = 60_000;
/**
 * 正文长度的下限，只用来兜住"渲染成了空壳"。
 *
 * 定得刻意低：参数少、自述还没写的模型（比如配乐类）整页正文本来就只有一百多字，
 * 那是有内容的一页。真正判断"渲染对了没有"的是标题比对，不是字数。
 */
const MIN_CONTENT_CHARS = 120;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function freePort() {
    return new Promise((resolve_, reject) => {
        const server = createServer();
        server.on("error", reject);
        server.listen(0, "127.0.0.1", () => {
            const { port } = server.address();
            server.close(() => resolve_(port));
        });
    });
}

async function launchPreview(port) {
    const child = spawn("bunx", ["vite", "preview", "--host", "127.0.0.1", "--port", String(port), "--strictPort"], {
        cwd: webDir,
        env: { ...process.env, VITE_API_PROXY_TARGET: apiTarget },
        stdio: ["ignore", "pipe", "pipe"],
    });
    let log = "";
    child.stdout.on("data", (d) => (log += d.toString()));
    child.stderr.on("data", (d) => (log += d.toString()));

    const deadline = Date.now() + STARTUP_TIMEOUT_MS;
    while (Date.now() < deadline) {
        if (child.exitCode !== null) throw new Error(`vite preview 提前退出（code ${child.exitCode}）：\n${log}`);
        try {
            const res = await fetch(`http://127.0.0.1:${port}/models`, { headers: { Accept: "text/html" } });
            if (res.ok) return child;
        } catch {
            // 还没起来
        }
        await sleep(500);
    }
    throw new Error(`vite preview 在 ${STARTUP_TIMEOUT_MS / 1000}s 内没有就绪：\n${log}`);
}

async function stop(child) {
    if (!child) return;
    const stopped = () => child.exitCode !== null || child.signalCode !== null;
    if (stopped()) return;
    child.kill("SIGTERM");
    const deadline = Date.now() + 8000;
    while (Date.now() < deadline && !stopped()) await sleep(200);
    if (!stopped()) child.kill("SIGKILL");
}

function xmlEscape(value) {
    return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function writePage(relativePath, html) {
    const target = resolve(distDir, relativePath);
    mkdirSync(dirname(target), { recursive: true });
    writeFileSync(target, html, "utf8");
    return target;
}

if (!existsSync(resolve(distDir, "index.html"))) {
    throw new Error(`没有找到 ${distDir}/index.html，先跑 bun run build:hosted`);
}

// 上次生成的页面必须先清掉：它们会被 vite preview 直接命中，抓到的就是上一轮的旧页面。
rmSync(resolve(distDir, "models"), { recursive: true, force: true });

const port = await freePort();
const origin = `http://127.0.0.1:${port}`;
let preview = null;
let browser = null;
const written = [];

try {
    preview = await launchPreview(port);

    const listResponse = await fetch(`${origin}/api/public/models`, { headers: { Accept: "application/json" } });
    if (!listResponse.ok) throw new Error(`取模型列表失败：HTTP ${listResponse.status}`);
    const listPayload = await listResponse.json();
    const models = listPayload?.data?.models ?? [];
    if (!models.length) throw new Error("模型列表为空，生成出来会是一个没有入口的目录页");

    browser = await chromium.launch({ headless: true, channel: "chrome" });
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "zh-CN" });

    const targets = [
        { path: "/models", file: "models/index.html", ready: ".doc-list-row, .doc-muted", heading: "模型", rows: models.length },
        ...models.map((model) => ({ path: `/models/${model.slug}`, file: `models/${model.slug}/index.html`, ready: "article.doc", heading: model.displayName })),
    ];

    for (const target of targets) {
        const page = await context.newPage();
        try {
            await page.goto(`${origin}${target.path}`, { waitUntil: "domcontentloaded", timeout: PAGE_TIMEOUT_MS });
            await page.waitForSelector(target.ready, { timeout: PAGE_TIMEOUT_MS });
            await page.waitForTimeout(400);

            const title = await page.title();
            // canonical 由应用按 location.origin 写死，抓取时的 origin 是本地预览端口，
            // 直接发布出去等于告诉搜索引擎"正本是 127.0.0.1"。这里只改这一处，
            // 不做整串替换——页面上其它绝对地址（Vite 注入的 preload 链接）应当保持
            // 相对路径，否则产物就被钉死在某一个域名上。
            const canonical = `${baseURL}${target.path}`;
            await page.evaluate((href) => {
                const link = document.querySelector('link[rel="canonical"]');
                if (link) link.href = href;
            }, canonical);

            // 标题比对是这一步的核心断言：slug 拼错、模型被下架、接口返回 404 时页面
            // 会渲染成"模型不存在"，那种页面同样是"有正文"的，只有比对标题才发现得了。
            const heading = (await page.locator("h1.doc-title").first().innerText()).trim();
            if (heading !== target.heading) {
                throw new Error(`页面标题是「${heading}」，期望「${target.heading}」——渲染出来的不是这一页`);
            }
            if (target.rows !== undefined) {
                const rows = await page.locator(".doc-list-row").count();
                if (rows !== target.rows) {
                    throw new Error(`目录页有 ${rows} 个模型，接口返回 ${target.rows} 个——列表没有渲染完整`);
                }
            }
            const text = (await page.locator("main").innerText()).replace(/\s+/g, "");
            if (text.length < MIN_CONTENT_CHARS) {
                throw new Error(`正文只有 ${text.length} 字，疑似仍然渲染成空壳`);
            }

            // Vite 的预加载助手会把注入的 link 写成 `http://127.0.0.1:端口/static/…`
            // 这样的绝对地址，还原成站内相对路径：产物不该认识它是在哪台机器上生成的。
            const html = (await page.content()).replaceAll(`${origin}/`, "/");
            if (html.includes("127.0.0.1")) {
                throw new Error("产物里仍残留 127.0.0.1，绝对地址没有还原成相对路径");
            }
            if (!html.includes(`<link rel="canonical" href="${canonical}">`)) {
                throw new Error(`canonical 不是 ${canonical}，这页会被判成另一个地址的副本`);
            }

            const file = writePage(target.file, html);
            written.push({ path: target.path, file, title, chars: text.length, bytes: Buffer.byteLength(html) });
            console.log(`ok   ${target.path}  →  ${target.file}  「${title}」 ${text.length} 字 / ${Math.round(Buffer.byteLength(html) / 1024)} KB`);
        } finally {
            await page.close();
        }
    }
} finally {
    if (browser) await browser.close();
    await stop(preview);
}

const lastmod = new Date().toISOString().slice(0, 10);
const urls = [`${baseURL}/`, `${baseURL}/models`, ...written.filter((entry) => entry.path !== "/models").map((entry) => `${baseURL}${entry.path}`)];
const sitemap = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    ...urls.map((loc) => `  <url><loc>${xmlEscape(loc)}</loc><lastmod>${lastmod}</lastmod></url>`),
    "</urlset>",
    "",
].join("\n");
writeFileSync(resolve(distDir, "sitemap.xml"), sitemap, "utf8");

console.log(`\n共生成 ${written.length} 个静态页，sitemap.xml 收录 ${urls.length} 条（lastmod ${lastmod}）`);
console.log(`接口来源：${apiTarget}`);
console.log(`站点前缀：${baseURL}`);
