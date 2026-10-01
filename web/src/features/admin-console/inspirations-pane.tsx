import { Button, Form, Input, InputNumber, Modal, Select, Switch, Table, Tag, type TableProps } from "antd";
import { Pencil, Plus, RefreshCw, Sparkles, Star, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import {
    createAdminCreationInspiration,
    deleteAdminCreationInspiration,
    listAdminCreationInspirations,
    updateAdminCreationInspiration,
    type AdminCreationInspiration,
    type AdminCreationInspirationInput,
    type CreationInspirationMode,
    type CreationInspirationStatus,
} from "./api-inspirations";

const statusMeta: Record<CreationInspirationStatus, { label: string; color: string }> = {
    ONLINE: { label: "已上架", color: "green" },
    OFFLINE: { label: "已下架", color: "default" },
};

const modeMeta: Record<CreationInspirationMode, { label: string; color: string }> = {
    video: { label: "视频", color: "geekblue" },
    image: { label: "图片", color: "magenta" },
    text: { label: "文本", color: "cyan" },
};

const modeOptions = (Object.keys(modeMeta) as CreationInspirationMode[]).map((value) => ({ value, label: modeMeta[value].label }));

/**
 * 署名口径与前台保持一致：带原始链接的是外部示例素材，带来源标注的是开源改编，
 * 两者都没有才是本平台原创。后台把它显式列出来，运营才看得见哪几条是"别人的作品"。
 */
function creditOf(inspiration: AdminCreationInspiration) {
    if (inspiration.sourceUrl) return inspiration.author ? `示例素材 · ${inspiration.author}` : "示例素材";
    if (inspiration.source) return "开源改编 · CC0";
    return inspiration.author ? `原创 · ${inspiration.author}` : "原创提示词";
}

type InspirationFormValues = {
    title: string;
    mode: CreationInspirationMode;
    category?: string;
    duration?: string;
    tags?: string[];
    description?: string;
    coverUrl?: string;
    prompt?: string;
    author?: string;
    likes?: number;
    sourceUrl?: string;
    source?: string;
    online: boolean;
    featured: boolean;
    sortOrder?: number;
};

const emptyInspiration: InspirationFormValues = {
    title: "",
    mode: "video",
    category: "",
    duration: "",
    tags: [],
    description: "",
    coverUrl: "",
    prompt: "",
    author: "",
    likes: 0,
    sourceUrl: "",
    source: "",
    online: true,
    featured: false,
    sortOrder: 0,
};

function valuesOf(inspiration: AdminCreationInspiration): InspirationFormValues {
    return {
        title: inspiration.title,
        mode: (modeMeta[inspiration.mode as CreationInspirationMode] ? (inspiration.mode as CreationInspirationMode) : "video"),
        category: inspiration.category,
        duration: inspiration.duration,
        tags: inspiration.tags ?? [],
        description: inspiration.description,
        coverUrl: inspiration.coverUrl,
        prompt: inspiration.prompt,
        author: inspiration.author,
        likes: inspiration.likes,
        sourceUrl: inspiration.sourceUrl,
        source: inspiration.source,
        online: inspiration.status === "ONLINE",
        featured: inspiration.featured,
        sortOrder: inspiration.sortOrder,
    };
}

function inputOf(values: InspirationFormValues): AdminCreationInspirationInput {
    return {
        title: values.title.trim(),
        description: (values.description ?? "").trim(),
        coverUrl: (values.coverUrl ?? "").trim(),
        // 提示词原样提交：它可能带 {{变量}} 之类的生成侧语法，后台不该替模型解释。
        prompt: (values.prompt ?? "").trim(),
        mode: values.mode,
        category: (values.category ?? "").trim(),
        duration: (values.duration ?? "").trim(),
        tags: (values.tags ?? []).map((tag) => tag.trim()).filter(Boolean),
        author: (values.author ?? "").trim(),
        likes: Number(values.likes ?? 0),
        sourceUrl: (values.sourceUrl ?? "").trim(),
        source: (values.source ?? "").trim(),
        status: values.online ? "ONLINE" : "OFFLINE",
        featured: values.featured,
        sortOrder: Number(values.sortOrder ?? 0),
    };
}

/**
 * 精选灵感广场管理。
 *
 * 广场是首页的主要内容：上下架直接决定用户端看不看得到这条灵感，所以上下架开关、
 * 推荐位与删除都是显式动作，删除还带二次确认。反馈一律落在页面内的 .admin-notice 上。
 */
export function InspirationsPane() {
    const [inspirations, setInspirations] = useState<AdminCreationInspiration[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [editorOpen, setEditorOpen] = useState(false);
    const [editingInspiration, setEditingInspiration] = useState<AdminCreationInspiration | null>(null);
    const [saving, setSaving] = useState(false);
    const [deleteTarget, setDeleteTarget] = useState<AdminCreationInspiration | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState("");
    const [form] = Form.useForm<InspirationFormValues>();

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminCreationInspirations();
            setInspirations(payload.inspirations ?? []);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载精选灵感失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const openEditor = useCallback(
        (inspiration: AdminCreationInspiration | null) => {
            setEditingInspiration(inspiration);
            setEditorOpen(true);
            setError("");
            setNotice("");
            form.setFieldsValue(inspiration ? valuesOf(inspiration) : emptyInspiration);
        },
        [form],
    );

    const submit = async (values: InspirationFormValues) => {
        setSaving(true);
        setError("");
        setNotice("");
        try {
            if (editingInspiration) {
                await updateAdminCreationInspiration(editingInspiration.id, inputOf(values));
                setNotice(`灵感「${values.title.trim()}」已更新。`);
            } else {
                await createAdminCreationInspiration(inputOf(values));
                setNotice(`灵感「${values.title.trim()}」已创建。`);
            }
            setEditorOpen(false);
            setEditingInspiration(null);
            await load();
        } catch (saveError) {
            setError(saveError instanceof Error ? saveError.message : "保存精选灵感失败");
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
            await deleteAdminCreationInspiration(deleteTarget.id);
            setNotice(`灵感「${deleteTarget.title}」已删除。`);
            setDeleteTarget(null);
            await load();
        } catch (deleteFailure) {
            // 服务端拒绝时把原因留在弹窗里，运营才能当场决定怎么改。
            setDeleteError(deleteFailure instanceof Error ? deleteFailure.message : "删除精选灵感失败");
        } finally {
            setDeleting(false);
        }
    };

    const onlineCount = inspirations.filter((inspiration) => inspiration.status === "ONLINE").length;
    const featuredCount = inspirations.filter((inspiration) => inspiration.featured).length;
    const sampleCount = inspirations.filter((inspiration) => inspiration.sourceUrl || inspiration.source).length;

    const columns: TableProps<AdminCreationInspiration>["columns"] = [
        {
            title: "标题",
            key: "title",
            width: 240,
            render: (_, row) => (
                <span className="admin-user-cell admin-inspiration-cell">
                    <span className="admin-user-name">{row.title}</span>
                    <span className="admin-user-sub admin-inspiration-desc">{row.description || "—"}</span>
                </span>
            ),
        },
        {
            title: "模式",
            dataIndex: "mode",
            key: "mode",
            width: 80,
            render: (value: string) => {
                const meta = modeMeta[value as CreationInspirationMode];
                return meta ? <Tag color={meta.color}>{meta.label}</Tag> : <Tag>{value}</Tag>;
            },
        },
        {
            title: "分类 / 时长",
            key: "category",
            width: 140,
            render: (_, row) => (
                <span className="admin-user-cell admin-inspiration-cell">
                    <span className="admin-user-name">{row.category || "精选"}</span>
                    <span className="admin-user-sub">{row.duration || "—"}</span>
                </span>
            ),
        },
        { title: "署名", key: "credit", width: 150, render: (_, row) => <span className="admin-user-sub">{creditOf(row)}</span> },
        {
            title: "状态",
            dataIndex: "status",
            key: "status",
            width: 90,
            render: (value: CreationInspirationStatus) => <Tag color={statusMeta[value]?.color ?? "default"}>{statusMeta[value]?.label ?? value}</Tag>,
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
        { title: "排序", dataIndex: "sortOrder", key: "sortOrder", width: 76, render: (value: number) => formatCount(value) },
        { title: "更新时间", dataIndex: "updatedAt", key: "updatedAt", width: 160, render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span> },
        {
            title: "操作",
            key: "actions",
            width: 140,
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
                    <h2 className="admin-section-title">精选灵感广场</h2>
                    <p className="admin-section-desc">
                        首页精选灵感的运营入口，既有运营自己录入的条目，也有审核通过的用户投稿。上架的条目会出现在用户端广场里，推荐位用于前台优先曝光；下架只是撤出广场，内容仍保留可随时恢复或替换。
                    </p>
                </div>
                <div className="admin-settings-inline">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button type="primary" icon={<Plus className="size-3.5" />} onClick={() => openEditor(null)}>
                        新建灵感
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
                    <span className="admin-metric-label">灵感总数</span>
                    <span className="admin-metric-value">{formatCount(inspirations.length)}</span>
                    <span className="admin-metric-hint">含已下架的条目</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">已上架</span>
                    <span className="admin-metric-value">{formatCount(onlineCount)}</span>
                    <span className="admin-metric-hint">前台广场可见的数量</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">推荐位数量</span>
                    <span className="admin-metric-value">{formatCount(featuredCount)}</span>
                    <span className="admin-metric-hint">前台优先曝光的条目</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">外部素材</span>
                    <span className="admin-metric-value">{formatCount(sampleCount)}</span>
                    <span className="admin-metric-hint">带原始链接或来源标注，上线前需替换</span>
                </div>
            </div>

            <div className="admin-card">
                <div className="admin-card-head">
                    <span className="flex min-w-0 items-center gap-2">
                        <Sparkles className="size-4" />
                        <b style={{ fontSize: "var(--fs-body)" }}>全部灵感</b>
                    </span>
                    <span className="admin-user-sub">共 {formatCount(inspirations.length)} 条</span>
                </div>
                <Table<AdminCreationInspiration>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={inspirations}
                    columns={columns}
                    // 各列宽度之和（1136）要略小于这里的滚动宽度，也要小于表格容器宽度：
                    // 声明宽度超过容器时浏览器会按"最小内容宽度"压缩窄列，分类会被压成
                    // 竖排的单字。
                    scroll={{ x: 1180 }}
                    pagination={false}
                />
            </div>

            <Modal
                open={editorOpen}
                width={720}
                title={editingInspiration ? `编辑灵感 · ${editingInspiration.title}` : "新建灵感"}
                okText={editingInspiration ? "保存" : "创建"}
                cancelText="取消"
                confirmLoading={saving}
                onOk={() => form.submit()}
                onCancel={() => {
                    setEditorOpen(false);
                    setEditingInspiration(null);
                }}
            >
                <Form form={form} layout="vertical" initialValues={emptyInspiration} onFinish={(values) => void submit(values)} className="admin-form-narrow">
                    <Form.Item label="标题" name="title" rules={[{ required: true, message: "请填写灵感标题" }, { max: 40, message: "标题不超过 40 个字符" }]}>
                        <Input placeholder="雨夜霓虹 · 电影感开场" />
                    </Form.Item>
                    <Form.Item label="创作模式" name="mode" extra="决定这条灵感出现在哪个筛选项下，也决定套用后进入哪种创作。">
                        <Select options={modeOptions} />
                    </Form.Item>
                    <Form.Item label="分类" name="category" extra="自由文本，前台用作卡片左上角的题材小标（如「情感 · 叙事」）；留空时前台回落到署名文案。">
                        <Input placeholder="情感 · 叙事" />
                    </Form.Item>
                    <Form.Item label="时长角标" name="duration" extra="卡片右下角显示，形如 01:42；图片与文本条目留空即不显示。">
                        <Input placeholder="01:42" />
                    </Form.Item>
                    <Form.Item label="题材标签" name="tags" extra="只出现在首页主推荐卡底部，最多 4 个；回车分隔。">
                        <Select mode="tags" open={false} suffixIcon={null} tokenSeparators={[","]} placeholder="科幻、冒险、史诗" />
                    </Form.Item>
                    <Form.Item label="说明" name="description" extra="卡片上的一行摘要，不超过 200 个字符。">
                        <Input.TextArea rows={2} placeholder="宽银幕构图、环境反光与缓慢推进镜头" />
                    </Form.Item>
                    <Form.Item label="封面地址" name="coverUrl">
                        <Input placeholder="https://cdn.example.com/inspirations/neon-rain.jpg" />
                    </Form.Item>
                    <Form.Item label="提示词" name="prompt" extra="用户点开卡片后原样送进创作框，服务端不解析其中的变量语法。">
                        <Input.TextArea rows={6} placeholder="雨夜城市街口，霓虹灯倒映在湿润路面……" />
                    </Form.Item>
                    <Form.Item label="作者署名" name="author" extra="外部素材必填，前台会显示「示例素材 · 作者」。">
                        <Input placeholder="原作者昵称" />
                    </Form.Item>
                    <Form.Item label="点赞数" name="likes" extra="仅用于展示，不参与排序或计费。">
                        <InputNumber min={0} precision={0} style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="原始作品链接" name="sourceUrl" extra="指向外部作品页。填了它，前台就按「示例素材」署名并在图片请求上禁用 referer。">
                        <Input placeholder="https://example.com/detail/xxxx" />
                    </Form.Item>
                    <Form.Item label="来源标注" name="source" extra="开源提示词改编时填来源名（如 Storyteller），前台显示「开源改编 · CC0」；原创留空。">
                        <Input placeholder="Storyteller" />
                    </Form.Item>
                    <Form.Item label="上架状态" name="online" valuePropName="checked" extra="关闭即下架，前台广场不再展示。">
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
                title="删除灵感？"
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
                        即将删除灵感「{deleteTarget?.title}」。删除后不可恢复；如果只是暂时不想让用户看到，请改用「下架」，内容会保留。
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
