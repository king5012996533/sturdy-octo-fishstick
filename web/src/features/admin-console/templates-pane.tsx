import { Button, Form, Input, InputNumber, Modal, Switch, Table, Tag, type TableProps } from "antd";
import { Layers, Pencil, Plus, RefreshCw, Star, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import {
    createAdminCanvasTemplate,
    deleteAdminCanvasTemplate,
    listAdminCanvasTemplates,
    updateAdminCanvasTemplate,
    type CanvasTemplate,
    type CanvasTemplateInput,
    type CanvasTemplateStatus,
} from "./api-templates";

/** 模板标识只允许小写字母、数字与连字符：服务端会再校验一次，这里只是即时提示。 */
const templateCodePattern = /^[a-z0-9-]+$/;

const statusMeta: Record<CanvasTemplateStatus, { label: string; color: string }> = {
    ONLINE: { label: "已上架", color: "green" },
    OFFLINE: { label: "已下架", color: "default" },
};

type TemplateFormValues = {
    code: string;
    name: string;
    category?: string;
    description?: string;
    coverUrl?: string;
    payloadJson?: string;
    online: boolean;
    featured: boolean;
    sortOrder?: number;
};

const emptyTemplate: TemplateFormValues = {
    code: "",
    name: "",
    category: "",
    description: "",
    coverUrl: "",
    payloadJson: "",
    online: true,
    featured: false,
    sortOrder: 0,
};

function valuesOf(template: CanvasTemplate): TemplateFormValues {
    return {
        code: template.code,
        name: template.name,
        category: template.category,
        description: template.description,
        coverUrl: template.coverUrl,
        payloadJson: template.payloadJson,
        online: template.status === "ONLINE",
        featured: template.featured,
        sortOrder: template.sortOrder,
    };
}

function inputOf(values: TemplateFormValues): CanvasTemplateInput {
    return {
        code: values.code.trim(),
        name: values.name.trim(),
        category: (values.category ?? "").trim(),
        description: (values.description ?? "").trim(),
        coverUrl: (values.coverUrl ?? "").trim(),
        // 画布快照原样提交，不做 JSON.parse：后台不该替客户端解释节点结构。
        payloadJson: values.payloadJson ?? "",
        status: values.online ? "ONLINE" : "OFFLINE",
        featured: values.featured,
        sortOrder: Number(values.sortOrder ?? 0),
    };
}

/**
 * 画布模板管理。
 *
 * 模板是平台内容：上下架直接决定用户端目录里能不能看到，所以上下架开关、推荐位与
 * 删除都是显式动作，删除还带二次确认。全局 antd message 在本项目是关闭的，反馈
 * 一律落在页面内的 .admin-notice 上。
 */
export function TemplatesPane() {
    const [templates, setTemplates] = useState<CanvasTemplate[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [editorOpen, setEditorOpen] = useState(false);
    const [editingTemplate, setEditingTemplate] = useState<CanvasTemplate | null>(null);
    const [saving, setSaving] = useState(false);
    const [deleteTarget, setDeleteTarget] = useState<CanvasTemplate | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState("");
    const [form] = Form.useForm<TemplateFormValues>();

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminCanvasTemplates();
            setTemplates(payload.templates ?? []);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载画布模板失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const openEditor = useCallback(
        (template: CanvasTemplate | null) => {
            setEditingTemplate(template);
            setEditorOpen(true);
            setError("");
            setNotice("");
            form.setFieldsValue(template ? valuesOf(template) : emptyTemplate);
        },
        [form],
    );

    const submit = async (values: TemplateFormValues) => {
        setSaving(true);
        setError("");
        setNotice("");
        try {
            if (editingTemplate) {
                await updateAdminCanvasTemplate(editingTemplate.id, inputOf(values));
                setNotice(`模板「${values.name.trim()}」已更新。`);
            } else {
                await createAdminCanvasTemplate(inputOf(values));
                setNotice(`模板「${values.name.trim()}」已创建。`);
            }
            setEditorOpen(false);
            setEditingTemplate(null);
            await load();
        } catch (saveError) {
            setError(saveError instanceof Error ? saveError.message : "保存画布模板失败");
        } finally {
            setSaving(false);
        }
    };

    const confirmDelete = async () => {
        if (!deleteTarget) return;
        setDeleting(true);
        setDeleteError("");
        setError("");
        setNotice("");
        try {
            await deleteAdminCanvasTemplate(deleteTarget.id);
            setNotice(`模板「${deleteTarget.name}」已删除。`);
            setDeleteTarget(null);
            await load();
        } catch (deleteFailure) {
            // 服务端拒绝时把原因留在弹窗里，运营才能当场决定怎么改。
            setDeleteError(deleteFailure instanceof Error ? deleteFailure.message : "删除画布模板失败");
        } finally {
            setDeleting(false);
        }
    };

    const onlineCount = templates.filter((template) => template.status === "ONLINE").length;
    const featuredCount = templates.filter((template) => template.featured).length;
    const categoryCount = new Set(templates.map((template) => template.category || "通用")).size;

    const columns: TableProps<CanvasTemplate>["columns"] = [
        {
            title: "名称",
            key: "name",
            width: 260,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{row.name}</span>
                    <span className="admin-user-sub">{row.description || "—"}</span>
                </span>
            ),
        },
        { title: "标识", dataIndex: "code", key: "code", width: 160, render: (value: string) => <code>{value}</code> },
        { title: "分类", dataIndex: "category", key: "category", width: 120, render: (value: string) => value || "通用" },
        {
            title: "状态",
            dataIndex: "status",
            key: "status",
            width: 100,
            render: (value: CanvasTemplateStatus) => <Tag color={statusMeta[value]?.color ?? "default"}>{statusMeta[value]?.label ?? value}</Tag>,
        },
        {
            title: "推荐",
            dataIndex: "featured",
            key: "featured",
            width: 90,
            render: (featured: boolean) =>
                featured ? (
                    <Tag color="gold" icon={<Star className="size-3" />}>
                        推荐位
                    </Tag>
                ) : (
                    <span className="admin-user-sub">—</span>
                ),
        },
        { title: "排序", dataIndex: "sortOrder", key: "sortOrder", width: 80, render: (value: number) => formatCount(value) },
        { title: "更新时间", dataIndex: "updatedAt", key: "updatedAt", width: 168, render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span> },
        {
            title: "操作",
            key: "actions",
            width: 150,
            render: (_, row) => (
                <div className="admin-settings-inline">
                    <Button size="small" type="text" icon={<Pencil className="size-3.5" />} onClick={() => openEditor(row)}>
                        编辑
                    </Button>
                    <Button
                        size="small"
                        type="text"
                        danger
                        icon={<Trash2 className="size-3.5" />}
                        onClick={() => {
                            setDeleteError("");
                            setDeleteTarget(row);
                        }}
                    >
                        删除
                    </Button>
                </div>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">画布模板</h2>
                    <p className="admin-section-desc">
                        平台统一的画布模板库。上架的模板会出现在用户端模板目录里，推荐位用于前台优先曝光；下架只是撤出目录，模板内容仍保留可随时恢复。
                    </p>
                </div>
                <div className="admin-settings-inline">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button type="primary" icon={<Plus className="size-3.5" />} onClick={() => openEditor(null)}>
                        新建模板
                    </Button>
                </div>
            </div>

            {error ? (
                <div className="admin-notice is-error">
                    <span>{error}</span>
                </div>
            ) : null}
            {notice ? (
                <div className="admin-notice is-ok">
                    <span>{notice}</span>
                </div>
            ) : null}

            <div className="admin-metric-grid">
                <div className="admin-metric">
                    <span className="admin-metric-label">模板总数</span>
                    <span className="admin-metric-value">{formatCount(templates.length)}</span>
                    <span className="admin-metric-hint">含已下架的模板</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">已上架</span>
                    <span className="admin-metric-value">{formatCount(onlineCount)}</span>
                    <span className="admin-metric-hint">用户端目录可见的数量</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">推荐位数量</span>
                    <span className="admin-metric-value">{formatCount(featuredCount)}</span>
                    <span className="admin-metric-hint">前台优先曝光的模板</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">分类数</span>
                    <span className="admin-metric-value">{formatCount(categoryCount)}</span>
                    <span className="admin-metric-hint">运营自定义的分类数量</span>
                </div>
            </div>

            <div className="admin-card">
                <div className="admin-card-head">
                    <span className="flex min-w-0 items-center gap-2">
                        <Layers className="size-4" />
                        <b style={{ fontSize: "var(--fs-body)" }}>全部模板</b>
                    </span>
                    <span className="admin-user-sub">共 {formatCount(templates.length)} 个</span>
                </div>
                <Table<CanvasTemplate>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={templates}
                    columns={columns}
                    scroll={{ x: 1120 }}
                    pagination={false}
                />
            </div>

            <Modal
                open={editorOpen}
                width={720}
                title={editingTemplate ? `编辑模板 · ${editingTemplate.name}` : "新建模板"}
                okText={editingTemplate ? "保存" : "创建"}
                cancelText="取消"
                confirmLoading={saving}
                onOk={() => form.submit()}
                onCancel={() => {
                    setEditorOpen(false);
                    setEditingTemplate(null);
                }}
            >
                <Form form={form} layout="vertical" initialValues={emptyTemplate} onFinish={(values) => void submit(values)} className="admin-form-narrow">
                    <Form.Item label="模板名称" name="name" rules={[{ required: true, message: "请填写模板名称" }, { max: 40, message: "名称不超过 40 个字符" }]}>
                        <Input placeholder="短剧三格分镜" />
                    </Form.Item>
                    <Form.Item
                        label="模板标识"
                        name="code"
                        extra="只允许小写字母、数字与连字符，例如 drama-storyboard。"
                        rules={[{ required: true, message: "请填写模板标识" }, { pattern: templateCodePattern, message: "标识只允许小写字母、数字与连字符" }]}
                    >
                        <Input placeholder="drama-storyboard" />
                    </Form.Item>
                    <Form.Item label="分类" name="category" extra="自由文本，不填默认为「通用」。">
                        <Input placeholder="通用" />
                    </Form.Item>
                    <Form.Item label="说明" name="description">
                        <Input.TextArea rows={2} placeholder="展示在用户端模板卡片上的说明" />
                    </Form.Item>
                    <Form.Item label="封面地址" name="coverUrl">
                        <Input placeholder="https://cdn.example.com/templates/drama.png" />
                    </Form.Item>
                    <Form.Item label="模板内容（画布 JSON）" name="payloadJson" extra="原样保存画布快照，服务端不解析其结构。">
                        <Input.TextArea rows={6} placeholder='{"nodes":[],"connections":[]}' />
                    </Form.Item>
                    <Form.Item label="上架状态" name="online" valuePropName="checked" extra="关闭即下架，用户端目录不再可见。">
                        <Switch checkedChildren="已上架" unCheckedChildren="已下架" />
                    </Form.Item>
                    <Form.Item label="推荐位" name="featured" valuePropName="checked">
                        <Switch checkedChildren="推荐" unCheckedChildren="普通" />
                    </Form.Item>
                    <Form.Item label="排序" name="sortOrder" extra="数字越小越靠前。">
                        <InputNumber min={0} precision={0} style={{ width: 200 }} />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={deleteTarget !== null}
                title="删除模板？"
                okText="确认删除"
                okButtonProps={{ danger: true }}
                cancelText="取消"
                confirmLoading={deleting}
                onOk={() => void confirmDelete()}
                onCancel={() => {
                    setDeleteTarget(null);
                    setDeleteError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.8 }}>
                        即将删除模板「{deleteTarget?.name}」（{deleteTarget?.code}）。删除后不可恢复；
                        如果只是暂时不想让用户看到，请改用「下架」，模板内容会保留。
                    </p>
                    {deleteError ? (
                        <div className="admin-notice is-error">
                            <span>{deleteError}</span>
                        </div>
                    ) : null}
                </div>
            </Modal>
        </div>
    );
}
