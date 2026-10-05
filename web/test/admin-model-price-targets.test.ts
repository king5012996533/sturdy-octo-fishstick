import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import {
    channelModelFullKey,
    modelPriceTargetLabel,
    modelPriceTargetSearchText,
    toPriceCapability,
    type ModelPriceTarget,
} from "../src/features/admin-console/model-price-targets";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

const targetsPath = "src/features/admin-console/model-price-targets.ts";
const pickerPath = "src/features/admin-console/model-key-picker.tsx";

/**
 * 「从模型出发定价」的目录层。
 *
 * 价目表的主键是 `渠道 ID::平台模型标识`，而运营记的是"Replicate 下的配乐模型"。这一层
 * 是两者的桥：拼错了主键，价格配得再对也不会被计费读到——而且不报错，只是静默不生效。
 */
describe("后台可定价模型目录", () => {
    test("主键与后端拼接口径一致：渠道 ID::平台模型标识", () => {
        expect(channelModelFullKey("CHANNEL_000003", "minimax/music-2.5")).toBe("CHANNEL_000003::minimax/music-2.5");
        // 前后空格来自表单，不能带进主键。
        expect(channelModelFullKey(" CHANNEL_000003 ", " minimax/music-2.5 ")).toBe("CHANNEL_000003::minimax/music-2.5");
        // 模型标识大小写敏感（上游 manifest 里就有 MiniMax-H3 这种写法），不做归一。
        expect(channelModelFullKey("CHANNEL_000008", "MiniMax-H3")).toBe("CHANNEL_000008::MiniMax-H3");
    });

    test("能力映射：小写渠道能力 → 大写价目能力，认不出的返回 null", () => {
        expect(toPriceCapability("text")).toBe("TEXT");
        expect(toPriceCapability("IMAGE")).toBe("IMAGE");
        expect(toPriceCapability("video")).toBe("VIDEO");
        expect(toPriceCapability("audio")).toBe("AUDIO");
        expect(toPriceCapability(" audio ")).toBe("AUDIO");
        // 认不出来时必须给 null：让运营给一个服务端必然拒收的模型定价，比不显示它更糟。
        expect(toPriceCapability("speech")).toBeNull();
        expect(toPriceCapability("")).toBeNull();
    });

    test("下拉文案带渠道与模型名，搜索串覆盖渠道名/展示名/标识/完整主键", () => {
        const target: ModelPriceTarget = {
            fullKey: "CHANNEL_000003::minimax/music-2.5",
            channelId: "CHANNEL_000003",
            channelName: "Replicate·主账号",
            modelKey: "minimax/music-2.5",
            displayName: "MiniMax Music 2.5",
            capability: "AUDIO",
            enabled: true,
        };
        expect(modelPriceTargetLabel(target)).toBe("Replicate·主账号 · MiniMax Music 2.5 · minimax/music-2.5");
        // 展示名缺失时退回模型标识，不能出现 " ·  · " 这种空档。
        expect(modelPriceTargetLabel({ ...target, displayName: "" })).toBe("Replicate·主账号 · minimax/music-2.5 · minimax/music-2.5");

        const search = modelPriceTargetSearchText(target);
        expect(search).toContain("replicate·主账号");
        expect(search).toContain("minimax music 2.5");
        expect(search).toContain("minimax/music-2.5");
        expect(search).toContain("channel_000003::minimax/music-2.5");
    });

    test("目录由渠道 + 渠道模型两跳拼出，跳过认不出能力的模型", () => {
        const source = read(targetsPath);
        expect(source).toContain("listAdminChannels({ page: 1, pageSize: 200 })");
        expect(source).toContain("listAdminChannelModels(channel.id)");
        expect(source).toContain("if (!capability) return [];");
        expect(source).toContain('fullKey: channelModelFullKey(channel.id, model.modelKey)');
        // 停用的渠道/模型照样列出来但打标记：直接隐藏会让运营以为模型丢了。
        expect(source).toContain("enabled: Boolean(channel.enabled) && Boolean(model.enabled)");
    });

    test("选择器可搜可手填：按搜索串过滤，选中回调整条目录项", () => {
        expect(existsSync(resolve(root, pickerPath))).toBe(true);
        const picker = read(pickerPath);
        expect(picker).toContain("AutoComplete");
        expect(picker).toContain("filterOption=");
        expect(picker).toContain("option?.searchText as string");
        expect(picker).toContain("onSelect={(_next, option) => onPick?.((option as { target: ModelPriceTarget }).target)}");
        // 目录为空时仍要能手填完整标识。
        expect(picker).toContain("notFoundContent");
    });
});
