import { Button, Form, Input, InputNumber, Modal, Switch, Table, Tag, type TableProps } from "antd";
import { Layers, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount } from "@/lib/format-usage";
import { formatMoneyFen } from "@/services/api/billing";

import {
    createAdminBillingPlan,
    deleteAdminBillingPlan,
    listAdminBillingPlans,
    updateAdminBillingPlan,
    type AdminBillingPlan,
    type AdminBillingPlanInput,
} from "./api";

/** 套餐 code 只能是小写字母、数字与连字符：它同时是订单里的 planCode 与前台路由片段。 */
const planCodePattern = /^[a-z0-9-]+$/;

type PlanFormValues = {
    code: string;
    name: string;
    description?: string;
    sortOrder?: number;
    priceYuan: number;
    periodDays: number;
    quotaCalls: number;
    quotaStorageMb: number;
    quotaMembers: number;
    enabled: boolean;
};

const emptyPlan: PlanFormValues = {
    code: "",
    name: "",
    description: "",
    sortOrder: 0,
    priceYuan: 0,
    periodDays: 30,
    quotaCalls: 0,
    quotaStorageMb: 0,
    quotaMembers: 1,
    enabled: true,
};

function valuesOf(plan: AdminBillingPlan): PlanFormValues {
    return {
        code: plan.code,
        name: plan.name,
        description: plan.description,
        sortOrder: plan.sortOrder,
        // 表单里按「元」编辑，提交前再乘 100 换回分。
        priceYuan: plan.priceFen / 100,
        periodDays: plan.periodDays,
        quotaCalls: plan.quotaCalls,
        quotaStorageMb: plan.quotaStorageMb,
        quotaMembers: plan.quotaMembers,
        enabled: plan.enabled,
    };
}

function inputOf(values: PlanFormValues): AdminBillingPlanInput {
    return {
        code: values.code.trim(),
        name: values.name.trim(),
        description: (values.description ?? "").trim(),
        sortOrder: Number(values.sortOrder ?? 0),
        enabled: values.enabled,
        priceFen: Math.round(Number(values.priceYuan ?? 0) * 100),
        periodDays: Number(values.periodDays ?? 0),
        quotaCalls: Number(values.quotaCalls ?? 0),
        quotaStorageMb: Number(values.quotaStorageMb ?? 0),
        quotaMembers: Number(values.quotaMembers ?? 0),
    };
}

/**
 * 套餐管理。
 *
 * 删除套餐是有依赖的破坏性动作：一旦有订单引用过它，历史订单的 planName/planCode 就
 * 变成了无法追溯的外键。所以服务端会拒绝删除，而这里不做"拦截式报错"——直接把服务端
 * 返回的原因原样显示出来（例如"该套餐已有订单，请改为停用"），并提供停用入口，让运营
 * 用「停用」而不是「删除」来处理下架，历史订单因此保持完整。
 */
export function PlansPane() {
    const [plans, setPlans] = useState<AdminBillingPlan[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [editorOpen, setEditorOpen] = useState(false);
    const [editingPlan, setEditingPlan] = useState<AdminBillingPlan | null>(null);
    const [saving, setSaving] = useState(false);
    const [deleteTarget, setDeleteTarget] = useState<AdminBillingPlan | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState("");
    const [form] = Form.useForm<PlanFormValues>();

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminBillingPlans();
            setPlans(payload.plans ?? []);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载套餐失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const openEditor = useCallback(
        (plan: AdminBillingPlan | null) => {
            setEditingPlan(plan);
            setEditorOpen(true);
            setError("");
            setNotice("");
            form.setFieldsValue(plan ? valuesOf(plan) : emptyPlan);
        },
        [form],
    );

    const submit = async (values: PlanFormValues) => {
        setSaving(true);
        setError("");
        setNotice("");
        try {
            if (editingPlan) {
                await updateAdminBillingPlan(editingPlan.id, inputOf(values));
                setNotice(`套餐「${values.name.trim()}」已更新。`);
            } else {
                await createAdminBillingPlan(inputOf(values));
                setNotice(`套餐「${values.name.trim()}」已创建。`);
            }
            setEditorOpen(false);
            setEditingPlan(null);
            await load();
        } catch (saveError) {
            setError(saveError instanceof Error ? saveError.message : "保存套餐失败");
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
            await deleteAdminBillingPlan(deleteTarget.id);
            setNotice(`套餐「${deleteTarget.name}」已删除。`);
            setDeleteTarget(null);
            await load();
        } catch (deleteFailure) {
            // 服务端拒绝时把原因留在弹窗里，运营才能当场决定"改为停用"。
            setDeleteError(deleteFailure instanceof Error ? deleteFailure.message : "删除套餐失败");
        } finally {
            setDeleting(false);
        }
    };

    const columns: TableProps<AdminBillingPlan>["columns"] = [
        {
            title: "套餐",
            key: "plan",
            width: 240,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{row.name}</span>
                    <span className="admin-user-sub">
                        {row.code} · 排序 {formatCount(row.sortOrder)}
                    </span>
                </span>
            ),
        },
        {
            title: "状态",
            dataIndex: "enabled",
            key: "enabled",
            width: 90,
            render: (enabled: boolean) => <Tag color={enabled ? "green" : "default"}>{enabled ? "启用中" : "已停用"}</Tag>,
        },
        {
            title: "价格",
            dataIndex: "priceFen",
            key: "priceFen",
            width: 120,
            render: (value: number) => <b>{formatMoneyFen(value)}</b>,
        },
        {
            title: "有效期",
            dataIndex: "periodDays",
            key: "periodDays",
            width: 100,
            render: (value: number) => `${formatCount(value)} 天`,
        },
        {
            title: "配额（调用 / 存储 / 成员）",
            key: "quota",
            width: 240,
            render: (_, row) => (
                <span className="admin-user-sub">
                    {formatCount(row.quotaCalls)} 次 · {formatCount(row.quotaStorageMb)} MB · {formatCount(row.quotaMembers)} 人
                </span>
            ),
        },
        {
            title: "说明",
            dataIndex: "description",
            key: "description",
            render: (value: string) => <span className="admin-user-sub">{value || "—"}</span>,
        },
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
                    <h2 className="admin-section-title">套餐管理</h2>
                    <p className="admin-section-desc">
                        这里的套餐就是前台下单时可选的价格与配额。已经产生过订单的套餐建议「停用」而不是「删除」，停用后前台不再可售，但历史订单与已购权益不受影响。
                    </p>
                </div>
                <div className="admin-settings-inline">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button type="primary" icon={<Plus className="size-3.5" />} onClick={() => openEditor(null)}>
                        新建套餐
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

            <div className="admin-card">
                <div className="admin-card-head">
                    <span className="flex min-w-0 items-center gap-2">
                        <Layers className="size-4" />
                        <b style={{ fontSize: "var(--fs-body)" }}>全部套餐</b>
                    </span>
                    <span className="admin-user-sub">共 {formatCount(plans.length)} 个</span>
                </div>
                <Table<AdminBillingPlan>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={plans}
                    columns={columns}
                    scroll={{ x: 1180 }}
                    pagination={false}
                />
            </div>

            <Modal
                open={editorOpen}
                width={720}
                title={editingPlan ? `编辑套餐 · ${editingPlan.name}` : "新建套餐"}
                okText={editingPlan ? "保存" : "创建"}
                cancelText="取消"
                confirmLoading={saving}
                onOk={() => form.submit()}
                onCancel={() => {
                    setEditorOpen(false);
                    setEditingPlan(null);
                }}
            >
                <Form form={form} layout="vertical" initialValues={emptyPlan} onFinish={(values) => void submit(values)} className="admin-form-narrow">
                    <Form.Item label="套餐 code" name="code" rules={[{ required: true, message: "请填写套餐 code" }, { pattern: planCodePattern, message: "code 只允许小写字母、数字与连字符" }]}>
                        <Input placeholder="pro-monthly" />
                    </Form.Item>
                    <Form.Item label="套餐名称" name="name" rules={[{ required: true, message: "请填写套餐名称" }]}>
                        <Input placeholder="专业版 · 月付" />
                    </Form.Item>
                    <Form.Item label="说明" name="description">
                        <Input.TextArea rows={2} placeholder="展示在前台套餐卡片上的说明" />
                    </Form.Item>
                    <Form.Item label="排序" name="sortOrder" extra="数字越小越靠前。">
                        <InputNumber min={0} precision={0} style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="价格（元）" name="priceYuan" extra="按「元」填写，保存时自动换算成「分」存储，例如 39.9 元 = 3990 分。" rules={[{ required: true, message: "请填写价格" }]}>
                        <InputNumber min={0} precision={2} step={1} style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="有效期（天）" name="periodDays" rules={[{ required: true, message: "请填写有效期天数" }]}>
                        <InputNumber min={1} precision={0} style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="配额 · 调用次数" name="quotaCalls" extra="0 表示不限制。">
                        <InputNumber min={0} precision={0} style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="配额 · 存储（MB）" name="quotaStorageMb">
                        <InputNumber min={0} precision={0} style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="配额 · 成员数" name="quotaMembers">
                        <InputNumber min={1} precision={0} style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="启用" name="enabled" valuePropName="checked">
                        <Switch />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={deleteTarget !== null}
                title="删除套餐？"
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
                        即将删除套餐「{deleteTarget?.name}」（{deleteTarget?.code}）。删除后不可恢复；
                        如果该套餐已经产生过订单，服务端会拒绝删除并说明原因，此时请改用「停用」让前台不再可售。
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
