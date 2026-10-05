import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const cssPath = resolve(import.meta.dir, "../src/styles/antd-select-search-text.css");
const appPath = resolve(import.meta.dir, "../src/application.tsx");

describe("选择器搜索态文字色", () => {
    test("补齐 antd 未声明的搜索态文字色，输入内容不再透明", () => {
        const css = readFileSync(cssPath, "utf8");
        // antd 只把 .ant-select-content-has-search-value 设成 transparent，输入框继承后不可见。
        expect(css).toContain(".ant-select:not(.ant-select-customize) .ant-select-content-has-search-value");
        expect(css).toContain("color: inherit !important");
        // 上游规则是运行时注入的，靠层叠顺序赢不了，必须 !important。
        expect(css).not.toContain("color: transparent");
    });

    test("样式表在入口处引入，否则整条兜底不生效", () => {
        const app = readFileSync(appPath, "utf8");
        expect(app).toContain('import "./styles/antd-select-search-text.css";');
    });
});
