import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import type { ShowcaseModel } from "@/features/model-showcase/api";
import { capabilityKey, capabilityLabel, priceLabel, specRows, tierLabel, unitLabel } from "@/features/model-showcase/presentation";
import { creditUnitRateLabel } from "@/lib/credit-price-label";
import { isPublicRoutePath } from "@/lib/public-routes";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

function buildModel(overrides: Partial<ShowcaseModel> = {}): ShowcaseModel {
    return {
        slug: "openai/gpt-image-2.5-sunburst",
        displayName: "GPT Image 2.5 Sunburst",
        icon: "",
        capability: "image",
        protocol: "replicate",
        tagline: "",
        summary: "",
        highlights: [],
        sourceUrl: "",
        spec: { ratios: [], qualityTiers: [], resolutions: [], durations: [], generateAudio: false, maxOutputs: 0, maxReferenceImages: 0, maxReferenceVideos: 0 },
        prices: [{ priceTier: "standard", unit: "IMAGE", sellUnitPrice: 30, priced: true }],
        ...overrides,
    };
}

describe("公开路径白名单", () => {
    test("只放行模型介绍页，其余路径一律不放行", () => {
        expect(isPublicRoutePath("/models")).toBe(true);
        expect(isPublicRoutePath("/models/")).toBe(true);
        expect(isPublicRoutePath("/models/openai/gpt-image-2")).toBe(true);
        expect(isPublicRoutePath("/models?capability=image")).toBe(true);
    });

    test("同前缀路径与目录穿越都不会被误判成公开页", () => {
        expect(isPublicRoutePath("/models-archive")).toBe(false);
        expect(isPublicRoutePath("/models/../admin")).toBe(false);
        expect(isPublicRoutePath("/settings")).toBe(false);
        expect(isPublicRoutePath("/")).toBe(false);
        expect(isPublicRoutePath("")).toBe(false);
    });

    test("白名单由 lib 判定，登录门自己不再抄一份", () => {
        const gate = read("src/features/hosted-auth/gate.tsx");
        expect(gate).not.toContain("/models");
        expect(gate).not.toContain("public-routes");
        const providers = read("src/components/layout/app-providers.tsx");
        expect(providers).toContain("isPublicRoutePath(pathname)");
        expect(providers).toContain("__BEEFTV_HOSTED_AUTH__ && isPublicRoutePath(pathname)");
    });

    test("模型介绍页挂在登录门之外，且只在托管构建里注册", () => {
        const router = read("src/router.tsx");
        expect(router).toContain("function modelDocRoutes()");
        expect(router).toContain("if (!__BEEFTV_HOSTED_AUTH__) return [];");
        // 详情必须是通配：模型标识自带斜杠，路径参数在不同代理上解不出同一个值。
        expect(router).toContain('path: "/models/*"');
        expect(router.indexOf("...modelDocRoutes(),")).toBeLessThan(router.indexOf("<WorkspaceLayout />"));
    });

    test("模型入口收在账户菜单里，并且新开标签进公开页", () => {
        const sidebar = read("src/components/layout/workspace-sidebar-nav.tsx");
        expect(sidebar).not.toContain('"/models"');
        // 公开页在工作区之外渲染：同标签跳过去，用户手里的画布与未保存状态会被顶掉。
        // 入口只在托管形态注册 —— sidebar-footer 属于 @/features/hosted-auth，本地构建整体被摇掉。
        const accountMenu = read("src/features/hosted-auth/sidebar-footer.tsx");
        expect(accountMenu).toContain('to="/models"');
        expect(accountMenu).toContain('target="_blank"');
        expect(accountMenu).toContain('data-testid="hosted-auth-account-models"');
    });
});

describe("积分单价文案", () => {
    test("单位枚举翻成中文，其余单位按次兜底", () => {
        expect(creditUnitRateLabel("IMAGE", 30)).toBe("30 积分/张");
        expect(creditUnitRateLabel("SECOND", 1200)).toBe("1,200 积分/秒");
        expect(creditUnitRateLabel("TOKEN_1M", 5)).toBe("5 积分/百万 token");
        expect(creditUnitRateLabel("TOKEN_1K", 5)).toBe("5 积分/千 token");
        expect(creditUnitRateLabel("REQUEST", 300)).toBe("300 积分/次");
    });

    test("创作台与广场共用同一份单价文案", () => {
        const estimate = read("src/pages/create/creation-credit-estimate.tsx");
        expect(estimate).toContain("creditUnitRateLabel(unit, sellUnitPrice)");
        expect(estimate).not.toContain("积分/张");
    });
});

describe("模型广场读模型", () => {
    test("能力分组：未知能力归到其他而不是消失", () => {
        expect(capabilityKey("IMAGE")).toBe("image");
        expect(capabilityKey(" video ")).toBe("video");
        expect(capabilityKey("3d")).toBe("other");
        expect(capabilityKey("")).toBe("other");
        expect(capabilityLabel("audio")).toBe("音频");
        expect(capabilityLabel("embedding")).toBe("其他");
    });

    test("未定价显示成暂不可用，绝不显示成 0", () => {
        expect(priceLabel({ priceTier: "low", unit: "IMAGE", sellUnitPrice: 30, priced: true })).toBe("30 积分/张");
        expect(priceLabel({ priceTier: "low", unit: "IMAGE", sellUnitPrice: null, priced: false })).toBe("暂不可用");
        expect(tierLabel("")).toBe("默认档");
        expect(unitLabel("SECOND")).toBe("秒");
        expect(unitLabel("TOKEN_1M")).toBe("百万 token");
        expect(unitLabel("REQUEST")).toBe("次");
    });

    test("参数表只渲染有值的行", () => {
        const image = buildModel({ spec: { ...buildModel().spec, ratios: ["1:1", "16:9"], qualityTiers: ["low", "max"], maxOutputs: 4, maxReferenceImages: 2 } });
        const rows = specRows(image.spec, image.capability);
        expect(rows).toEqual([
            { label: "画面比例", value: "1:1 / 16:9" },
            { label: "画质档位", value: "low / max" },
            { label: "单次输出", value: "最多 4 张" },
            { label: "参考图", value: "最多 2 张" },
        ]);
        expect(specRows(buildModel().spec, "image")).toEqual([]);
    });

    test("视频参数：枚举时长与区间时长分开渲染，参考视频是段不是张", () => {
        const enumed = buildModel({ capability: "video", spec: { ...buildModel().spec, durations: [5, 10], generateAudio: true, maxReferenceVideos: 3 } });
        expect(specRows(enumed.spec, enumed.capability)).toEqual([
            { label: "可选时长", value: "5 / 10 秒" },
            { label: "声音", value: "支持生成音频" },
            { label: "参考视频", value: "最多 3 段" },
        ]);
        const ranged = buildModel({ capability: "video", spec: { ...buildModel().spec, range: { min: 1, max: 15, step: 1, value: 5 } } });
        expect(specRows(ranged.spec, ranged.capability)).toEqual([{ label: "时长范围", value: "1–15 秒，1 秒步进" }]);
    });
});
