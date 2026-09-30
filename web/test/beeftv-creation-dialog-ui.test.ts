import { expect, test } from "bun:test";

const page = await Bun.file(new URL("../src/pages/create/index.tsx", import.meta.url)).text();
const composer = await Bun.file(new URL("../src/pages/create/creation-workspace.tsx", import.meta.url)).text();
const styles = await Bun.file(new URL("../src/pages/create/creation-product.css", import.meta.url)).text();
const creationTypes = await Bun.file(new URL("../src/pages/create/creation-types.ts", import.meta.url)).text();

test("BeefTV creation dialog uses the simplified Agent controls", () => {
    // 品牌名取自外观配置，换皮时横幅标题跟着走；上游这里是写死的字符串。
    expect(page).toContain("className=\"creation-home-hero-brand\"");
    expect(page).toContain("<span>{brandName}</span> <em>Agent</em>");
    expect(page).toContain("state.appearance.brandName");
    expect(page).not.toContain("从一个画面、一个角色或一句话开始");
    expect(composer).toContain("creation-chat-reference-add");
    expect(composer).toContain('className="creation-submit is-icon-only"');
    expect(composer).not.toContain('<span>{showWorkingSpinner ? "生成中" : "开始创作"}</span>');
    expect(styles).toContain("creation-submit.is-icon-only");
});

test("new BeefTV Agent sessions start in the LibTV video mode", () => {
    expect(creationTypes).toContain('export const defaultCreationMode: CreationMode = "video";');
});
