/**
 * 抓取上游 Replicate 的模型自述文件，写入 scripts/model-showcase-copy.json 的 upstream.readme。
 *
 * 为什么必须是浏览器而不是 curl：Replicate 的正文不在服务端 HTML 里，是页面加载后由
 * 客户端取的（我试过 /api/models/... 与 ?_data= 几个入口，全是 403/404）。所以这里用
 * Playwright 打开渲染后的页面，从 .readme-prose 里把 DOM 转成 Markdown。
 *
 * 为什么落在 web/scripts 而不是仓库根的 scripts：Playwright 装在 web/node_modules，
 * ESM 按文件位置解析依赖，脚本放根目录就 import 不到。
 *
 * 用法（默认只打印，不加 --write 不写任何东西）：
 *
 *     cd web && bun scripts/fetch-model-readme.mjs            # 预览抓到的正文首几行
 *     cd web && bun scripts/fetch-model-readme.mjs --write    # 写回内容文件
 *
 * 写完要人工把中文填进 entry.readme（这一步刻意不自动化：Replicate 只有英文，机器翻
 * 出来的中文读起来是机翻腔，而这一页是给内容创作者看的门面）。
 */

import { readFileSync, writeFileSync } from "node:fs";
import { chromium } from "playwright";

const COPY_PATH = new URL("../../scripts/model-showcase-copy.json", import.meta.url).pathname;
const WRITE = process.argv.includes("--write");

// 只处理上游实际用到的那几种行内标记，不做通用 HTML→Markdown 转换。
const TO_MARKDOWN = `(() => {
  const root = document.querySelector(".readme-prose");
  if (!root) return null;
  const inline = (node) => {
    let out = "";
    for (const child of node.childNodes) {
      if (child.nodeType === 3) { out += child.textContent.replace(/\\s+/g, " "); continue; }
      if (child.nodeType !== 1) continue;
      const tag = child.tagName.toLowerCase();
      const inner = inline(child);
      if (tag === "strong" || tag === "b") out += "**" + inner.trim() + "**";
      else if (tag === "em" || tag === "i") out += "_" + inner.trim() + "_";
      else if (tag === "code") out += "\`" + inner.trim() + "\`";
      else if (tag === "br") out += "\\n";
      else if (tag === "a") { const href = child.getAttribute("href") || ""; out += href && !href.startsWith("#") ? "[" + inner.trim() + "](" + href + ")" : inner; }
      else out += inner;
    }
    return out;
  };
  const blocks = [];
  const push = (line) => blocks.push(line);
  for (const el of root.children) {
    const tag = el.tagName.toLowerCase();
    if (["h1", "h2", "h3", "h4"].includes(tag)) {
      push("#".repeat(Number(tag[1])) + " " + inline(el).trim()); push("");
    } else if (tag === "ul" || tag === "ol") {
      let i = 1;
      for (const li of el.children) push((tag === "ol" ? (i++) + ". " : "- ") + inline(li).trim());
      push("");
    } else if (tag === "pre") {
      push("\`\`\`"); push(el.innerText.trim()); push("\`\`\`"); push("");
    } else if (tag === "hr") {
      push("---"); push("");
    } else {
      const text = inline(el).trim();
      if (text) { push(text); push(""); }
    }
  }
  return blocks.join("\\n").replace(/\\n{3,}/g, "\\n\\n").trim();
})()`;

const document_ = JSON.parse(readFileSync(COPY_PATH, "utf8"));
const targets = document_.models.filter((entry) => entry.upstream?.url);

const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ locale: "en-US" });
let changed = 0;

for (const entry of targets) {
    const page = await context.newPage();
    try {
        await page.goto(entry.upstream.url + "/readme", { waitUntil: "domcontentloaded", timeout: 60000 });
        await page.waitForSelector(".readme-prose", { timeout: 30000 });
        await page.waitForTimeout(1500);
        const markdown = (await page.evaluate(TO_MARKDOWN)) || "";
        if (!markdown) {
            console.log(`  [跳过] ${entry.modelKey}：页面上没有 .readme-prose`);
            continue;
        }
        const previous = entry.upstream.readme || "";
        const unchanged = previous.trim() === markdown.trim();
        entry.upstream.readme = markdown;
        if (!unchanged) changed += 1;
        console.log(`  [${unchanged ? "一致" : "更新"}] ${entry.modelKey} → ${markdown.length} 字符`);
        if (!WRITE) console.log("        " + markdown.split("\n").slice(0, 2).join(" / ").slice(0, 110));
    } catch (error) {
        console.log(`  [失败] ${entry.modelKey}：${String(error).slice(0, 120)}`);
    }
    await page.close();
}

await browser.close();

if (WRITE) {
    writeFileSync(COPY_PATH, JSON.stringify(document_, null, 2) + "\n", "utf8");
    console.log(`\n写回内容文件，${changed} 条有变化。`);
} else {
    console.log(`\n预览：${changed} 条与文件不同；加 --write 生效。`);
}
