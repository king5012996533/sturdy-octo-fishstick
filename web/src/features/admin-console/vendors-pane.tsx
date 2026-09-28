import { Button, Drawer, Form, Input, InputNumber, Modal, Switch, Table, Tag, type TableProps } from "antd";
import { Cable, CloudDownload, Pencil, Plus, RefreshCw, Search, Trash2, Zap } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import {
    createAdminVendor,
    createAdminVendorCredential,
    deleteAdminVendor,
    deleteAdminVendorCredential,
    importAdminVendorCredentialModels,
    listAdminVendorCatalog,
    listAdminVendorCredentialModels,
    listAdminVendorCredentials,
    listAdminVendors,
    probeAdminVendorCredential,
    updateAdminVendor,
    updateAdminVendorCredential,
    type Credential,
    type CredentialInput,
    type Vendor,
    type VendorCatalogItem,
    type VendorInput,
    type VendorModel,
    type VendorUpdateInput,
} from "./api-vendors";

/** 能力枚举固定四种，映射成中文用于标签展示；未知值原样回显，便于发现后端新增能力。 */
const capabilityLabels: Record<string, string> = { TEXT: "文本", IMAGE: "图片", VIDEO: "视频", AUDIO: "音频" };

export function capabilityLabel(capability: string) {
    return capabilityLabels[capability] ?? (capability || "—");
}

/** 内置厂商由平台预置，标识与能力由版本迭代维护，运营只在自建厂商上动手。 */
export function vendorKindLabel(kind: Vendor["kind"]) {
    return kind === "BUILTIN" ? "内置" : "自建";
}

type VendorFormValues = { name: string; docsUrl: string; sortOrder: number; enabled: boolean };

type JoinFormValues = {
    credentialName: string;
    baseUrl: string;
    apiKey: string;
    secretKey: string;
    concurrencyLimit: number;
    enabled: boolean;
    initialModels: string;
};

type CredentialFormValues = {
    name: string;
    baseUrl: string;
    apiKey: string;
    secretKey: string;
    concurrencyLimit: number;
    weight: number;
    enabled: boolean;
    initialModels: string;
};

const emptyJoin: JoinFormValues = { credentialName: "", baseUrl: "", apiKey: "", secretKey: "", concurrencyLimit: 3, enabled: true, initialModels: "" };

const emptyCredential: CredentialFormValues = { name: "", baseUrl: "", apiKey: "", secretKey: "", concurrencyLimit: 3, weight: 100, enabled: true, initialModels: "" };

/** 密钥只写不读：编辑时留空即保持原值，因此这里把空串收成 undefined 再提交。 */
function optionalText(value?: string) {
    const trimmed = (value ?? "").trim();
    return trimmed ? trimmed : undefined;
}

function optionalNumber(value?: number) {
    return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

/** 初始模型每行一个：运营从上游文档复制的一般就是换行列表，不做逗号切分以免误伤模型名。 */
function modelLinesOf(value?: string) {
    const lines = (value ?? "")
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter(Boolean);
    return lines.length ? lines : undefined;
}

function reasonOf(error: unknown, fallback: string) {
    return error instanceof Error && error.message ? error.message : fallback;
}

function credentialFormValuesOf(credential: Credential): CredentialFormValues {
    return {
        name: credential.name,
        baseUrl: credential.baseUrl,
        // 密钥不回显：留空就是"不修改"，服务端也只认缺省字段。
        apiKey: "",
        secretKey: "",
        concurrencyLimit: 3,
        weight: credential.weight,
        enabled: credential.enabled,
        initialModels: "",
    };
}

function credentialInputOf(values: CredentialFormValues | JoinFormValues, options: { includeInitialModels: boolean }): CredentialInput {
    const named = "credentialName" in values ? { name: values.credentialName } : { name: values.name };
    return {
        name: named.name.trim(),
        baseUrl: values.baseUrl.trim(),
        apiKey: optionalText(values.apiKey),
        secretKey: optionalText(values.secretKey),
        concurrencyLimit: optionalNumber(values.concurrencyLimit),
        weight: optionalNumber("weight" in values ? values.weight : undefined),
        enabled: values.enabled,
        models: options.includeInitialModels ? modelLinesOf(values.initialModels) : undefined,
    };
}

/**
 * 模型厂商。
 *
 * 平台持有的上游厂商与密钥在这里配置；前台只看到模型目录，看不到真实地址与凭证。
 * 面板分两层：卡片是厂商概览（能力、凭证与模型数量、启停），抽屉里才是凭证与模型的
 * 明细——凭证是密钥的载体，混在卡片上会让"哪条线路出问题"变得看不出来。
 *
 * 全局的 antd message 在本项目是关闭的，所有反馈都走页面内的 .admin-notice。
 */
export function VendorsPane() {
    const [vendors, setVendors] = useState<Vendor[]>([]);
    const [catalog, setCatalog] = useState<VendorCatalogItem[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [togglingIds, setTogglingIds] = useState<string[]>([]);

    const [detail, setDetail] = useState<Vendor | null>(null);
    const [detailOpen, setDetailOpen] = useState(false);
    const [credentials, setCredentials] = useState<Credential[]>([]);
    const [credentialsLoading, setCredentialsLoading] = useState(false);
    const [activeCredentialId, setActiveCredentialId] = useState("");
    const [models, setModels] = useState<VendorModel[]>([]);
    const [modelsLoading, setModelsLoading] = useState(false);
    const [detailError, setDetailError] = useState("");
    const [detailNotice, setDetailNotice] = useState("");
    const [probingId, setProbingId] = useState("");
    const [vendorSaving, setVendorSaving] = useState(false);

    const [credentialEditor, setCredentialEditor] = useState<{ credential: Credential | null } | null>(null);
    const [credentialSaving, setCredentialSaving] = useState(false);
    const [credentialFormError, setCredentialFormError] = useState("");
    const [credentialTarget, setCredentialTarget] = useState<Credential | null>(null);
    const [credentialDeleting, setCredentialDeleting] = useState(false);
    const [credentialDeleteError, setCredentialDeleteError] = useState("");
    const [vendorDeleteOpen, setVendorDeleteOpen] = useState(false);
    const [vendorDeleting, setVendorDeleting] = useState(false);
    const [vendorDeleteError, setVendorDeleteError] = useState("");

    const [importOpen, setImportOpen] = useState(false);
    const [importCandidates, setImportCandidates] = useState<string[]>([]);
    const [importSelected, setImportSelected] = useState<string[]>([]);
    const [importBusy, setImportBusy] = useState(false);
    const [importError, setImportError] = useState("");

    const [joinOpen, setJoinOpen] = useState(false);
    const [joinKeyword, setJoinKeyword] = useState("");
    const [joinCode, setJoinCode] = useState("");
    const [joinSaving, setJoinSaving] = useState(false);
    const [joinError, setJoinError] = useState("");
    /** 第二步失败的厂商：厂商已建、凭据未建，抽屉停在这里只补第二步。 */
    const [pendingVendor, setPendingVendor] = useState<Vendor | null>(null);

    const [vendorForm] = Form.useForm<VendorFormValues>();
    const [credentialForm] = Form.useForm<CredentialFormValues>();
    const [joinForm] = Form.useForm<JoinFormValues>();

    const activeCredential = useMemo(() => credentials.find((item) => item.id === activeCredentialId) ?? null, [credentials, activeCredentialId]);

    const filteredCatalog = useMemo(() => {
        const keyword = joinKeyword.trim().toLowerCase();
        if (!keyword) return catalog;
        return catalog.filter((item) => item.name.toLowerCase().includes(keyword) || item.code.toLowerCase().includes(keyword));
    }, [catalog, joinKeyword]);

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const [vendorPayload, catalogPayload] = await Promise.all([listAdminVendors(), listAdminVendorCatalog()]);
            setVendors(vendorPayload.vendors ?? []);
            setCatalog(catalogPayload.catalog ?? []);
        } catch (loadError) {
            setError(reasonOf(loadError, "加载厂商失败"));
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const loadCredentials = useCallback(async (vendorId: string) => {
        if (!vendorId) {
            setCredentials([]);
            return;
        }
        setCredentialsLoading(true);
        setDetailError("");
        try {
            const payload = await listAdminVendorCredentials(vendorId);
            const items = payload.credentials ?? [];
            setCredentials(items);
            setActiveCredentialId((current) => (items.some((item) => item.id === current) ? current : (items[0]?.id ?? "")));
        } catch (loadError) {
            setCredentials([]);
            setDetailError(reasonOf(loadError, "加载凭证失败"));
        } finally {
            setCredentialsLoading(false);
        }
    }, []);

    const loadModels = useCallback(async (vendorId: string, credentialId: string) => {
        if (!vendorId || !credentialId) {
            setModels([]);
            return;
        }
        setModelsLoading(true);
        try {
            const payload = await listAdminVendorCredentialModels(vendorId, credentialId);
            setModels(payload.models ?? []);
        } catch (loadError) {
            setModels([]);
            setDetailError(reasonOf(loadError, "加载凭证模型失败"));
        } finally {
            setModelsLoading(false);
        }
    }, []);

    useEffect(() => {
        if (!detail?.id || !activeCredentialId) {
            setModels([]);
            return;
        }
        void loadModels(detail.id, activeCredentialId);
    }, [detail?.id, activeCredentialId, loadModels]);

    const openDetail = useCallback(
        (vendor: Vendor) => {
            setDetail(vendor);
            setDetailOpen(true);
            setDetailError("");
            setDetailNotice("");
            setActiveCredentialId("");
            setCredentials([]);
            setModels([]);
            vendorForm.setFieldsValue({ name: vendor.name, docsUrl: vendor.docsUrl, sortOrder: vendor.sortOrder, enabled: vendor.enabled });
            void loadCredentials(vendor.id);
        },
        [loadCredentials, vendorForm],
    );

    /**
     * 启停开关走乐观更新：开关必须即时响应，失败再回滚并说明原因。
     * 不做「先请求再变状态」——那会让每次点击都空转一次往返才看到结果。
     */
    const toggleVendor = useCallback(
        async (vendor: Vendor, enabled: boolean) => {
            setError("");
            setNotice("");
            setTogglingIds((current) => [...current, vendor.id]);
            setVendors((current) => current.map((item) => (item.id === vendor.id ? { ...item, enabled } : item)));
            try {
                const payload = await updateAdminVendor(vendor.id, { enabled });
                setVendors((current) => current.map((item) => (item.id === payload.vendor.id ? payload.vendor : item)));
                setNotice(`厂商「${vendor.name}」已${enabled ? "启用" : "停用"}。`);
            } catch (toggleError) {
                setVendors((current) => current.map((item) => (item.id === vendor.id ? { ...item, enabled: vendor.enabled } : item)));
                setError(`厂商「${vendor.name}」${enabled ? "启用" : "停用"}失败：${reasonOf(toggleError, "请稍后重试")}`);
            } finally {
                setTogglingIds((current) => current.filter((id) => id !== vendor.id));
            }
        },
        [],
    );

    const submitVendor = useCallback(
        async (values: VendorFormValues) => {
            if (!detail) return;
            setVendorSaving(true);
            setDetailError("");
            setDetailNotice("");
            const input: VendorUpdateInput = {
                name: values.name.trim(),
                docsUrl: (values.docsUrl ?? "").trim(),
                sortOrder: Number(values.sortOrder ?? 0),
                enabled: values.enabled,
            };
            try {
                const payload = await updateAdminVendor(detail.id, input);
                setDetail(payload.vendor);
                setVendors((current) => current.map((item) => (item.id === payload.vendor.id ? payload.vendor : item)));
                setDetailNotice(`厂商「${payload.vendor.name}」已保存。`);
            } catch (saveError) {
                setDetailError(`保存厂商失败：${reasonOf(saveError, "请稍后重试")}`);
            } finally {
                setVendorSaving(false);
            }
        },
        [detail],
    );

    const confirmDeleteVendor = useCallback(async () => {
        if (!detail) return;
        setVendorDeleting(true);
        setVendorDeleteError("");
        try {
            await deleteAdminVendor(detail.id);
            setVendorDeleteOpen(false);
            setDetailOpen(false);
            setDetail(null);
            setNotice(`厂商「${detail.name}」已删除。`);
            await load();
        } catch (deleteFailure) {
            // 已被模型或套餐引用这类原因只有服务端知道，原样留在弹窗里。
            setVendorDeleteError(reasonOf(deleteFailure, "删除厂商失败"));
        } finally {
            setVendorDeleting(false);
        }
    }, [detail, load]);

    const runProbe = useCallback(
        async (credential: Credential) => {
            setProbingId(credential.id);
            setDetailError("");
            setDetailNotice("");
            try {
                const payload = await probeAdminVendorCredential(credential.vendorId, credential.id);
                const probed = payload.models ?? [];
                setDetailNotice(`凭证「${credential.name}」连通正常，上游返回 ${formatCount(probed.length)} 个模型。`);
            } catch (probeError) {
                setDetailError(`凭证「${credential.name}」探测失败：${reasonOf(probeError, "请检查地址与密钥")}`);
            } finally {
                setProbingId("");
            }
        },
        [],
    );

    const openCredentialEditor = useCallback(
        (credential: Credential | null) => {
            setCredentialEditor({ credential });
            setCredentialFormError("");
            setDetailError("");
            setDetailNotice("");
            credentialForm.setFieldsValue(credential ? credentialFormValuesOf(credential) : emptyCredential);
        },
        [credentialForm],
    );

    const submitCredential = useCallback(
        async (values: CredentialFormValues) => {
            if (!detail || !credentialEditor) return;
            const editing = credentialEditor.credential;
            setCredentialSaving(true);
            setCredentialFormError("");
            try {
                if (editing) {
                    await updateAdminVendorCredential(detail.id, editing.id, credentialInputOf(values, { includeInitialModels: false }));
                    setDetailNotice(`凭证「${values.name.trim()}」已更新。`);
                } else {
                    await createAdminVendorCredential(detail.id, credentialInputOf(values, { includeInitialModels: true }));
                    setDetailNotice(`凭证「${values.name.trim()}」已添加。`);
                }
                setCredentialEditor(null);
                await Promise.all([loadCredentials(detail.id), load()]);
            } catch (saveError) {
                setCredentialFormError(`保存凭证失败：${reasonOf(saveError, "请稍后重试")}`);
            } finally {
                setCredentialSaving(false);
            }
        },
        [credentialEditor, detail, load, loadCredentials],
    );

    const confirmDeleteCredential = useCallback(async () => {
        if (!detail || !credentialTarget) return;
        setCredentialDeleting(true);
        setCredentialDeleteError("");
        try {
            await deleteAdminVendorCredential(detail.id, credentialTarget.id);
            setCredentialTarget(null);
            setDetailNotice(`凭证「${credentialTarget.name}」已删除。`);
            await Promise.all([loadCredentials(detail.id), load()]);
        } catch (deleteFailure) {
            setCredentialDeleteError(reasonOf(deleteFailure, "删除凭证失败"));
        } finally {
            setCredentialDeleting(false);
        }
    }, [credentialTarget, detail, load, loadCredentials]);

    /** 「从上游拉取」先探测再勾选：探测结果只作候选，没勾选的模型不写库。 */
    const openImport = useCallback(async () => {
        if (!detail || !activeCredential) return;
        setImportOpen(true);
        setImportBusy(true);
        setImportError("");
        setImportCandidates([]);
        setImportSelected([]);
        try {
            const payload = await probeAdminVendorCredential(detail.id, activeCredential.id);
            const existing = new Set(models.map((model) => model.providerModelKey || model.modelKey));
            const candidates = payload.models ?? [];
            setImportCandidates(candidates);
            setImportSelected(candidates.filter((name) => !existing.has(name)));
        } catch (probeError) {
            setImportError(`拉取上游模型目录失败：${reasonOf(probeError, "请检查地址与密钥")}`);
        } finally {
            setImportBusy(false);
        }
    }, [activeCredential, detail, models]);

    const submitImport = useCallback(async () => {
        if (!detail || !activeCredential || !importSelected.length) return;
        setImportBusy(true);
        setImportError("");
        try {
            const result = await importAdminVendorCredentialModels(detail.id, activeCredential.id, importSelected);
            setDetailNotice(result.added > 0 ? `已导入 ${formatCount(result.added)} 个模型。` : "所选模型都已存在，没有新增。");
            setImportOpen(false);
            await Promise.all([loadModels(detail.id, activeCredential.id), loadCredentials(detail.id), load()]);
        } catch (importFailure) {
            setImportError(`导入模型失败：${reasonOf(importFailure, "请稍后重试")}`);
        } finally {
            setImportBusy(false);
        }
    }, [activeCredential, detail, importSelected, load, loadCredentials, loadModels]);

    const openJoin = useCallback(() => {
        setJoinOpen(true);
        setJoinKeyword("");
        setJoinCode("");
        setJoinError("");
        setPendingVendor(null);
        joinForm.setFieldsValue(emptyJoin);
    }, [joinForm]);

    /**
     * 接入厂商是两步：建厂商 → 建凭证。第二步失败时绝不整体回滚——
     * 厂商已经写库了，假装失败会让运营重建一次，最后得到两条重复的厂商。
     * 这里把待补凭据的厂商留在 pendingVendor 上，弹窗停在"只补第二步"的状态。
     */
    const submitJoin = useCallback(
        async (values: JoinFormValues) => {
            const credentialName = values.credentialName.trim();
            setJoinSaving(true);
            setJoinError("");
            try {
                if (pendingVendor) {
                    try {
                        await createAdminVendorCredential(pendingVendor.id, credentialInputOf(values, { includeInitialModels: true }));
                    } catch (retryError) {
                        setJoinError(`凭证仍未保存：${reasonOf(retryError, "请稍后重试")}。厂商「${pendingVendor.name}」已在库中，可以继续重试或到凭证列表里补。`);
                        return;
                    }
                    setJoinOpen(false);
                    setNotice(`厂商「${pendingVendor.name}」的凭证「${credentialName}」已保存。`);
                    setPendingVendor(null);
                    await load();
                    return;
                }

                const item = catalog.find((entry) => entry.code === joinCode);
                if (!item) {
                    setJoinError("请先在左侧选择要接入的厂商。");
                    return;
                }
                const input: VendorInput = {
                    code: item.code,
                    name: item.name,
                    capabilities: item.capabilities,
                    protocols: item.protocols,
                    docsUrl: item.docsUrl,
                    sortOrder: 0,
                    enabled: values.enabled,
                };
                let created: Vendor;
                try {
                    const payload = await createAdminVendor(input);
                    created = payload.vendor;
                } catch (createError) {
                    setJoinError(`厂商创建失败：${reasonOf(createError, "请稍后重试")}`);
                    return;
                }
                try {
                    await createAdminVendorCredential(created.id, credentialInputOf(values, { includeInitialModels: true }));
                } catch (credentialError) {
                    setPendingVendor(created);
                    setJoinError(
                        `厂商已建、凭据未建：厂商「${created.name}」已经写库，凭证未保存（${reasonOf(credentialError, "请稍后重试")}）。` +
                            "补好上面的凭证表单后点「重试保存凭据」即可，不需要重新创建厂商。",
                    );
                    // 让列表先出现这家厂商：否则运营会以为整个动作失败了。
                    await load();
                    return;
                }
                setJoinOpen(false);
                setNotice(`厂商「${created.name}」已接入，凭证「${credentialName}」已保存。`);
                await load();
            } finally {
                setJoinSaving(false);
            }
        },
        [catalog, joinCode, load, pendingVendor],
    );

    const modelColumns: TableProps<VendorModel>["columns"] = [
        { title: "模型标识", dataIndex: "modelKey", key: "modelKey", width: 220, render: (value: string) => <code>{value}</code> },
        {
            title: "上游标识",
            dataIndex: "providerModelKey",
            key: "providerModelKey",
            width: 200,
            render: (value: string, row) => <span className="admin-user-sub">{value || row.modelKey}</span>,
        },
        { title: "能力", dataIndex: "capability", key: "capability", width: 90, render: (value: string) => <Tag>{capabilityLabel(value)}</Tag> },
        {
            title: "状态",
            dataIndex: "enabled",
            key: "enabled",
            width: 90,
            render: (value: boolean) => (
                <span className="flex items-center gap-2" style={{ fontSize: "var(--fs-caption)" }}>
                    <i className={`admin-dot ${value === false ? "is-off" : "is-on"}`} aria-hidden />
                    {value === false ? "停用" : "启用"}
                </span>
            ),
        },
    ];

    const enabledCount = vendors.filter((vendor) => vendor.enabled).length;
    const builtinCount = vendors.filter((vendor) => vendor.kind === "BUILTIN").length;
    const modelTotal = vendors.reduce((total, vendor) => total + vendor.modelCount, 0);
    const credentialTotal = vendors.reduce((total, vendor) => total + vendor.credentialCount, 0);

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">模型厂商</h2>
                    <p className="admin-section-desc">平台持有的上游厂商与密钥在这里配置；前台只看到模型目录，看不到真实地址与凭证。</p>
                </div>
                <div className="admin-settings-inline">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button type="primary" icon={<Plus className="size-3.5" />} onClick={openJoin}>
                        接入厂商
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

            <div className="admin-metric-grid">
                <div className="admin-metric">
                    <span className="admin-metric-label">厂商总数</span>
                    <span className="admin-metric-value">{formatCount(vendors.length)}</span>
                    <span className="admin-metric-note">含已停用的厂商</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">内置厂商</span>
                    <span className="admin-metric-value">{formatCount(builtinCount)}</span>
                    <span className="admin-metric-note">平台预置，不可删除</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">自建厂商</span>
                    <span className="admin-metric-value">{formatCount(vendors.length - builtinCount)}</span>
                    <span className="admin-metric-note">运营手工接入</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">已启用</span>
                    <span className="admin-metric-value">{formatCount(enabledCount)}</span>
                    <span className="admin-metric-note">全部厂商 {formatCount(credentialTotal)} 条凭据 · {formatCount(modelTotal)} 个模型</span>
                </div>
            </div>

            {loading && !vendors.length ? (
                <div className="admin-card admin-empty">正在加载厂商…</div>
            ) : vendors.length ? (
                <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                    {vendors.map((vendor) => (
                        /* 整卡可点：卡片是最自然的点击目标。开关单独 stopPropagation，
                           否则一次启停会顺手把抽屉打开；role/tabIndex 让键盘也能进详情。 */
                        <div
                            key={vendor.id}
                            className="admin-card admin-card-pad flex cursor-pointer flex-col gap-3"
                            role="button"
                            tabIndex={0}
                            aria-label={`查看厂商 ${vendor.name} 的详情`}
                            onClick={() => openDetail(vendor)}
                            onKeyDown={(event) => {
                                if (event.key === "Enter" || event.key === " ") {
                                    event.preventDefault();
                                    openDetail(vendor);
                                }
                            }}
                        >
                            <div className="flex items-start justify-between gap-3">
                                <span className="admin-channel-item-main">
                                    <span className="admin-channel-item-name">{vendor.name}</span>
                                    <span className="admin-channel-item-sub">{vendor.code}</span>
                                </span>
                                <span
                                    className="flex items-center gap-2"
                                    onClick={(event) => event.stopPropagation()}
                                    onKeyDown={(event) => event.stopPropagation()}
                                >
                                    <Switch
                                        size="small"
                                        checked={vendor.enabled}
                                        loading={togglingIds.includes(vendor.id)}
                                        aria-label={`${vendor.enabled ? "停用" : "启用"}厂商 ${vendor.name}`}
                                        onChange={(checked) => void toggleVendor(vendor, checked)}
                                    />
                                </span>
                            </div>
                            <div className="flex flex-wrap items-center gap-1.5">
                                <Tag color={vendor.kind === "BUILTIN" ? "blue" : "default"}>{vendorKindLabel(vendor.kind)}</Tag>
                                {vendor.capabilities.map((capability) => (
                                    <Tag key={capability}>{capabilityLabel(capability)}</Tag>
                                ))}
                            </div>
                            <div className="admin-settings-inline">
                                <span>
                                    凭证 {formatCount(vendor.credentialCount)} · 模型 {formatCount(vendor.modelCount)}
                                </span>
                            </div>
                        </div>
                    ))}
                </div>
            ) : (
                <div className="admin-card admin-empty">还没有接入厂商。点右上角「接入厂商」，先从目录里挑一家，再填地址与密钥。</div>
            )}

            <Drawer
                open={detailOpen}
                size={860}
                title={detail ? `厂商详情 · ${detail.name}` : "厂商详情"}
                onClose={() => {
                    setDetailOpen(false);
                    setDetail(null);
                    setDetailError("");
                    setDetailNotice("");
                }}
            >
                {detail ? (
                    <div className="flex flex-col gap-3">
                        {detailError ? (
                            <div className="admin-notice is-error">
                                <span>{detailError}</span>
                                <Button size="small" type="text" onClick={() => void loadCredentials(detail.id)}>
                                    重试
                                </Button>
                            </div>
                        ) : null}
                        {detailNotice ? (
                            <div className="admin-notice is-ok">
                                <span>{detailNotice}</span>
                            </div>
                        ) : null}

                        <div className="admin-card">
                            <div className="admin-card-head">
                                <span className="flex min-w-0 items-center gap-2">
                                    <i className={`admin-dot ${detail.enabled ? "is-on" : "is-off"}`} aria-hidden />
                                    <b style={{ fontSize: "var(--fs-body)" }}>{detail.name}</b>
                                    <span className="admin-console-mono">{detail.code}</span>
                                </span>
                                <span className="flex items-center gap-2">
                                    <Tag color={detail.kind === "BUILTIN" ? "blue" : "default"}>{vendorKindLabel(detail.kind)}</Tag>
                                    <Button
                                        size="small"
                                        danger
                                        icon={<Trash2 className="size-3.5" />}
                                        disabled={detail.kind === "BUILTIN"}
                                        onClick={() => {
                                            setVendorDeleteError("");
                                            setVendorDeleteOpen(true);
                                        }}
                                    >
                                        删除厂商
                                    </Button>
                                </span>
                            </div>
                            <div className="admin-card-pad">
                                <Form form={vendorForm} layout="vertical" className="admin-form-narrow" onFinish={(values) => void submitVendor(values)}>
                                    <div className="admin-meta-grid">
                                        <Form.Item label="厂商名称" name="name" rules={[{ required: true, message: "请填写厂商名称" }, { max: 40, message: "名称不超过 40 个字符" }]}>
                                            <Input />
                                        </Form.Item>
                                        <Form.Item label="文档链接" name="docsUrl" extra="给运营查价格与模型清单用，不会展示给前台。">
                                            <Input placeholder="https://docs.example.com/pricing" />
                                        </Form.Item>
                                        <Form.Item label="排序" name="sortOrder" extra="数字越小越靠前。">
                                            <InputNumber min={0} precision={0} style={{ width: "100%" }} />
                                        </Form.Item>
                                        <Form.Item label="启用" name="enabled" valuePropName="checked" extra="停用后该厂商下的模型不再参与路由。">
                                            <Switch checkedChildren="启用" unCheckedChildren="停用" />
                                        </Form.Item>
                                    </div>
                                    <div className="admin-settings-actions">
                                        <Button type="primary" htmlType="submit" loading={vendorSaving}>
                                            保存厂商
                                        </Button>
                                        <span className="admin-user-sub">能力与协议来自厂商目录，不在这里改。</span>
                                    </div>
                                </Form>
                            </div>
                        </div>

                        <div className="admin-card">
                            <div className="admin-card-head">
                                <span className="flex items-center gap-2">
                                    <Cable className="size-4" />
                                    <b style={{ fontSize: "var(--fs-body)" }}>凭证</b>
                                    <span className="admin-console-mono">credentials · {credentials.length}</span>
                                </span>
                                <Button size="small" type="primary" icon={<Plus className="size-3.5" />} onClick={() => openCredentialEditor(null)}>
                                    新增凭证
                                </Button>
                            </div>
                            <div className="admin-card-pad">
                                {credentialsLoading && !credentials.length ? (
                                    <div className="admin-empty">正在加载凭证…</div>
                                ) : credentials.length ? (
                                    <div className="admin-channel-list">
                                        {credentials.map((credential) => (
                                            <div key={credential.id} className={`admin-channel-item${credential.id === activeCredentialId ? " is-active" : ""}`}>
                                                <i className={`admin-dot ${credential.enabled ? "is-on" : "is-off"}`} aria-hidden />
                                                <button
                                                    type="button"
                                                    className="admin-channel-item-main"
                                                    onClick={() => setActiveCredentialId(credential.id)}
                                                    aria-label={`查看凭证 ${credential.name} 的模型`}
                                                >
                                                    <span className="admin-channel-item-name">
                                                        {credential.name}
                                                        {/* 密钥只写不读，这里只能展示尾号，用来分辨同厂商的多条 key。 */}
                                                        {credential.hasApiKey ? ` · key ${credential.keyHint || "已设置"}` : " · 未设置密钥"}
                                                    </span>
                                                    <span className="admin-channel-item-sub">
                                                        {credential.baseUrl}
                                                        {` · 权重 ${formatCount(credential.weight)} · 模型 ${formatCount(credential.modelCount)}`}
                                                    </span>
                                                    {credential.lastError ? <span className="admin-user-sub">最近错误：{credential.lastError}</span> : null}
                                                </button>
                                                <span className="admin-settings-inline">
                                                    <Button
                                                        size="small"
                                                        type="text"
                                                        icon={<Zap className="size-3.5" />}
                                                        loading={probingId === credential.id}
                                                        onClick={() => void runProbe(credential)}
                                                    >
                                                        测连通性
                                                    </Button>
                                                    <Button size="small" type="text" icon={<Pencil className="size-3.5" />} onClick={() => openCredentialEditor(credential)}>
                                                        编辑
                                                    </Button>
                                                    <Button
                                                        size="small"
                                                        type="text"
                                                        danger
                                                        icon={<Trash2 className="size-3.5" />}
                                                        onClick={() => {
                                                            setCredentialDeleteError("");
                                                            setCredentialTarget(credential);
                                                        }}
                                                    >
                                                        删除
                                                    </Button>
                                                </span>
                                            </div>
                                        ))}
                                    </div>
                                ) : (
                                    <div className="admin-empty">还没有凭证。至少要有一条可用的地址与密钥，模型才能被调用。</div>
                                )}
                            </div>
                        </div>

                        <div className="admin-card">
                            <div className="admin-card-head">
                                <span className="flex items-center gap-2">
                                    <CloudDownload className="size-4" />
                                    <b style={{ fontSize: "var(--fs-body)" }}>模型</b>
                                    <span className="admin-console-mono">
                                        {activeCredential ? `${activeCredential.name} · ${models.length}` : "未选择凭证"}
                                    </span>
                                </span>
                                <span className="flex items-center gap-2">
                                    <Button
                                        size="small"
                                        icon={<RefreshCw className="size-3.5" />}
                                        disabled={!activeCredential}
                                        loading={modelsLoading}
                                        onClick={() => detail && activeCredential && void loadModels(detail.id, activeCredential.id)}
                                    >
                                        刷新
                                    </Button>
                                    <Button size="small" type="primary" icon={<CloudDownload className="size-3.5" />} disabled={!activeCredential} onClick={() => void openImport()}>
                                        从上游拉取
                                    </Button>
                                </span>
                            </div>
                            <Table<VendorModel>
                                rowKey="id"
                                size="small"
                                loading={modelsLoading}
                                dataSource={models}
                                columns={modelColumns}
                                pagination={false}
                                locale={{ emptyText: activeCredential ? "该凭证还没有模型，可以从上游拉取后导入。" : "先在上面的凭证里选一条，再管理它的模型。" }}
                            />
                        </div>
                    </div>
                ) : null}
            </Drawer>

            <Modal
                open={credentialEditor !== null}
                width={640}
                title={credentialEditor?.credential ? `编辑凭证 · ${credentialEditor.credential.name}` : `新增凭证 · ${detail?.name ?? ""}`}
                okText="保存"
                cancelText="取消"
                confirmLoading={credentialSaving}
                onOk={() => credentialForm.submit()}
                onCancel={() => {
                    setCredentialEditor(null);
                    setCredentialFormError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    {credentialFormError ? (
                        <div className="admin-notice is-error">
                            <span>{credentialFormError}</span>
                        </div>
                    ) : null}
                    <Form form={credentialForm} layout="vertical" initialValues={emptyCredential} className="admin-form-narrow" onFinish={(values) => void submitCredential(values)}>
                        <Form.Item label="凭证名称" name="name" rules={[{ required: true, message: "请填写凭证名称" }]}>
                            <Input placeholder="主线路" />
                        </Form.Item>
                        <Form.Item label="Base URL" name="baseUrl" rules={[{ required: true, message: "请填写 Base URL" }]}>
                            <Input placeholder="https://api.example.com/v1" />
                        </Form.Item>
                        <Form.Item
                            label="API Key"
                            name="apiKey"
                            extra={
                                credentialEditor?.credential
                                    ? credentialEditor.credential.hasApiKey
                                        ? "已设置，留空表示不修改。密钥不会回显，需要换 key 时才填。"
                                        : "未设置。密钥只写不读，保存后只保留尾号用于辨认。"
                                    : "密钥只写不读，保存后只保留尾号用于辨认。"
                            }
                        >
                            <Input.Password autoComplete="new-password" placeholder={credentialEditor?.credential?.hasApiKey ? "留空表示不修改" : "sk-…"} />
                        </Form.Item>
                        <Form.Item
                            label="Secret Key"
                            name="secretKey"
                            extra={credentialEditor?.credential?.hasSecretKey ? "已设置，留空表示不修改。" : "只有部分厂商需要，没有就留空。"}
                        >
                            <Input.Password autoComplete="new-password" placeholder={credentialEditor?.credential?.hasSecretKey ? "留空表示不修改" : "选填"} />
                        </Form.Item>
                        {credentialEditor?.credential ? null : (
                            <Form.Item label="并发上限" name="concurrencyLimit" extra="创建时用于初始化底层渠道，缺省由服务端给默认值。">
                                <InputNumber min={1} precision={0} style={{ width: 200 }} />
                            </Form.Item>
                        )}
                        <Form.Item label="权重" name="weight" extra="同厂商多条凭证按权重分流，数字越大分到的请求越多。">
                            <InputNumber min={0} precision={0} style={{ width: 200 }} />
                        </Form.Item>
                        {credentialEditor?.credential ? null : (
                            <Form.Item label="初始模型标识" name="initialModels" extra="每行一个；也可以先留空，保存后到模型区「从上游拉取」。">
                                <Input.TextArea rows={4} placeholder={"gpt-4o\ngpt-4o-mini"} />
                            </Form.Item>
                        )}
                        <Form.Item label="启用" name="enabled" valuePropName="checked">
                            <Switch checkedChildren="启用" unCheckedChildren="停用" />
                        </Form.Item>
                    </Form>
                </div>
            </Modal>

            <Modal
                open={credentialTarget !== null}
                title="删除凭证？"
                okText="确认删除"
                okButtonProps={{ danger: true }}
                cancelText="取消"
                confirmLoading={credentialDeleting}
                onOk={() => void confirmDeleteCredential()}
                onCancel={() => {
                    setCredentialTarget(null);
                    setCredentialDeleteError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.8 }}>
                        即将删除凭证「{credentialTarget?.name}」。它下面的模型会一并停止路由；如果只是暂时不用，请改用「编辑」里的停用。
                    </p>
                    {credentialDeleteError ? (
                        <div className="admin-notice is-error">
                            <span>{credentialDeleteError}</span>
                        </div>
                    ) : null}
                </div>
            </Modal>

            <Modal
                open={vendorDeleteOpen}
                title="删除厂商？"
                okText="确认删除"
                okButtonProps={{ danger: true }}
                cancelText="取消"
                confirmLoading={vendorDeleting}
                onOk={() => void confirmDeleteVendor()}
                onCancel={() => {
                    setVendorDeleteOpen(false);
                    setVendorDeleteError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.8 }}>
                        即将删除厂商「{detail?.name}」（{detail?.code}）。凭证与模型会一并下线，删除后不可恢复；如果只是暂时不用，请改为停用。
                    </p>
                    {vendorDeleteError ? (
                        <div className="admin-notice is-error">
                            <span>{vendorDeleteError}</span>
                        </div>
                    ) : null}
                </div>
            </Modal>

            <Modal
                open={importOpen}
                width={560}
                title={activeCredential ? `从上游拉取模型 · ${activeCredential.name}` : "从上游拉取模型"}
                okText={importSelected.length ? `导入所选 ${importSelected.length} 个` : "导入所选"}
                okButtonProps={{ disabled: !importSelected.length }}
                cancelText="取消"
                confirmLoading={importBusy}
                onOk={() => void submitImport()}
                onCancel={() => setImportOpen(false)}
            >
                <div className="flex flex-col gap-3">
                    {importError ? (
                        <div className="admin-notice is-error">
                            <span>{importError}</span>
                        </div>
                    ) : null}
                    <Table<string>
                        rowKey={(name) => name}
                        size="small"
                        loading={importBusy}
                        dataSource={importCandidates}
                        pagination={{ pageSize: 10, size: "small", hideOnSinglePage: true }}
                        rowSelection={{ selectedRowKeys: importSelected, onChange: (keys) => setImportSelected(keys.map(String)) }}
                        columns={[{ title: "上游模型标识", dataIndex: "0", key: "model" }]}
                        locale={{ emptyText: importBusy ? "正在探测上游目录…" : "上游没有返回可导入的模型。" }}
                    />
                </div>
            </Modal>

            <Modal
                open={joinOpen}
                width={900}
                title="接入厂商"
                okText={pendingVendor ? "重试保存凭据" : "保存并接入"}
                cancelText="取消"
                confirmLoading={joinSaving}
                onOk={() => joinForm.submit()}
                onCancel={() => {
                    setJoinOpen(false);
                    setPendingVendor(null);
                }}
            >
                <div className="flex flex-col gap-3">
                    {joinError ? (
                        <div className="admin-notice is-error">
                            <span>{joinError}</span>
                        </div>
                    ) : null}
                    <div className="grid gap-4 md:grid-cols-[minmax(220px,280px)_minmax(0,1fr)]">
                        <div className="flex flex-col gap-2">
                            {pendingVendor ? (
                                <div className="admin-card admin-card-pad flex flex-col gap-1">
                                    <span className="admin-channel-item-name">{pendingVendor.name}</span>
                                    <span className="admin-channel-item-sub">{pendingVendor.code}</span>
                                    <span className="admin-user-sub">厂商已入库，现在只差凭证。</span>
                                </div>
                            ) : (
                                <>
                                    <Input
                                        allowClear
                                        prefix={<Search className="size-3.5" />}
                                        placeholder="搜索厂商名称或标识"
                                        value={joinKeyword}
                                        onChange={(event) => setJoinKeyword(event.target.value)}
                                    />
                                    <div className="admin-channel-list">
                                        {filteredCatalog.length ? (
                                            filteredCatalog.map((item) => (
                                                <button
                                                    key={item.code}
                                                    type="button"
                                                    className={`admin-channel-item${item.code === joinCode ? " is-active" : ""}`}
                                                    onClick={() => setJoinCode(item.code)}
                                                >
                                                    <span className="admin-channel-item-main">
                                                        <span className="admin-channel-item-name">{item.name}</span>
                                                        <span className="admin-channel-item-sub">
                                                            {item.code}
                                                            {item.capabilities.length ? ` · ${item.capabilities.map((capability) => capabilityLabel(capability)).join("/")}` : ""}
                                                        </span>
                                                    </span>
                                                </button>
                                            ))
                                        ) : (
                                            <div className="admin-empty">目录里没有匹配的厂商。</div>
                                        )}
                                    </div>
                                </>
                            )}
                        </div>
                        <Form form={joinForm} layout="vertical" initialValues={emptyJoin} onFinish={(values) => void submitJoin(values)}>
                            <Form.Item label="凭证名称" name="credentialName" rules={[{ required: true, message: "请填写凭证名称" }]}>
                                <Input placeholder="主线路" />
                            </Form.Item>
                            <Form.Item label="Base URL" name="baseUrl" rules={[{ required: true, message: "请填写 Base URL" }]}>
                                <Input placeholder="https://api.example.com/v1" />
                            </Form.Item>
                            <Form.Item label="API Key" name="apiKey" extra="密钥只写不读：保存后前端只保留尾号用于辨认。">
                                <Input.Password autoComplete="new-password" placeholder="sk-…" />
                            </Form.Item>
                            <Form.Item label="Secret Key" name="secretKey" extra="只有部分厂商需要，没有就留空。">
                                <Input.Password autoComplete="new-password" placeholder="选填" />
                            </Form.Item>
                            <Form.Item label="并发上限" name="concurrencyLimit" extra="缺省由服务端给默认值。">
                                <InputNumber min={1} precision={0} style={{ width: 200 }} />
                            </Form.Item>
                            <Form.Item label="初始模型标识" name="initialModels" extra="每行一个；留空则保存后到模型区「从上游拉取」。">
                                <Input.TextArea rows={4} placeholder={"gpt-4o\ngpt-4o-mini"} />
                            </Form.Item>
                            <Form.Item label="启用" name="enabled" valuePropName="checked">
                                <Switch checkedChildren="启用" unCheckedChildren="停用" />
                            </Form.Item>
                        </Form>
                    </div>
                </div>
            </Modal>
        </div>
    );
}
