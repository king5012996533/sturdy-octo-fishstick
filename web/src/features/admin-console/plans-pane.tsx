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

/**
 * 周期为 0 且带积分 = 纯积分包：只加积分、不产生订阅。
 *
 * 这是运营卖积分的主要形态，列表里必须一眼能认出来，否则「有效期 0 天」会被读成漏填。
 */
function isCreditPack(plan: AdminBillingPlan) {
    return plan.periodDays === 0 && (plan.credits ?? 0) + (plan.giftCredits ?? 0) > 0;
}

/**
 * 周期为 0 就必须带积分，否则这是一件卖出去也没有东西能交付的空商品。
 *
 * 服务端同样会拒绝，但在这里拦住能让运营当场看到原因，而不是提交后收到一句 400。
 * 校验挂在「到账积分」上并声明依赖另外两个字段，改周期或改赠送额时都会重新判一次。
 */
function validateCreditPack(credits: unknown, periodDays: unknown, giftCredits: unknown) {
    const days = Number(periodDays ?? 0);
    const total = Number(credits ?? 0) + Number(giftCredits ?? 0);
    if (days === 0 && total <= 0) {
        return Promise.reject(new Error("周期为 0 时必须是积分包，请填写到账积分或赠送积分"));
    }
    return Promise.resolve();
}

type PlanFormValues = {
    code: string;
    name: string;
    description?: string;
    sortOrder?: number;
    priceYuan: number;
    periodDays: number;
    credits: number;
    giftCredits: number;
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
    credits: 0,
    giftCredits: 0,
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
        credits: plan.credits ?? 0,
        giftCredits: plan.giftCredits ?? 0,
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
        credits: Math.trunc(Number(values.credits ?? 0)),
        giftCredits: Math.trunc(Number(values.giftCredits ?? 0)),
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
    // 用于把「周期 0 = 纯积分包」这件事当场说出来，而不是让运营提交后才发现。
    const periodDays = Form.useWatch("periodDays", form);

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
            render: (value: number, row) => (isCreditPack(row) ? <Tag color="blue">积分包</Tag> : `${formatCount(value)} 天`),
        },
        {
            title: "到账 / 赠送积分",
            key: "credits",
            width: 170,
            render: (_, row) => {
                const credits = row.credits ?? 0;
                const gifted = row.giftCredits ?? 0;
                if (credits + gifted === 0) return <span className="admin-user-sub">—</span>;
                return (
                    <span className="admin-user-sub">
                        <b style={{ color: "var(--admin-ink)", fontWeight: 500 }}>{formatCount(credits)}</b> · 赠 {formatCount(gifted)}
                    </span>
                );
            },
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
                    scroll={{ x: 1320 }}
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
                    <Form.Item
                        label="有效期（天）"
                        name="periodDays"
                        extra="允许填 0：0 表示「纯积分包」——只加积分、不产生订阅，此时必须填写积分。1 到 3650 之间为正常订阅周期。"
                        rules={[{ required: true, message: "请填写有效期天数" }]}
                    >
                        <InputNumber min={0} max={3650} precision={0} style={{ width: 200 }} />
                    </Form.Item>
                    {Number(periodDays ?? 0) === 0 ? (
                        <div className="admin-notice" style={{ marginBottom: 16 }}>
                            <span>这是纯积分包：用户下单后只加积分、不生成订阅，所以下面必须至少填写一项积分。</span>
                        </div>
                    ) : null}
                    <Form.Item
                        label="到账积分（分）"
                        name="credits"
                        dependencies={["periodDays", "giftCredits"]}
                        extra="单位是「分」，且 1 分 = 1 积分：填 1000 表示购买后到账 1000 积分。这里不做元/分换算，也不与价格联动。"
                        rules={[
                            {
                                validator: (_, value) => validateCreditPack(value, form.getFieldValue("periodDays"), form.getFieldValue("giftCredits")),
                            },
                        ]}
                    >
                        <InputNumber min={0} precision={0} step={1000} style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="赠送积分（分）" name="giftCredits" extra="平台额外赠送的积分（分），到账时与上面那笔分开入账、分开展示；不填按 0 处理。">
                        <InputNumber min={0} precision={0} step={1000} style={{ width: 200 }} />
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
