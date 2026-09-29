import { App, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Switch, Table, Tag, Tooltip, type TableProps } from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, CloudDownload, Copy, Pencil, Plus, RefreshCw, Save, Trash2, Zap } from "lucide-react";

import { ChannelHeadersEditor, validateChannelHeaders } from "@/components/channel-headers-editor";
import { defaultModelCapabilityConfig } from "@/lib/model-capabilities";
import type { ModelProtocolDefinition } from "@/lib/model-protocols";

import {
    batchDeleteAdminChannelModels,
    createAdminChannel,
    createAdminChannelModel,
    deleteAdminChannel,
    deleteAdminChannelModel,
    duplicateAdminChannel,
    fetchAdminProtocols,
    getAdminChannelOrder,
    importAdminChannelUpstreamModels,
    listAdminChannelModels,
    listAdminChannels,
    previewAdminChannelUpstreamModels,
    saveAdminChannelOrder,
    testAdminChannelModel,
    toChannelModelVariantInputs,
    toProtocolDefinition,
    updateAdminChannel,
    updateAdminChannelModel,
    type AdminChannel,
    type AdminChannelHeader,
    type AdminChannelInput,
    type AdminChannelModel,
    type AdminChannelModelInput,
} from "./api";

const capabilityLabels: Record<string, string> = { text: "文本", image: "图片", video: "视频", audio: "音频" };
const capabilityOptions = Object.entries(capabilityLabels).map(([value, label]) => ({ value, label }));

type ChannelFormValues = {
    name: string;
    publicAlias?: string;
    baseUrl: string;
    apiKey?: string;
    secretKey?: string;
    concurrencyLimit?: number;
    enabled: boolean;
    headers?: AdminChannelHeader[];
    initialModels?: string;
};

type ModelFormValues = {
    modelKey: string;
    providerModelKey?: string;
    displayName?: string;
    capability: string;
    protocol: string;
    enabled: boolean;
};

function channelHost(baseUrl: string) {
    try {
        return new URL(baseUrl).host;
    } catch {
        return baseUrl;
    }
}

function errorMessage(error: unknown, fallback: string) {
    return error instanceof Error && error.message ? error.message : fallback;
}

/**
 * 渠道与模型。
 *
 * 列表顺序即路由顺序（serve 时按 sortOrder 选渠道），因此上移/下移直接落库而不是
 * 只改前端顺序：管理员看到的就是真实生效的优先级。
 */
export function ChannelsPane() {
    const { message } = App.useApp();
    const [channels, setChannels] = useState<AdminChannel[]>([]);
    const [channelOrder, setChannelOrder] = useState<string[]>([]);
    const [channelsLoading, setChannelsLoading] = useState(true);
    const [activeChannelId, setActiveChannelId] = useState("");
    const [models, setModels] = useState<AdminChannelModel[]>([]);
    const [modelsLoading, setModelsLoading] = useState(false);
    const [selectedModelIds, setSelectedModelIds] = useState<string[]>([]);
    const [protocols, setProtocols] = useState<ModelProtocolDefinition[]>([]);

    const [channelEditor, setChannelEditor] = useState<{ mode: "create" | "edit"; channel?: AdminChannel } | null>(null);
    const [channelSaving, setChannelSaving] = useState(false);
    const [modelEditor, setModelEditor] = useState<{ model?: AdminChannelModel } | null>(null);
    const [modelSaving, setModelSaving] = useState(false);
    const [modelTesting, setModelTesting] = useState(false);
    const [upstreamOpen, setUpstreamOpen] = useState(false);
    const [upstreamModels, setUpstreamModels] = useState<string[]>([]);
    const [upstreamSelected, setUpstreamSelected] = useState<string[]>([]);
    const [upstreamBusy, setUpstreamBusy] = useState(false);

    const [channelForm] = Form.useForm<ChannelFormValues>();
    const [modelForm] = Form.useForm<ModelFormValues>();
    const modelCapability = Form.useWatch("capability", modelForm);

    const activeChannel = useMemo(() => channels.find((channel) => channel.id === activeChannelId) ?? null, [channels, activeChannelId]);

    const loadChannels = useCallback(
        async (preferredId?: string) => {
            setChannelsLoading(true);
            try {
                const [page, order] = await Promise.all([listAdminChannels({ pageSize: 200 }), getAdminChannelOrder()]);
                setChannels(page.channels ?? []);
                setChannelOrder((order.items ?? []).map((item) => item.id));
                setActiveChannelId((current) => {
                    const wanted = preferredId ?? current;
                    if (wanted && (page.channels ?? []).some((channel) => channel.id === wanted)) return wanted;
                    return page.channels?.[0]?.id ?? "";
                });
            } catch (error) {
                message.error(errorMessage(error, "加载渠道失败"));
            } finally {
                setChannelsLoading(false);
            }
        },
        [message],
    );

    const loadModels = useCallback(
        async (channelId: string) => {
            if (!channelId) {
                setModels([]);
                return;
            }
            setModelsLoading(true);
            try {
                const payload = await listAdminChannelModels(channelId);
                setModels(payload.models ?? []);
                setSelectedModelIds([]);
            } catch (error) {
                message.error(errorMessage(error, "加载渠道模型失败"));
            } finally {
                setModelsLoading(false);
            }
        },
        [message],
    );

    useEffect(() => {
        void loadChannels();
    }, [loadChannels]);

    useEffect(() => {
        if (!activeChannelId) return;
        void loadModels(activeChannelId);
    }, [activeChannelId, loadModels]);

    useEffect(() => {
        // 协议目录由服务端下发：它是保存时校验用的同一份注册表，前端不再自己维护清单。
        void fetchAdminProtocols()
            .then((payload) => setProtocols((payload.providers ?? []).map(toProtocolDefinition).filter((item) => item.enabled)))
            .catch(() => setProtocols([]));
    }, []);

    const protocolsForCapability = useMemo(() => {
        if (!modelCapability) return protocols;
        const matched = protocols.filter((item) => item.capability === modelCapability);
        return matched.length ? matched : protocols;
    }, [protocols, modelCapability]);

    useEffect(() => {
        // 新建模型时能力与协议必须自洽：协议目录是异步到达的（也可能晚于弹窗打开），
        // 初始值很容易落成"文本能力 + 视频协议"，那种组合要到保存时才被服务端拒绝。
        // 编辑已有模型不动它的协议，避免把管理员正在改的那一项悄悄换掉。
        if (!modelEditor || modelEditor.model) return;
        const current = modelForm.getFieldValue("protocol");
        if (current && protocolsForCapability.some((item) => item.value === current)) return;
        const fallback = protocolsForCapability[0]?.value;
        if (fallback) modelForm.setFieldValue("protocol", fallback);
    }, [modelEditor, modelForm, protocolsForCapability]);

    const moveChannel = async (channelId: string, delta: number) => {
        const ids = channelOrder.length ? [...channelOrder] : channels.map((channel) => channel.id);
        const index = ids.indexOf(channelId);
        const target = index + delta;
        if (index < 0 || target < 0 || target >= ids.length) return;
        [ids[index], ids[target]] = [ids[target], ids[index]];
        try {
            await saveAdminChannelOrder(ids, channelOrder);
            await loadChannels(channelId);
        } catch (error) {
            message.error(errorMessage(error, "调整渠道顺序失败，请刷新后重试"));
        }
    };

    const submitChannel = async (values: ChannelFormValues) => {
        const headerError = validateChannelHeaders(values.headers);
        if (headerError) {
            message.error(headerError);
            return;
        }
        const initialModels = (values.initialModels ?? "")
            .split(/[\n,]/)
            .map((item) => item.trim())
            .filter(Boolean);
        const payload: AdminChannelInput = {
            name: values.name.trim(),
            publicAlias: values.publicAlias?.trim() || undefined,
            baseUrl: values.baseUrl.trim(),
            // 留空表示"不修改密钥"：管理端不回显密钥原文，空字符串会被服务端当成清空。
            apiKey: values.apiKey?.trim() ? values.apiKey.trim() : undefined,
            secretKey: values.secretKey?.trim() ? values.secretKey.trim() : undefined,
            concurrencyLimit: values.concurrencyLimit,
            headers: values.headers ?? [],
            enabled: values.enabled,
            models: initialModels,
        };
        setChannelSaving(true);
        try {
            const channel = channelEditor?.mode === "edit" && channelEditor.channel ? await updateAdminChannel(channelEditor.channel.id, payload) : await createAdminChannel(payload);
            message.success(channelEditor?.mode === "edit" ? "渠道已更新" : "渠道已创建");
            setChannelEditor(null);
            await loadChannels(channel.id);
        } catch (error) {
            message.error(errorMessage(error, "保存渠道失败"));
        } finally {
            setChannelSaving(false);
        }
    };

    const submitModel = async (values: ModelFormValues, runTest = false) => {
        if (!activeChannel) return;
        const existing = modelEditor?.model;
        const capabilityChanged = existing ? existing.capability !== values.capability || existing.protocol !== values.protocol : true;
        const payload: AdminChannelModelInput = {
            modelKey: values.modelKey.trim(),
            providerModelKey: values.providerModelKey?.trim() || undefined,
            displayName: values.displayName?.trim() || undefined,
            capability: values.capability,
            protocol: values.protocol,
            enabled: values.enabled,
            // 能力/协议没变时保留原有能力配置（可能被上游目录拉取改过），变了就必须重建，
            // 否则会把旧协议的上限（例如尺寸档位）带进新协议。
            capabilityConfig: existing && !capabilityChanged ? existing.capabilityConfig : defaultModelCapabilityConfig(values.protocol, values.modelKey.trim()),
            // 档位同理，而且更要紧：它是"这个模型卖哪几档"的定义，漏传会被服务端重置成
            // 一条默认记录。能力/协议变了才交给服务端重建。
            variants: existing && !capabilityChanged ? toChannelModelVariantInputs(existing) : undefined,
        };
        setModelSaving(!runTest);
        setModelTesting(runTest);
        try {
            if (runTest) {
                const result = await testAdminChannelModel(activeChannel.id, payload);
                message.success(`连通正常，用时 ${result.durationMs} ms`);
                return;
            }
            if (existing) await updateAdminChannelModel(activeChannel.id, existing.id, payload);
            else await createAdminChannelModel(activeChannel.id, payload);
            message.success(existing ? "模型已更新" : "模型已创建");
            setModelEditor(null);
            await Promise.all([loadModels(activeChannel.id), loadChannels(activeChannel.id)]);
        } catch (error) {
            message.error(errorMessage(error, runTest ? "连通性测试失败" : "保存模型失败"));
        } finally {
            setModelSaving(false);
            setModelTesting(false);
        }
    };

    const openUpstreamPreview = async () => {
        if (!activeChannel) return;
        setUpstreamOpen(true);
        setUpstreamBusy(true);
        setUpstreamModels([]);
        setUpstreamSelected([]);
        try {
            const payload = await previewAdminChannelUpstreamModels(activeChannel.id);
            const existing = new Set(models.map((model) => model.providerModelKey || model.modelKey));
            setUpstreamModels(payload.models ?? []);
            // 只预选尚未入库的上游模型：已存在的重复导入没有意义。
            setUpstreamSelected((payload.models ?? []).filter((name) => !existing.has(name)));
        } catch (error) {
            message.error(errorMessage(error, "拉取上游模型目录失败"));
        } finally {
            setUpstreamBusy(false);
        }
    };

    const importUpstream = async () => {
        if (!activeChannel || !upstreamSelected.length) return;
        setUpstreamBusy(true);
        try {
            const result = await importAdminChannelUpstreamModels(activeChannel.id, upstreamSelected);
            message.success(result.added > 0 ? `已导入 ${result.added} 个模型` : "所选模型都已存在，没有新增");
            setUpstreamOpen(false);
            await Promise.all([loadModels(activeChannel.id), loadChannels(activeChannel.id)]);
        } catch (error) {
            message.error(errorMessage(error, "导入模型失败"));
        } finally {
            setUpstreamBusy(false);
        }
    };

    const removeChannels = async (channel: AdminChannel) => {
        try {
            await deleteAdminChannel(channel.id);
            message.success("渠道已删除");
            await loadChannels();
        } catch (error) {
            message.error(errorMessage(error, "删除渠道失败"));
        }
    };

    const duplicateChannel = async (channel: AdminChannel) => {
        try {
            const copy = await duplicateAdminChannel(channel.id);
            message.success("已复制渠道");
            await loadChannels(copy.id);
        } catch (error) {
            message.error(errorMessage(error, "复制渠道失败"));
        }
    };

    const removeModels = async (ids: string[]) => {
        if (!activeChannel || !ids.length) return;
        try {
            if (ids.length === 1) await deleteAdminChannelModel(activeChannel.id, ids[0]);
            else await batchDeleteAdminChannelModels(activeChannel.id, ids);
            message.success(`已删除 ${ids.length} 个模型`);
            await Promise.all([loadModels(activeChannel.id), loadChannels(activeChannel.id)]);
        } catch (error) {
            message.error(errorMessage(error, "删除模型失败"));
        }
    };

    const testExistingModel = async (model: AdminChannelModel) => {
        if (!activeChannel) return;
        try {
            const result = await testAdminChannelModel(activeChannel.id, {
                modelKey: model.modelKey,
                providerModelKey: model.providerModelKey,
                capability: model.capability,
                protocol: model.protocol,
                enabled: model.enabled,
                capabilityConfig: model.capabilityConfig,
            });
            message.success(`${model.modelKey} 连通正常，用时 ${result.durationMs} ms`);
        } catch (error) {
            message.error(errorMessage(error, `${model.modelKey} 连通性测试失败`));
        }
    };

    // 弹窗带 destroyOnHidden：每次打开都是新的表单实例，必须在打开时显式回填，
    // 否则编辑已存在的模型会拿到空表单，连必填的平台标识都过不了校验。
    const openModelEditor = (model?: AdminChannelModel) => {
        setModelEditor(model ? { model } : {});
        modelForm.setFieldsValue(model ? {
            modelKey: model.modelKey,
            providerModelKey: model.providerModelKey ?? "",
            displayName: model.displayName ?? "",
            capability: model.capability,
            protocol: model.protocol,
            enabled: model.enabled,
        } : {
            modelKey: "",
            providerModelKey: "",
            displayName: "",
            capability: "text",
            // 目录按 ID 排序，第一个往往不是文本协议；这里直接按能力取。
            protocol: protocols.find((item) => item.capability === "text")?.value ?? protocols[0]?.value ?? "chat-completion",
            enabled: true,
        });
    };

    const modelColumns: TableProps<AdminChannelModel>["columns"] = [
        {
            title: "平台标识",
            dataIndex: "modelKey",
            render: (_value, record) => (
                <span className="flex min-w-0 flex-col">
                    <b className="admin-console-mono" style={{ fontSize: "var(--fs-caption)", letterSpacing: 0, textTransform: "none", color: "inherit" }}>
                        {record.modelKey}
                    </b>
                    {record.displayName && record.displayName !== record.modelKey ? <span style={{ color: "var(--admin-ink-faint)", fontSize: "var(--fs-tiny)" }}>{record.displayName}</span> : null}
                </span>
            ),
        },
        { title: "上游标识", dataIndex: "providerModelKey", render: (value: string, record) => <span className="admin-console-mono" style={{ letterSpacing: 0, textTransform: "none" }}>{value || record.modelKey}</span> },
        { title: "能力", dataIndex: "capability", width: 90, render: (value: string) => <Tag variant="filled">{capabilityLabels[value] ?? value}</Tag> },
        { title: "协议", dataIndex: "protocol", width: 168, render: (value: string) => <span className="admin-console-mono" style={{ letterSpacing: 0, textTransform: "none" }}>{value}</span> },
        {
            title: "状态",
            dataIndex: "enabled",
            width: 88,
            render: (value: boolean) => (
                <span className="flex items-center gap-2" style={{ fontSize: "var(--fs-caption)" }}>
                    <i className={`admin-dot ${value ? "is-on" : "is-off"}`} aria-hidden />
                    {value ? "启用" : "停用"}
                </span>
            ),
        },
        {
            title: "操作",
            key: "actions",
            width: 176,
            render: (_value, record) => (
                <span className="flex items-center gap-1">
                    <Tooltip title="连通性测试">
                        <Button size="small" type="text" icon={<Zap className="size-3.5" />} aria-label={`测试 ${record.modelKey}`} onClick={() => void testExistingModel(record)} />
                    </Tooltip>
                    <Button size="small" type="text" icon={<Pencil className="size-3.5" />} onClick={() => openModelEditor(record)}>
                        编辑
                    </Button>
                    <Popconfirm title={`删除模型 ${record.modelKey}？`} description="正在被前台模型或进行中任务使用的模型无法删除。" onConfirm={() => void removeModels([record.id])}>
                        <Button size="small" type="text" danger icon={<Trash2 className="size-3.5" />} aria-label={`删除 ${record.modelKey}`} />
                    </Popconfirm>
                </span>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">渠道与模型</h2>
                    <p className="admin-section-desc">平台持有的上游地址与密钥在这里配置；前台只看到模型目录，看不到真实地址与凭证。列表顺序即路由优先级。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button icon={<RefreshCw className="size-3.5" />} onClick={() => void loadChannels(activeChannelId)} loading={channelsLoading}>
                        刷新
                    </Button>
                    <Button
                        type="primary"
                        icon={<Plus className="size-3.5" />}
                        onClick={() => {
                            setChannelEditor({ mode: "create" });
                            channelForm.setFieldsValue({ name: "", publicAlias: "", baseUrl: "", apiKey: "", secretKey: "", concurrencyLimit: 3, enabled: true, headers: [], initialModels: "" });
                        }}
                    >
                        新建渠道
                    </Button>
                </div>
            </div>

            <div className="admin-channel-layout">
                <div className="admin-card admin-channel-rail">
                    <div className="admin-card-head">
                        <span className="admin-console-mono">channels · {channels.length}</span>
                    </div>
                    <div className="admin-card-pad">
                    {channelsLoading && !channels.length ? (
                        <div className="admin-empty">正在加载渠道…</div>
                    ) : channels.length ? (
                        <div className="admin-channel-list">
                            {channels.map((channel, index) => (
                                <div key={channel.id} className={`admin-channel-item${channel.id === activeChannelId ? " is-active" : ""}`}>
                                    <i className={`admin-dot ${channel.enabled ? "is-on" : "is-off"}`} aria-hidden />
                                    <button type="button" className="admin-channel-item-main" onClick={() => setActiveChannelId(channel.id)}>
                                        <span className="admin-channel-item-name">{channel.name}</span>
                                        <span className="admin-channel-item-sub">{channelHost(channel.baseUrl)} · {channel.models.length} 模型</span>
                                    </button>
                                    <span className="admin-channel-item-actions">
                                        <Button size="small" type="text" disabled={index === 0} icon={<ArrowUp className="size-3.5" />} aria-label="上移" onClick={() => void moveChannel(channel.id, -1)} />
                                        <Button size="small" type="text" disabled={index === channels.length - 1} icon={<ArrowDown className="size-3.5" />} aria-label="下移" onClick={() => void moveChannel(channel.id, 1)} />
                                    </span>
                                </div>
                            ))}
                        </div>
                    ) : (
                        <div className="admin-empty">还没有渠道。先建一个渠道，再从上游拉取模型。</div>
                    )}
                    </div>
                </div>

                {activeChannel ? (
                    <div className="flex min-w-0 flex-col gap-3">
                        <div className="admin-card">
                            <div className="admin-card-head">
                                <span className="flex min-w-0 items-center gap-2">
                                    <i className={`admin-dot ${activeChannel.enabled ? "is-on" : "is-off"}`} aria-hidden />
                                    <b style={{ fontSize: "var(--fs-body-lg)" }}>{activeChannel.name}</b>
                                    {activeChannel.publicAlias ? <span className="admin-console-mono">alias · {activeChannel.publicAlias}</span> : null}
                                </span>
                                <span className="flex items-center gap-2">
                                    <Button
                                        size="small"
                                        icon={<Pencil className="size-3.5" />}
                                        onClick={() => {
                                            setChannelEditor({ mode: "edit", channel: activeChannel });
                                            channelForm.setFieldsValue({
                                                name: activeChannel.name,
                                                publicAlias: activeChannel.publicAlias ?? "",
                                                baseUrl: activeChannel.baseUrl,
                                                apiKey: "",
                                                secretKey: "",
                                                concurrencyLimit: activeChannel.concurrencyLimit,
                                                enabled: activeChannel.enabled,
                                                headers: activeChannel.headers ?? [],
                                                initialModels: "",
                                            });
                                        }}
                                    >
                                        编辑
                                    </Button>
                                    <Tooltip title="复制为新渠道">
                                        <Button size="small" icon={<Copy className="size-3.5" />} onClick={() => void duplicateChannel(activeChannel)} />
                                    </Tooltip>
                                    <Popconfirm title={`删除渠道 ${activeChannel.name}？`} description="渠道下的模型会一并下线。" onConfirm={() => void removeChannels(activeChannel)}>
                                        <Button size="small" danger icon={<Trash2 className="size-3.5" />} />
                                    </Popconfirm>
                                </span>
                            </div>
                            <div className="admin-card-pad">
                                <div className="admin-meta-grid">
                                    <div className="admin-kv">
                                        <span className="admin-kv-label">base url</span>
                                        <span className="admin-kv-value is-mono" title={activeChannel.baseUrl}>{activeChannel.baseUrl}</span>
                                    </div>
                                    <div className="admin-kv">
                                        <span className="admin-kv-label">api key</span>
                                        <span className="admin-kv-value is-mono">{activeChannel.hasApiKey ? "已配置" : "未配置"}</span>
                                    </div>
                                    <div className="admin-kv">
                                        <span className="admin-kv-label">concurrency</span>
                                        <span className="admin-kv-value">{activeChannel.concurrencyLimit}</span>
                                    </div>
                                    <div className="admin-kv">
                                        <span className="admin-kv-label">updated</span>
                                        <span className="admin-kv-value">{new Date(activeChannel.updatedAt).toLocaleString("zh-CN")}</span>
                                    </div>
                                </div>
                            </div>
                        </div>

                        <div className="admin-card">
                            <div className="admin-card-head">
                                <span className="admin-console-mono">models · {models.length}</span>
                                <span className="flex items-center gap-2">
                                    {selectedModelIds.length ? (
                                        <Popconfirm title={`删除所选 ${selectedModelIds.length} 个模型？`} onConfirm={() => void removeModels(selectedModelIds)}>
                                            <Button size="small" danger icon={<Trash2 className="size-3.5" />}>
                                                删除所选
                                            </Button>
                                        </Popconfirm>
                                    ) : null}
                                    <Button size="small" icon={<CloudDownload className="size-3.5" />} onClick={() => void openUpstreamPreview()}>
                                        从上游拉取
                                    </Button>
                                    <Button
                                        size="small"
                                        type="primary"
                                        icon={<Plus className="size-3.5" />}
                                        onClick={() => openModelEditor()}
                                    >
                                        新增模型
                                    </Button>
                                </span>
                            </div>
                            <Table<AdminChannelModel>
                                rowKey="id"
                                size="small"
                                columns={modelColumns}
                                dataSource={models}
                                loading={modelsLoading}
                                pagination={false}
                                rowSelection={{ selectedRowKeys: selectedModelIds, onChange: (keys) => setSelectedModelIds(keys.map(String)) }}
                                locale={{ emptyText: "该渠道还没有模型，可以从上游拉取后导入。" }}
                            />
                        </div>

                        <div className="admin-inline-note">
                            <span>转发端点：<code className="admin-console-mono" style={{ letterSpacing: 0, textTransform: "none" }}>/api/ai/system/{activeChannel.id}</code> —— 前台用它调用本渠道，凭证由服务端注入，浏览器不会拿到真实密钥。</span>
                        </div>
                    </div>
                ) : (
                    <div className="admin-card admin-empty">选择左侧渠道查看模型配置。</div>
                )}
            </div>

            <Modal
                open={Boolean(channelEditor)}
                title={channelEditor?.mode === "edit" ? "编辑渠道" : "新建渠道"}
                onCancel={() => setChannelEditor(null)}
                onOk={() => channelForm.submit()}
                confirmLoading={channelSaving}
                okText="保存"
                destroyOnHidden
                width={640}
            >
                <Form form={channelForm} layout="vertical" onFinish={(values) => void submitChannel(values)} className="admin-form-narrow">
                    <Form.Item name="name" label="渠道名称" rules={[{ required: true, message: "请填写渠道名称" }]}>
                        <Input placeholder="例如：KinoTV 主上游" />
                    </Form.Item>
                    <Form.Item name="publicAlias" label="公开别名" extra="前台展示用；留空则用渠道名称。">
                        <Input placeholder="可选" />
                    </Form.Item>
                    <Form.Item name="baseUrl" label="Base URL" rules={[{ required: true, message: "请填写 Base URL" }]}>
                        <Input placeholder="https://api.example.com/v1" />
                    </Form.Item>
                    <div className="grid grid-cols-1 gap-x-4 sm:grid-cols-2">
                        <Form.Item name="apiKey" label="API Key" extra={channelEditor?.mode === "edit" ? "留空表示不修改。" : undefined}>
                            <Input.Password placeholder={channelEditor?.channel?.hasApiKey ? "已配置，留空保持不变" : "sk-..."} autoComplete="new-password" />
                        </Form.Item>
                        <Form.Item name="secretKey" label="Secret Key" extra={channelEditor?.mode === "edit" ? "留空表示不修改。" : undefined}>
                            <Input.Password placeholder={channelEditor?.channel?.hasSecretKey ? "已配置，留空保持不变" : "可选，即梦等协议需要"} autoComplete="new-password" />
                        </Form.Item>
                    </div>
                    <div className="grid grid-cols-1 gap-x-4 sm:grid-cols-2">
                        <Form.Item name="concurrencyLimit" label="渠道并发上限">
                            <InputNumber min={1} max={999} className="w-full" />
                        </Form.Item>
                        <Form.Item name="enabled" label="启用" valuePropName="checked">
                            <Switch />
                        </Form.Item>
                    </div>
                    {channelEditor?.mode === "create" ? (
                        <Form.Item name="initialModels" label="初始模型标识" extra="每行一个平台模型标识；也可以建好渠道后从上游目录拉取。">
                            <Input.TextArea rows={3} placeholder={"kino-chat\nkino-image-1"} />
                        </Form.Item>
                    ) : null}
                    <Form.Item name="headers" label="自定义请求头">
                        <ChannelHeadersEditor />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={Boolean(modelEditor)}
                title={modelEditor?.model ? `编辑模型 · ${modelEditor.model.modelKey}` : "新增模型"}
                onCancel={() => setModelEditor(null)}
                destroyOnHidden
                width={620}
                footer={
                    <span className="flex items-center justify-end gap-2">
                        <Button
                            icon={<Zap className="size-3.5" />}
                            loading={modelTesting}
                            onClick={() => {
                                void modelForm.validateFields().then((values) => submitModel(values, true));
                            }}
                        >
                            测试连通
                        </Button>
                        <Button type="primary" icon={<Save className="size-3.5" />} loading={modelSaving} onClick={() => modelForm.submit()}>
                            保存
                        </Button>
                    </span>
                }
            >
                <Form form={modelForm} layout="vertical" onFinish={(values) => void submitModel(values)} className="admin-form-narrow">
                    <Form.Item name="modelKey" label="平台模型标识" rules={[{ required: true, message: "请填写平台模型标识" }]} extra="前台传来的 model 字段就是它；改名等于换了一个对外 SKU。">
                        <Input placeholder="kino-chat" />
                    </Form.Item>
                    <Form.Item name="providerModelKey" label="上游模型标识" extra="发给上游的真实 SKU；留空则与平台标识相同。">
                        <Input placeholder="gpt-4o-mini" />
                    </Form.Item>
                    <Form.Item name="displayName" label="展示名" extra="前台模型列表里的名字；留空用平台标识。">
                        <Input placeholder="可选" />
                    </Form.Item>
                    <div className="grid grid-cols-1 gap-x-4 sm:grid-cols-2">
                        <Form.Item name="capability" label="模型能力" rules={[{ required: true }]}>
                            <Select options={capabilityOptions} onChange={(value) => {
                                const matched = protocols.filter((item) => item.capability === value);
                                // 能力切换后原协议必然不再适用：直接落到该能力的第一个协议，避免保存时报"能力与协议不匹配"。
                                if (matched.length && !matched.some((item) => item.value === modelForm.getFieldValue("protocol"))) {
                                    modelForm.setFieldValue("protocol", matched[0].value);
                                }
                            }} />
                        </Form.Item>
                        <Form.Item name="protocol" label="请求协议" rules={[{ required: true, message: "请选择请求协议" }]} extra="由服务端下发，与保存校验使用同一份注册表。">
                            <Select showSearch optionFilterProp="label" options={protocolsForCapability.map((item) => ({ value: item.value, label: `${item.label} · ${item.value}` }))} />
                        </Form.Item>
                    </div>
                    <Form.Item name="enabled" label="启用" valuePropName="checked">
                        <Switch />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={upstreamOpen}
                title="从上游拉取模型"
                onCancel={() => setUpstreamOpen(false)}
                onOk={() => void importUpstream()}
                okText={`导入所选（${upstreamSelected.length}）`}
                okButtonProps={{ disabled: !upstreamSelected.length }}
                confirmLoading={upstreamBusy}
                width={680}
            >
                <Table<string>
                    rowKey={(id) => id}
                    size="small"
                    loading={upstreamBusy}
                    dataSource={upstreamModels}
                    pagination={{ pageSize: 10, size: "small", hideOnSinglePage: true }}
                    rowSelection={{ selectedRowKeys: upstreamSelected, onChange: (keys) => setUpstreamSelected(keys.map(String)) }}
                    columns={[{ title: "上游模型标识", dataIndex: "0", key: "model" }]}
                    locale={{ emptyText: upstreamBusy ? "正在拉取上游目录…" : "上游没有返回可导入的模型。" }}
                />
            </Modal>
        </div>
    );
}
