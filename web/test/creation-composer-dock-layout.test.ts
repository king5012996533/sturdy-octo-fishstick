import { expect, test } from "bun:test";

const styles = await Bun.file(new URL("../src/pages/create/creation-product.css", import.meta.url)).text();

function containerBlock(query: string) {
    const escaped = query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    return styles.match(new RegExp(`@container \\(${escaped}\\) \\{([\\s\\S]*?)\\n\\}`))?.[1] ?? "";
}

test("首页输入卡宽度给六个控件留出余量，同时仍比灵感墙窄一档", () => {
    // 880px 是"六个控件刚好一行"压出来的，余量不到 10px：
    // 换个长一点的型号（MiniMax H3 768P）就能把时长键挤到第二行。
    expect(styles).toContain("--creation-home-composer: min(960px, calc(100% - 72px));");
    // 悬浮层依然比它下面的墙窄 24px，否则输入卡会看起来像网格的第一行。
    expect(styles).toContain("--creation-home-wall: min(1680px, calc(100% - 48px));");
});

test("卡片够宽时底栏控件锁成一行，只有模型名可以伸缩", () => {
    const wide = containerBlock("min-width: 660px");
    expect(wide).toContain(".creation-chat-controls { flex-wrap: nowrap; }");
    // 模式 / 参考 / 语音 / 比例 / 时长被压窄只会让文字换行，必须钉住尺寸。
    expect(wide).toContain("> :not(.voice-recording-inline) { flex: 0 0 auto; }");
    // 模型选择器外面还有一层自适应宽度的壳，伸缩要落在这层壳上。
    expect(wide).toContain(":has(> .creation-model-picker)");
    expect(wide).toContain("flex: 0 1 auto;");
});

test("卡片放不下整排控件时，折行由卡片自身宽度决定而不是视口断点", () => {
    const narrow = containerBlock("max-width: 799px");
    expect(narrow).toContain("flex-wrap: wrap !important;");
    expect(narrow).toContain("justify-content: flex-end;");
    expect(narrow).toContain("flex: 1 1 100%; width: 100%;");
    // 侧栏在 1020px 附近收起，卡片反而变宽；
    // 用视口断点会在那一档把本来放得下的底栏折成两行。
    expect(styles).not.toContain("@media (max-width: 1119px)");
});
