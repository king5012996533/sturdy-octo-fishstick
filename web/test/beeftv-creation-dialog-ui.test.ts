import { expect, test } from "bun:test";

const page = await Bun.file(new URL("../src/pages/create/index.tsx", import.meta.url)).text();
const composer = await Bun.file(new URL("../src/pages/create/creation-workspace.tsx", import.meta.url)).text();
const styles = await Bun.file(new URL("../src/pages/create/creation-product.css", import.meta.url)).text();
const creationTypes = await Bun.file(new URL("../src/pages/create/creation-types.ts", import.meta.url)).text();

test("BeefTV creation home uses the platform creation entry", () => {
    // 品牌名取自外观配置，换皮时平台眉标和说明文案都会跟着走。
    expect(page).toContain("className=\"creation-home-hero-brand\"");
    expect(page).toContain("<span>{brandName}</span> <em>创作平台</em>");
    expect(page).toContain("把想象，拍成故事。");
    expect(page).toContain("{brandName} 会陪你把它发展成真正能被看见的故事。");
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
