import { describe, expect, test } from "bun:test";

import { creationFeaturedWorks } from "../src/pages/create/creation-inspirations";
import { inspirationCreationPath } from "../src/pages/home/home-inspirations";

/** 从链接里取查询串，模拟创作页真实的解析方式。 */
function queryOf(path: string): URLSearchParams {
    return new URLSearchParams(path.slice(path.indexOf("?") + 1));
}

describe("KinoTV 首页精选灵感", () => {
    test("灵感卡同时带上模式与提示词，点进去就能直接生成", () => {
        const item = creationFeaturedWorks[0];
        const query = queryOf(inspirationCreationPath(item));
        expect(query.get("mode")).toBe(item.mode);
        expect(query.get("prompt")).toBe(item.prompt);
    });

    test("提示词里的换行与保留字符不会截断查询串", () => {
        const prompt = "第一行\n第二行 & 100% #分镜?=真";
        const query = queryOf(inspirationCreationPath({ title: "t", description: "d", image: "/x.jpg", mode: "text", prompt }));
        expect(query.get("mode")).toBe("text");
        expect(query.get("prompt")).toBe(prompt);
    });

    test("三种模式的推荐内容都存在，筛选后不会出现空列表", () => {
        for (const mode of ["video", "image", "text"] as const) {
            expect(creationFeaturedWorks.filter((item) => item.mode === mode).length).toBeGreaterThan(0);
        }
    });
});
