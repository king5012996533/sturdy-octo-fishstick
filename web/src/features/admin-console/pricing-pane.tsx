import { Button, Form, Input, InputNumber, Modal, Select, Switch, Table, Tag, type TableProps } from "antd";
import { BadgePercent, Calculator, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import {
    createAdminModelPrice,
    deleteAdminModelPrice,
    getAdminBillingMarkup,
    listAdminModelPrices,
    previewAdminModelPrice,
    updateAdminBillingMarkup,
    updateAdminModelPrice,
    type MarkupRule,
    type MarkupRuleInput,
    type MarkupScope,
    type ModelPrice,
    type ModelPriceCapability,
    type ModelPriceInput,
    type ModelPriceTokenTier,
    type ModelPriceUnit,
    type PricingResolution,
} from "./api-pricing";

/**
 * 倍率用万分比存储：12000 = ×1.2。页面按「倍率」输入（1.2），提交前乘 10000 换成 bp，
 * 与后端字段口径一致；不这样做的话每次改动都要人工算一遍万分比。
 */
const basisPointPerUnit = 10_000;

const capabilityLabels: Record<ModelPriceCapability, string> = { TEXT: "文本", IMAGE: "图片", VIDEO: "视频", AUDIO: "音频" };

/** 计价单位决定"一个单价代表多少量"，展示时必须写清楚，否则 0.5 元是每张还是每秒没人知道。 */
const unitLabels: Record<ModelPriceUnit, string> = { TOKEN_1M: "百万 token", TOKEN_1K: "千 token（旧）", IMAGE: "张", SECOND: "秒", REQUEST: "次" };

/**
 * 文本的 token 档位。上游对这三档分别定价，差到两个数量级（DeepSeek 输出价是缓存命中
 * 价的 200 倍），所以必须分开配；图片/视频/音频没有这个概念。
 */
const tierLabels: Record<ModelPriceTokenTier, string> = { "": "不区分", CACHE: "缓存命中", INPUT: "缓存未命中", OUTPUT: "输出" };

const tierOptions = [
    { value: "" as ModelPriceTokenTier, label: "不区分（图片 / 视频 / 音频）" },
    ...(["CACHE", "INPUT", "OUTPUT"] as ModelPriceTokenTier[]).map((value) => ({ value, label: tierLabels[value] })),
];

function tierLabel(value: string) {
    return tierLabels[value as ModelPriceTokenTier] ?? (value ? value : "不区分");
}

const capabilityOptions = (Object.keys(capabilityLabels) as ModelPriceCapability[]).map((value) => ({ value, label: capabilityLabels[value] }));

const unitOptions = (Object.keys(unitLabels) as ModelPriceUnit[]).map((value) => ({ value, label: unitLabels[value] }));

const scopeLabels: Record<MarkupScope, string> = { GLOBAL: "全局", CAPABILITY: "能力", VENDOR: "厂商", MODEL: "模型" };

const scopeOptions = (Object.keys(scopeLabels) as MarkupScope[]).map((value) => ({ value, label: scopeLabels[value] }));

export function priceCapabilityLabel(capability: string) {
    return capabilityLabels[capability as ModelPriceCapability] ?? (capability || "—");
}

export function unitLabel(unit: string) {
    return unitLabels[unit as ModelPriceUnit] ?? (unit || "—");
}

/** 万分比 → 展示用倍率：12000 → 1.2；去掉尾随的 0，避免出现 1.2000 这种读数。 */
export function formatMultiplierBp(basisPoints: number | null | undefined) {
    if (typeof basisPoints !== "number" || !Number.isFinite(basisPoints)) return "—";
    return String(Number((basisPoints / basisPointPerUnit).toFixed(4)));
}

/** 上游单价为空是「未定价」，不是 0：0 代表免费，两者在计费上完全不是一件事。 */
export function formatUnitPrice(value: number | null | undefined) {
    if (value === null || value === undefined || !Number.isFinite(value)) return "未定价";
    return String(value);
}

/** 售价为空时走倍率解析，所以显示「按倍率计算」，而不是显示成 0 元。 */
export function formatSellPrice(value: number | null | undefined) {
    if (value === null || value === undefined || !Number.isFinite(value)) return "按倍率计算";
    return String(value);
}

export function numberOrNull(value: number | null | undefined) {
    return typeof value === "number" && Number.isFinite(value) ? value : null;
}

/** 草稿行没有后端 id（新增的行还没保存），用一个自增 key 保证表格行不串位。 */
let markupDraftSeq = 0;

function nextMarkupDraftKey() {
    markupDraftSeq += 1;
    return `markup-draft-${markupDraftSeq}`;
}

type MarkupRuleDraft = {
    key: string;
    scope: MarkupScope;
    target: string;
    multiplier: number;
    note: string;
};

type PriceFormValues = {
    modelKey: string;
    vendorCode: string;
    capability: ModelPriceCapability;
    tokenTier: ModelPriceTokenTier;
    unit: ModelPriceUnit;
    upstreamUnitPrice: number | null;
    sellUnitPrice: number | null;
    multiplier: number | null;
    enabled: boolean;
    note: string;
};

type PreviewFormValues = {
    modelKey: string;
    vendorCode: string;
    capability: ModelPriceCapability;
    tokenTier: ModelPriceTokenTier;
    upstreamUnitPrice: number | null;
};

const emptyPrice: PriceFormValues = {
    modelKey: "",
    vendorCode: "",
    capability: "TEXT",
    // 默认落在文本最常见的档位：输入未命中缓存。默认空档位会让首次保存直接被拒。
    tokenTier: "INPUT",
    unit: "TOKEN_1M",
    upstreamUnitPrice: null,
    sellUnitPrice: null,
    multiplier: null,
    enabled: true,
    note: "",
};

const emptyPreview: PreviewFormValues = { modelKey: "", vendorCode: "", capability: "TEXT", tokenTier: "INPUT", upstreamUnitPrice: null };

function markupDraftOf(rule: MarkupRule): MarkupRuleDraft {
    const parsed = Number(rule.multiplier);
    return {
        key: `markup-rule-${rule.id}`,
        scope: rule.scope,
        target: rule.target,
        multiplier: Number.isFinite(parsed) ? parsed : rule.multiplierBp / basisPointPerUnit,
        note: rule.note,
    };
}

function markupInputOf(draft: MarkupRuleDraft): MarkupRuleInput {
    return {
        scope: draft.scope,
        target: draft.scope === "GLOBAL" ? "*" : draft.target.trim(),
        multiplierBp: Math.round(draft.multiplier * basisPointPerUnit),
        note: draft.note.trim(),
    };
}

function priceFormValuesOf(price: ModelPrice): PriceFormValues {
    return {
        modelKey: price.modelKey,
        vendorCode: price.vendorCode,
        capability: price.capability,
        tokenTier: price.tokenTier,
        unit: price.unit,
        upstreamUnitPrice: price.upstreamUnitPrice,
        sellUnitPrice: price.sellUnitPrice,
        multiplier: price.multiplierBp === null ? null : price.multiplierBp / basisPointPerUnit,
        enabled: price.enabled,
        note: price.note,
    };
}

function priceInputOf(values: PriceFormValues): ModelPriceInput {
    return {
        modelKey: values.modelKey.trim(),
        vendorCode: (values.vendorCode ?? "").trim(),
        capability: values.capability,
        // 非文本能力必须提交空档位：服务端会拒绝"图片带 token 档位"这种配置，
        // 与其让用户填完再报错，不如在这里就按能力抹平。
        tokenTier: values.capability === "TEXT" ? values.tokenTier : "",
        unit: values.unit,
        // 空 = null（未定价），不是 0：0 会被当成真实售价写进计费。
        upstreamUnitPrice: numberOrNull(values.upstreamUnitPrice),
        sellUnitPrice: numberOrNull(values.sellUnitPrice),
        multiplierBp: numberOrNull(values.multiplier) === null ? null : Math.round((values.multiplier ?? 0) * basisPointPerUnit),
        enabled: values.enabled,
        note: (values.note ?? "").trim(),
    };
}

function reasonOf(error: unknown, fallback: string) {
    return error instanceof Error && error.message ? error.message : fallback;
}

/**
 * 模型定价。
 *
 * 售价 = 上游成本 × 倍率。单价现在是占位数字，按上游真实价格回填即可——所以这一页
 * 刻意不做元/分换算，也不在没有上游价时编一个 0 出来：把「未定价」和「免费」分开，
 * 是这一页唯一不能妥协的口径。
 *
 * 全局的 antd message 在本项目是关闭的，所有反馈都走页面内的 .admin-notice。
 */
export function PricingPane() {
    const [rules, setRules] = useState<MarkupRule[]>([]);
    const [draftRules, setDraftRules] = useState<MarkupRuleDraft[]>([]);
    const [defaultMultiplierBp, setDefaultMultiplierBp] = useState<number | null>(null);
    const [prices, setPrices] = useState<ModelPrice[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");

    const [rulesError, setRulesError] = useState("");
    const [rulesSaving, setRulesSaving] = useState(false);

    const [priceEditor, setPriceEditor] = useState<{ price: ModelPrice | null } | null>(null);

    const [priceSaving, setPriceSaving] = useState(false);
    const [priceFormError, setPriceFormError] = useState("");
    const [priceTarget, setPriceTarget] = useState<ModelPrice | null>(null);
    const [priceDeleting, setPriceDeleting] = useState(false);
    const [priceDeleteError, setPriceDeleteError] = useState("");

    const [preview, setPreview] = useState<PricingResolution | null>(null);
    const [previewError, setPreviewError] = useState("");
    const [previewing, setPreviewing] = useState(false);

    const [priceForm] = Form.useForm<PriceFormValues>();
    // 档位与单位都由能力决定，表单里要跟着能力变灰/变化，所以这里订阅它。
    const priceFormCapability = Form.useWatch("capability", priceForm);
    const [previewForm] = Form.useForm<PreviewFormValues>();

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const [markupPayload, pricePayload] = await Promise.all([getAdminBillingMarkup(), listAdminModelPrices()]);
            const loadedRules = markupPayload.rules ?? [];
            setRules(loadedRules);
            setDraftRules(loadedRules.map(markupDraftOf));
            setDefaultMultiplierBp(markupPayload.defaultMultiplierBp);
            setPrices(pricePayload.prices ?? []);
        } catch (loadError) {
            setError(reasonOf(loadError, "加载定价失败"));
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const rulesDirty = useMemo(
        () => JSON.stringify(draftRules.map(markupInputOf)) !== JSON.stringify(rules.map((rule) => markupInputOf(markupDraftOf(rule)))),
        [draftRules, rules],
    );

    const updateDraft = useCallback((key: string, patch: Partial<MarkupRuleDraft>) => {
        setRulesError("");
        setDraftRules((current) => current.map((draft) => (draft.key === key ? { ...draft, ...patch } : draft)));
    }, []);

    const addDraftRule = useCallback(() => {
        setRulesError("");
        setDraftRules((current) => [
            ...current,
            {
                key: nextMarkupDraftKey(),
                scope: "MODEL",
                target: "",
                // 新增规则默认给全局默认倍率，运营只需要改不成立的那一两条。
                multiplier: (defaultMultiplierBp ?? basisPointPerUnit) / basisPointPerUnit,
                note: "",
            },
        ]);
    }, [defaultMultiplierBp]);

    const removeDraftRule = useCallback((key: string) => {
        setRulesError("");
        setDraftRules((current) => current.filter((draft) => draft.key !== key));
    }, []);

    const saveRules = useCallback(async () => {
        const invalid = draftRules.find((draft) => draft.scope !== "GLOBAL" && !draft.target.trim());
        if (invalid) {
            setRulesError(`「${scopeLabels[invalid.scope]}」规则必须填写目标（厂商 code / 能力 / 模型标识）。`);
            return;
        }
        const nonPositive = draftRules.find((draft) => !(draft.multiplier > 0));
        if (nonPositive) {
            setRulesError("倍率必须大于 0；想不参与加价请填 1。");
            return;
        }
        setRulesSaving(true);
        setRulesError("");
        setNotice("");
        try {
            const payload = await updateAdminBillingMarkup(draftRules.map(markupInputOf));
            const savedRules = payload.rules ?? [];
            setRules(savedRules);
            setDraftRules(savedRules.map(markupDraftOf));
            setDefaultMultiplierBp(payload.defaultMultiplierBp);
            setNotice(`倍率规则已保存，共 ${formatCount(savedRules.length)} 条。`);
        } catch (saveError) {
            setRulesError(`保存倍率失败：${reasonOf(saveError, "请稍后重试")}`);
        } finally {
            setRulesSaving(false);
        }
    }, [draftRules]);

    const openPriceEditor = useCallback(
        (price: ModelPrice | null) => {
            setPriceEditor({ price });
            setPriceFormError("");
            setError("");
            setNotice("");
            priceForm.setFieldsValue(price ? priceFormValuesOf(price) : emptyPrice);
        },
        [priceForm],
    );

    const submitPrice = useCallback(
        async (values: PriceFormValues) => {
            if (!priceEditor) return;
            setPriceSaving(true);
            setPriceFormError("");
            try {
                if (priceEditor.price) {
                    await updateAdminModelPrice(priceEditor.price.id, priceInputOf(values));
                    setNotice(`模型「${values.modelKey.trim()}」的定价已更新。`);
                } else {
                    await createAdminModelPrice(priceInputOf(values));
                    setNotice(`模型「${values.modelKey.trim()}」的定价已创建。`);
                }
                setPriceEditor(null);
                await load();
            } catch (saveError) {
                setPriceFormError(`保存定价失败：${reasonOf(saveError, "请稍后重试")}`);
            } finally {
                setPriceSaving(false);
            }
        },
        [load, priceEditor],
    );

    const confirmDeletePrice = useCallback(async () => {
        if (!priceTarget) return;
        setPriceDeleting(true);
        setPriceDeleteError("");
        try {
            await deleteAdminModelPrice(priceTarget.id);
            setNotice(`模型「${priceTarget.modelKey}」的定价已删除，之后按规则里的上游价与倍率重新回填。`);
            setPriceTarget(null);
            await load();
        } catch (deleteFailure) {
            setPriceDeleteError(reasonOf(deleteFailure, "删除定价失败"));
        } finally {
            setPriceDeleting(false);
        }
    }, [load, priceTarget]);

    const submitPreview = useCallback(async (values: PreviewFormValues) => {
        setPreviewing(true);
        setPreviewError("");
        setPreview(null);
        try {
            const payload = await previewAdminModelPrice({
                modelKey: values.modelKey.trim(),
                vendorCode: (values.vendorCode ?? "").trim(),
                capability: values.capability,
                tokenTier: values.capability === "TEXT" ? values.tokenTier : "",
                upstreamUnitPrice: numberOrNull(values.upstreamUnitPrice),
            });
            setPreview(payload.resolution);
        } catch (previewFailure) {
            setPreviewError(`试算失败：${reasonOf(previewFailure, "请稍后重试")}`);
        } finally {
            setPreviewing(false);
        }
    }, []);

    const priceColumns: TableProps<ModelPrice>["columns"] = [
        {
            title: "模型标识",
            dataIndex: "modelKey",
            key: "modelKey",
            width: 200,
            render: (value: string, row) => (
                <span className="admin-user-cell">
                    <code>{value}</code>
                    <span className="admin-user-sub">{row.vendorCode || "未标注厂商"}</span>
                </span>
            ),
        },
        { title: "能力", dataIndex: "capability", key: "capability", width: 80, render: (value: string) => <Tag>{priceCapabilityLabel(value)}</Tag> },
        { title: "档位", dataIndex: "tokenTier", key: "tokenTier", width: 108, render: (value: string) => <span className="admin-user-sub">{tierLabel(value)}</span> },
        { title: "单位", dataIndex: "unit", key: "unit", width: 108, render: (value: string) => <span className="admin-user-sub">{unitLabel(value)}</span> },
        {
            title: "上游单价（分/单位）",
            dataIndex: "upstreamUnitPrice",
            key: "upstreamUnitPrice",
            width: 150,
            render: (value: number | null) =>
                value === null || value === undefined ? <span className="admin-user-sub">未定价</span> : <span>{formatUnitPrice(value)}</span>,
        },
        {
            title: "倍率",
            dataIndex: "multiplierBp",
            key: "multiplierBp",
            width: 96,
            render: (value: number | null) =>
                value === null || value === undefined ? <span className="admin-user-sub">按上级规则</span> : <span>×{formatMultiplierBp(value)}</span>,
        },
        {
            title: "售价（分/单位）",
            dataIndex: "sellUnitPrice",
            key: "sellUnitPrice",
            width: 140,
            render: (value: number | null) =>
                value === null || value === undefined ? <span className="admin-user-sub">按倍率计算</span> : <span>{formatSellPrice(value)}</span>,
        },
        {
            title: "启用",
            dataIndex: "enabled",
            key: "enabled",
            width: 84,
            render: (value: boolean) => (
                <span className="flex items-center gap-2" style={{ fontSize: "var(--fs-caption)" }}>
                    <i className={`admin-dot ${value ? "is-on" : "is-off"}`} aria-hidden />
                    {value ? "启用" : "停用"}
                </span>
            ),
        },
        { title: "备注", dataIndex: "note", key: "note", width: 180, render: (value: string) => <span className="admin-user-sub">{value || "—"}</span> },
        { title: "更新时间", dataIndex: "updatedAt", key: "updatedAt", width: 168, render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span> },
        {
            title: "操作",
            key: "actions",
            width: 140,
            render: (_value, row) => (
                <span className="admin-settings-inline">
                    <Button size="small" type="text" icon={<Pencil className="size-3.5" />} onClick={() => openPriceEditor(row)}>
                        编辑
                    </Button>
                    <Button
                        size="small"
                        type="text"
                        danger
                        icon={<Trash2 className="size-3.5" />}
                        onClick={() => {
                            setPriceDeleteError("");
                            setPriceTarget(row);
                        }}
                    >
                        删除
                    </Button>
                </span>
            ),
        },
    ];

    const ruleColumns: TableProps<MarkupRuleDraft>["columns"] = [
        {
            title: "作用域",
            dataIndex: "scope",
            key: "scope",
            width: 110,
            render: (value: MarkupScope, row) => (
                <Select
                    value={value}
                    options={scopeOptions}
                    style={{ width: 88 }}
                    onChange={(next) => updateDraft(row.key, { scope: next, target: next === "GLOBAL" ? "*" : row.target })}
                />
            ),
        },
        {
            title: "目标",
            dataIndex: "target",
            key: "target",
            width: 220,
            render: (value: string, row) =>
                row.scope === "GLOBAL" ? (
                    // 全局规则固定 *：它是兜底，不允许只对某个目标生效。
                    <span className="admin-user-sub">全部（*）</span>
                ) : (
                    <Input
                        value={value}
                        placeholder={row.scope === "CAPABILITY" ? "TEXT" : row.scope === "VENDOR" ? "openai" : "gpt-4o"}
                        onChange={(event) => updateDraft(row.key, { target: event.target.value })}
                    />
                ),
        },
        {
            title: "倍率",
            dataIndex: "multiplier",
            key: "multiplier",
            width: 190,
            render: (value: number, row) => (
                <span className="flex items-center gap-2">
                    <InputNumber
                        min={0}
                        step={0.05}
                        precision={2}
                        style={{ width: 96 }}
                        value={value}
                        onChange={(next) => updateDraft(row.key, { multiplier: numberOrNull(next) ?? 0 })}
                    />
                    <span className="admin-user-sub">×{formatMultiplierBp(Math.round((value || 0) * basisPointPerUnit))}</span>
                </span>
            ),
        },
        {
            title: "备注",
            dataIndex: "note",
            key: "note",
            render: (value: string, row) => <Input value={value} placeholder="为什么这么定" onChange={(event) => updateDraft(row.key, { note: event.target.value })} />,
        },
        {
            title: "操作",
            key: "actions",
            width: 90,
            render: (_value, row) => (
                <Button size="small" type="text" danger icon={<Trash2 className="size-3.5" />} onClick={() => removeDraftRule(row.key)}>
                    删除
                </Button>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">模型定价</h2>
                    <p className="admin-section-desc">
                        售价 = 上游成本 × 倍率。单价现在是占位，按上游真实价格回填即可；上游单价为空时显示「未定价」，不会当成 0 参与计费。
                    </p>
                </div>
                <div className="admin-settings-inline">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button type="primary" icon={<Plus className="size-3.5" />} onClick={() => openPriceEditor(null)}>
                        新增定价
                    </Button>
                </div>
            </div>

            {error ? (
                <div className="admin-notice is-error">
                    <span>{error}</span>
                    <Button size="small" type="text" onClick={() => void load()}>
                        重试
                    </Button>
                </div>
            ) : null}
            {notice ? (
                <div className="admin-notice is-ok">
                    <span>{notice}</span>
                </div>
            ) : null}

            {loading && !prices.length && !rules.length ? (
                <div className="admin-card admin-empty">正在加载定价…</div>
            ) : (
                <>
                    <div className="admin-card">
                        <div className="admin-card-head">
                            <span className="flex items-center gap-2">
                                <BadgePercent className="size-4" />
                                <b style={{ fontSize: "var(--fs-body)" }}>加价倍率</b>
                                <span className="admin-console-mono">默认 ×{formatMultiplierBp(defaultMultiplierBp)}</span>
                            </span>
                            <span className="flex items-center gap-2">
                                <Button size="small" icon={<Plus className="size-3.5" />} onClick={addDraftRule}>
                                    新增规则
                                </Button>
                                <Button size="small" type="primary" loading={rulesSaving} disabled={!rulesDirty} onClick={() => void saveRules()}>
                                    保存
                                </Button>
                            </span>
                        </div>
                        <div className="admin-card-pad flex flex-col gap-3">
                            <div className="admin-meta-grid">
                                <div className="admin-kv">
                                    <span className="admin-kv-label">default multiplier</span>
                                    <span className="admin-kv-value is-mono">×{formatMultiplierBp(defaultMultiplierBp)}</span>
                                </div>
                                <div className="admin-kv">
                                    <span className="admin-kv-label">basis points</span>
                                    <span className="admin-kv-value is-mono">{defaultMultiplierBp ?? "—"} bp</span>
                                </div>
                                <div className="admin-kv">
                                    <span className="admin-kv-label">rules</span>
                                    <span className="admin-kv-value is-mono">{formatCount(draftRules.length)}</span>
                                </div>
                            </div>
                            <p className="admin-inline-note">
                                默认倍率由服务端下发，页面只读。规则按「模型 → 厂商 → 能力 → 全局」逐级覆盖，保存是整表提交：中间态不会被并发的计费请求读到。
                                {rulesDirty ? " 当前有未保存的改动。" : ""}
                            </p>
                            {rulesError ? (
                                <div className="admin-notice is-error">
                                    <span>{rulesError}</span>
                                </div>
                            ) : null}
                            <Table<MarkupRuleDraft>
                                rowKey="key"
                                size="small"
                                dataSource={draftRules}
                                columns={ruleColumns}
                                pagination={false}
                                locale={{ emptyText: "还没有倍率规则。没有命中任何规则时按上面的默认倍率计算。" }}
                            />
                        </div>
                    </div>

                    <div className="admin-card">
                        <div className="admin-card-head">
                            <span className="flex items-center gap-2">
                                <Calculator className="size-4" />
                                <b style={{ fontSize: "var(--fs-body)" }}>模型单价</b>
                                <span className="admin-console-mono">prices · {prices.length}</span>
                            </span>
                            <span className="admin-user-sub">
                                金额一律整数分：TEXT 按分/百万token（缓存命中 / 未命中 / 输出三档分开）、IMAGE 按分/张、SECOND 按分/秒、REQUEST 按分/次，页面不做元与分的换算。
                            </span>
                        </div>
                        <Table<ModelPrice>
                            rowKey="id"
                            size="small"
                            loading={loading}
                            dataSource={prices}
                            columns={priceColumns}
                            scroll={{ x: 1320 }}
                            pagination={false}
                            locale={{ emptyText: "还没有定价记录。点右上角「新增定价」，把上游价格回填进来。" }}
                        />
                    </div>

                    <div className="admin-card">
                        <div className="admin-card-head">
                            <span className="flex items-center gap-2">
                                <BadgePercent className="size-4" />
                                <b style={{ fontSize: "var(--fs-body)" }}>试算</b>
                                <span className="admin-console-mono">preview</span>
                            </span>
                            <span className="admin-user-sub">试算不写库：先看清这条模型最终按哪个倍率、卖多少钱，再决定要不要落价格。</span>
                        </div>
                        <div className="admin-card-pad flex flex-col gap-3">
                            {previewError ? (
                                <div className="admin-notice is-error">
                                    <span>{previewError}</span>
                                </div>
                            ) : null}
                            <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
                                <Form form={previewForm} layout="vertical" initialValues={emptyPreview} onFinish={(values) => void submitPreview(values)}>
                                    <div className="admin-meta-grid">
                                        <Form.Item label="模型标识" name="modelKey" rules={[{ required: true, message: "请填写模型标识" }]}>
                                            <Input placeholder="gpt-4o" />
                                        </Form.Item>
                                        <Form.Item label="厂商 code" name="vendorCode">
                                            <Input placeholder="openai" />
                                        </Form.Item>
                                        <Form.Item label="能力" name="capability">
                                            <Select options={capabilityOptions} />
                                        </Form.Item>
                                        <Form.Item label="token 档位" name="tokenTier" extra="文本必填；其余能力选「不区分」。">
                                            <Select options={tierOptions} />
                                        </Form.Item>
                                        <Form.Item label="上游单价（分/单位）" name="upstreamUnitPrice" extra="留空 = 未定价：不出售价，也不会按 0 计算。">
                                            <InputNumber min={0} precision={0} style={{ width: "100%" }} placeholder="未定价" />
                                        </Form.Item>
                                    </div>
                                    <div className="admin-settings-actions">
                                        <Button type="primary" htmlType="submit" loading={previewing}>
                                            试算
                                        </Button>
                                        <span className="admin-user-sub">上游单价按各行自己的单位填，例如 TEXT 就是分/百万 token。</span>
                                    </div>
                                </Form>
                                <div className="admin-meta-grid">
                                    <div className="admin-kv">
                                        <span className="admin-kv-label">multiplier</span>
                                        <span className="admin-kv-value is-mono">
                                            {preview ? `×${formatMultiplierBp(preview.multiplierBp)}` : "—"}
                                        </span>
                                    </div>
                                    <div className="admin-kv">
                                        <span className="admin-kv-label">source</span>
                                        <span className="admin-kv-value">{preview ? preview.source || "—" : "—"}</span>
                                    </div>
                                    <div className="admin-kv">
                                        <span className="admin-kv-label">sell price</span>
                                        <span className="admin-kv-value is-mono">
                                            {preview ? (preview.priced && preview.sellUnitPrice !== null ? formatSellPrice(preview.sellUnitPrice) : "未定价，无法计算售价") : "—"}
                                        </span>
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </>
            )}

            <Modal
                open={priceEditor !== null}
                width={720}
                title={priceEditor?.price ? `编辑定价 · ${priceEditor.price.modelKey}` : "新增定价"}
                okText="保存"
                cancelText="取消"
                confirmLoading={priceSaving}
                onOk={() => priceForm.submit()}
                onCancel={() => {
                    setPriceEditor(null);
                    setPriceFormError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    {priceFormError ? (
                        <div className="admin-notice is-error">
                            <span>{priceFormError}</span>
                        </div>
                    ) : null}
                    <Form
                        form={priceForm}
                        layout="vertical"
                        initialValues={emptyPrice}
                        className="admin-form-narrow"
                        // 切能力时同步口径：单价单位与 token 档位都由能力决定，让用户先改能力
                        // 再自己想起来改另外两格，漏改的那次会被服务端拒掉，而拒的原因看起来
                        // 跟"我只是换个能力"毫不相关。
                        onValuesChange={(changed) => {
                            if (!("capability" in changed)) return;
                            const next = changed.capability as ModelPriceCapability;
                            priceForm.setFieldsValue({
                                unit: next === "TEXT" ? "TOKEN_1M" : next === "IMAGE" ? "IMAGE" : "SECOND",
                                tokenTier: next === "TEXT" ? "INPUT" : "",
                            });
                        }}
                        onFinish={(values) => void submitPrice(values)}
                    >
                        <Form.Item label="模型标识" name="modelKey" rules={[{ required: true, message: "请填写模型标识" }]}>
                            <Input placeholder="gpt-4o" />
                        </Form.Item>
                        <Form.Item label="厂商 code" name="vendorCode" extra="对应「模型厂商」里的厂商标识；留空表示平台自有。" >
                            <Input placeholder="openai" />
                        </Form.Item>
                        <div className="grid gap-3 md:grid-cols-3">
                            <Form.Item label="能力" name="capability">
                                <Select options={capabilityOptions} />
                            </Form.Item>
                            <Form.Item label="token 档位" name="tokenTier" extra="文本必填；其余能力必须选「不区分」。">
                                <Select options={tierOptions} disabled={priceFormCapability !== "TEXT"} />
                            </Form.Item>
                            <Form.Item label="计价单位" name="unit" extra="决定这个单价代表多少量。">
                                <Select options={unitOptions} />
                            </Form.Item>
                        </div>
                        <div className="grid gap-3 md:grid-cols-2">
                            <Form.Item label="上游单价（分/单位）" name="upstreamUnitPrice" extra="填上游真实价格；留空表示还没定价，不会当成 0。">
                                <InputNumber min={0} precision={0} style={{ width: "100%" }} placeholder="未定价" />
                            </Form.Item>
                            <Form.Item label="倍率" name="multiplier" extra="填 1.2 表示 ×1.2；留空走上级规则。">
                                <InputNumber min={0} step={0.05} precision={2} style={{ width: "100%" }} placeholder="按上级规则" />
                            </Form.Item>
                        </div>
                        <Form.Item label="售价（分/单位）" name="sellUnitPrice" extra="留空表示按倍率计算；只有需要单独定价（例如促销价）时才填。">
                            <InputNumber min={0} precision={0} style={{ width: 200 }} placeholder="按倍率计算" />
                        </Form.Item>
                        <Form.Item label="启用" name="enabled" valuePropName="checked">
                            <Switch checkedChildren="启用" unCheckedChildren="停用" />
                        </Form.Item>
                        <Form.Item label="备注" name="note">
                            <Input.TextArea rows={2} placeholder="价格来源、生效时间等" />
                        </Form.Item>
                    </Form>
                </div>
            </Modal>

            <Modal
                open={priceTarget !== null}
                title="删除定价？"
                okText="确认删除"
                okButtonProps={{ danger: true }}
                cancelText="取消"
                confirmLoading={priceDeleting}
                onOk={() => void confirmDeletePrice()}
                onCancel={() => {
                    setPriceTarget(null);
                    setPriceDeleteError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.8 }}>
                        即将删除模型「{priceTarget?.modelKey}」的定价记录。删除后按倍率规则回落到上级单价；如果只是暂时不上架，请改用「编辑」里的停用。
                    </p>
                    {priceDeleteError ? (
                        <div className="admin-notice is-error">
                            <span>{priceDeleteError}</span>
                        </div>
                    ) : null}
                </div>
            </Modal>
        </div>
    );
}
