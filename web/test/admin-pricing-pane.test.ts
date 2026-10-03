import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { formatMultiplierBp, formatSellPrice, formatUnitPrice, numberOrNull } from "../src/features/admin-console/pricing-pane";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

const apiPath = "src/features/admin-console/api-pricing.ts";
const panePath = "src/features/admin-console/pricing-pane.tsx";

/**
 * 「模型定价」分区：源码级契约 + 关键口径的纯函数断言。
 *
 * 最容易被改坏的不是接口路径，而是那一句「未定价不是 0」：把 null 当成 0 展示，页面上
 * 看起来一切正常，真实账单却会少收。所以这里既钉源码，也直接调用展示口径的纯函数。
 */
describe("后台模型定价面板", () => {
    test("文件存在，导出名与合并方 import 的一致", () => {
        expect(existsSync(resolve(root, apiPath))).toBe(true);
        expect(existsSync(resolve(root, panePath))).toBe(true);
        expect(read(panePath)).toContain("export function PricingPane()");
        expect(read(apiPath)).toContain("export function listAdminModelPrices()");
    });

    test("全局 antd message 在项目里是关闭的，反馈必须落在页面上", () => {
        const pane = read(panePath);
        expect(pane).not.toContain("message.success");
        expect(pane).not.toContain("message.error");
        expect(pane).not.toContain("notification.");
        expect(pane).toContain("admin-notice is-error");
        expect(pane).toContain("admin-notice is-ok");
        expect(pane).toContain("重试");
        expect(pane).toContain("admin-empty");
        expect(pane).not.toMatch(/import\s*\{[^}]*\bEmpty\b/);
    });

    test("取数走 useCallback + useEffect，并覆盖加载态与空态", () => {
        const pane = read(panePath);
        expect(pane).toContain("const load = useCallback(async () => {");
        expect(pane).toContain("useEffect(() => {");
        expect(pane).toContain("void load();");
        expect(pane).toContain("setLoading(true)");
        expect(pane).toContain("正在加载定价");
        // 倍率与单价两张表各自有空态文案，不能只靠 antd 默认的 "No data"。
        expect(pane).toContain("还没有倍率规则");
        expect(pane).toContain("还没有定价记录");
    });

    test("未定价显示成「未定价」而不是 0", () => {
        expect(formatUnitPrice(null)).toBe("未定价");
        expect(formatUnitPrice(undefined)).toBe("未定价");
        // 0 是真实价格（免费），必须和"还没定价"区分开。
        expect(formatUnitPrice(0)).toBe("0");
        expect(formatUnitPrice(12)).toBe("12");
        const pane = read(panePath);
        expect(pane).toContain("未定价");
        expect(pane).toContain("Number.isFinite(value)) return \"未定价\"");
    });

    test("售价为空显示「按倍率计算」，试算未定价时说明无法计算", () => {
        expect(formatSellPrice(null)).toBe("按倍率计算");
        expect(formatSellPrice(0)).toBe("0");
        expect(formatSellPrice(30)).toBe("30");
        const pane = read(panePath);
        expect(pane).toContain("按倍率计算");
        expect(pane).toContain("未定价，无法计算售价");
        // 售价与倍率口径来自服务端解析，页面不自己乘一遍。
        expect(pane).toContain("preview.priced");
        expect(pane).toContain("preview.sellUnitPrice");
    });

    test("倍率按万分比换算：12000 → 1.2，去掉尾随的 0", () => {
        expect(formatMultiplierBp(12000)).toBe("1.2");
        expect(formatMultiplierBp(10000)).toBe("1");
        expect(formatMultiplierBp(12500)).toBe("1.25");
        expect(formatMultiplierBp(null)).toBe("—");
        const pane = read(panePath);
        expect(pane).toContain("const basisPointPerUnit = 10_000;");
        expect(pane).toContain("Math.round(draft.multiplier * basisPointPerUnit)");
        // 页面按「倍率」输入（1.2），不让人工去算万分比。
        expect(pane).toContain("万分比");
    });

    test("空值一律收成 null，不会被当成 0 写进计费", () => {
        expect(numberOrNull(null)).toBeNull();
        expect(numberOrNull(undefined)).toBeNull();
        expect(numberOrNull(Number.NaN)).toBeNull();
        expect(numberOrNull(0)).toBe(0);
        const pane = read(panePath);
        expect(pane).toContain("upstreamUnitPrice: numberOrNull(values.upstreamUnitPrice)");
        expect(pane).toContain("sellUnitPrice: numberOrNull(values.sellUnitPrice)");
    });

    test("单价是整数分占位，不做元/分换算", () => {
        const pane = read(panePath);
        // formatMoneyFen 是"分 → 元"的展示口径，这一页不能用它。
        expect(pane).not.toContain("formatMoneyFen");
        expect(pane).not.toContain("* 100");
        expect(pane).not.toContain("/ 100");
        // 表头/卡片说明必须写清单位，否则不知道 0.5 是每张还是每秒。
        // 文本是"分/百万 token"：0.02 元/百万 token 换成"分/千 token"是 0.02 分，
        // 整数存不下只能向上取整成 1 分，等于按 ¥10 卖。
        expect(pane).toContain("分/百万token");
        expect(pane).toContain("上游单价（分/单位）");
        expect(pane).toContain("售价（分/单位）");
        expect(pane).toContain('TOKEN_1M: "百万 token"');
    });

    test("倍率区：默认倍率只读展示 + 规则表可增删 + 整表保存", () => {
        const pane = read(panePath);
        expect(pane).toContain("defaultMultiplierBp");
        expect(pane).toContain("新增规则");
        expect(pane).toContain("保存");
        // 保存是整表提交（PUT 全量），不是逐条改。
        expect(pane).toContain("updateAdminBillingMarkup(draftRules.map(markupInputOf))");
        expect(pane).toContain("整表提交");
        // 作用域四档都在，且 GLOBAL 的目标固定为 *。
        expect(pane).toContain('{ GLOBAL: "全局", CAPABILITY: "能力", VENDOR: "厂商", MODEL: "模型" }');
        expect(pane).toContain('target: draft.scope === "GLOBAL" ? "*" : draft.target.trim()');
        // 未保存的改动要看得出来。
        expect(pane).toContain("rulesDirty");
    });

    test("单价表列齐口径：模型/能力/单位/上游单价/倍率/售价/启用/备注", () => {
        const pane = read(panePath);
        for (const title of ["模型标识", "能力", "单位", "上游单价（分/单位）", "倍率", "售价（分/单位）", "启用", "备注"]) {
            expect(pane).toContain(`title: "${title}"`);
        }
        expect(pane).toContain("listAdminModelPrices");
        expect(pane).toContain("createAdminModelPrice");
        expect(pane).toContain("updateAdminModelPrice");
        expect(pane).toContain("deleteAdminModelPrice");
        // 删除有二次确认。
        expect(pane).toContain("删除定价？");
        expect(pane).toContain("确认删除");
    });

    test("试算区：模型标识 / 厂商 code / 能力 / 上游单价 → preview", () => {
        const pane = read(panePath);
        expect(pane).toContain("previewAdminModelPrice({");
        expect(pane).toContain("modelKey: values.modelKey.trim()");
        expect(pane).toContain("vendorCode: (values.vendorCode ?? \"\").trim()");
        expect(pane).toContain("capability: values.capability");
        expect(pane).toContain("upstreamUnitPrice: numberOrNull(values.upstreamUnitPrice)");
        // 结果要能看到生效倍率、来源与售价。
        expect(pane).toContain("formatMultiplierBp(preview.multiplierBp)");
        expect(pane).toContain("preview.source");
    });

    test("档位随能力收敛：文本三档 / 图片五档质量 + 空档 / 音频三档时长 + 兜底 / 视频只有空档", () => {
        const pane = read(panePath);
        // 选项按能力取，不是一份全局写死的列表。
        expect(pane).toContain("tierOptionsByCapability");
        expect(pane).toContain('TEXT: (["CACHE", "INPUT", "OUTPUT"] as ModelPricePriceTier[]).map((value) => ({ value, label: tierLabels[value] }))');
        expect(pane).toContain('IMAGE: (["", "LOW", "MEDIUM", "HIGH", "XHIGH", "MAX"] as ModelPricePriceTier[]).map((value) => ({ value, label: tierLabels[value] }))');
        expect(pane).toContain('VIDEO: [{ value: "", label: tierLabels[""] }]');
        expect(pane).toContain('AUDIO: (["", "SHORT", "MEDIUM", "LONG"] as ModelPricePriceTier[]).map((value) => ({ value, label: audioTierLabels[value] }))');
        // 图片五档质量与文案都要在，否则运营选不到 low / medium / high / xhigh / max。
        expect(pane).toContain('LOW: "低（low）"');
        expect(pane).toContain('MEDIUM: "中（medium）"');
        expect(pane).toContain('HIGH: "高（high）"');
        expect(pane).toContain('XHIGH: "极高（xhigh）"');
        expect(pane).toContain('MAX: "最高（max）"');
        // 音频三档必须带时长区间，否则运营不知道边界落在哪。
        expect(pane).toContain('SHORT: "短（≤30 秒）"');
        expect(pane).toContain('LONG: "长（>90 秒）"');
        // MEDIUM 在图片里是质量、在音频里是时长，音频必须换成带区间的文案。
        expect(pane).toContain('const audioTierLabels: Record<ModelPricePriceTier, string> = { ...tierLabels, MEDIUM: "中（≤90 秒）" };');
        expect(pane).toContain('TEXT: "INPUT"');
        expect(pane).toContain('IMAGE: ""');
        expect(pane).toContain('AUDIO: ""');
        // 表单 label 与提示词随能力给，不再是「token 档位」。
        expect(pane).toContain('label="价格档位"');
        expect(pane).not.toContain("token 档位");
        expect(pane).toContain("tierExtraOf");
    });

    test("提交时档位不会被清空：只有视频塌成空串，文本/图片/音频都保留", () => {
        const pane = read(panePath);
        // 曾经的写法 `capability === "TEXT" ? priceTier : ""` 会把图片选的质量档清空，
        // 三档质量价于是塌成同一条记录——校验只认能力，看不出这种"合法的静默降级"。
        expect(pane).not.toContain('capability === "TEXT" ? values.priceTier : ""');
        // 现在按"哪些能力不区分档位"来反向判断，音频加入分档后只剩余视频落空串。
        expect(pane).toContain('values.capability === "VIDEO" ? "" : values.priceTier');
        expect(pane).not.toContain('values.capability !== "VIDEO" ? "" : values.priceTier');
        // 保存与试算两处都必须走同一个口径。
        expect(pane).toContain("priceTier: tierForSubmit(values),");
        expect(pane.match(/priceTier: tierForSubmit\(values\),/g)?.length).toBe(2);
        // 列表页的列名同步改名。
        expect(pane).toContain('dataIndex: "priceTier", key: "priceTier"');
        // 表格按行的能力挑档位文案，否则音频行的 MEDIUM 会显示成图片的「中（medium）」。
        expect(pane).toContain("tierLabelFor(row.capability, value)");
    });

    test("接口路径与动词都拼在 /admin/billing 下", () => {
        const api = read(apiPath);
        expect(api).toContain('http.get<{ rules: MarkupRule[]; defaultMultiplierBp: number }>("/admin/billing/markup")');
        expect(api).toContain('http.put<{ rules: MarkupRule[]; defaultMultiplierBp: number }>("/admin/billing/markup", { rules })');
        expect(api).toContain('http.get<{ prices: ModelPrice[] }>("/admin/billing/model-prices")');
        expect(api).toContain('http.post<{ price: ModelPrice }>("/admin/billing/model-prices", input)');
        expect(api).toContain("http.put<{ price: ModelPrice }>(`/admin/billing/model-prices/${encodeURIComponent(id)}`, input)");
        expect(api).toContain("http.delete<{ deleted: true }>(`/admin/billing/model-prices/${encodeURIComponent(id)}`)");
        // 试算单独一条路径，不写库。
        expect(api).toContain('http.post<{ resolution: PricingResolution }>("/admin/billing/model-prices/preview", input)');
        // 类型与后端契约字段一致。
        expect(api).toContain('export type ModelPriceUnit = "TOKEN_1M" | "TOKEN_1K" | "IMAGE" | "SECOND" | "REQUEST";');
        // 分档的价格必须能表达：文本的三行靠 priceTier 区分，图片的五档质量价同理。
        expect(api).toContain(
            'export type ModelPricePriceTier = "" | "CACHE" | "INPUT" | "OUTPUT" | "LOW" | "MEDIUM" | "HIGH" | "XHIGH" | "MAX" | "SHORT" | "LONG";',
        );
        expect(api).toContain("priceTier: ModelPricePriceTier;");
        // 旧字段名残留会让请求体与服务端契约对不上（服务端读 priceTier）。
        expect(api).not.toContain("tokenTier");
        expect(api).toContain('export type MarkupScope = "GLOBAL" | "CAPABILITY" | "VENDOR" | "MODEL";');
        expect(api).toContain("upstreamUnitPrice: number | null;");
        expect(api).toContain("sellUnitPrice: number | null;");
        expect(api).toContain("priced: boolean;");
    });
});
