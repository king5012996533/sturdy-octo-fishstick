import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

test("create empty state keeps the hero and composer in document flow", () => {
    const styles = readFileSync(resolve(import.meta.dir, "../src/styles/globals.css"), "utf8");
    const layoutStart = styles.indexOf("/* 空态布局收敛：");
    const layoutEnd = styles.indexOf("/* ===== 素材库", layoutStart);
    const layout = styles.slice(layoutStart, layoutEnd);

    expect(layoutStart).toBeGreaterThanOrEqual(0);
    expect(layoutEnd).toBeGreaterThan(layoutStart);
    // 主区自己滚：区块按 flex-start 排布并各自锁定高度，不能靠 auto 外边距把滚动撑坏。
    expect(layout).toContain("justify-content: flex-start;");
    expect(layout).toContain("flex: 0 0 auto;");
    expect(layout).toContain(".creation-home .creation-empty-workspace > .creation-empty-composer");
    expect(layout).not.toContain("margin: auto auto");
    expect(layout).not.toContain("margin: 26px auto auto;");
});
