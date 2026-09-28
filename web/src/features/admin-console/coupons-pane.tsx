import { Button, DatePicker, Drawer, Form, Input, InputNumber, Modal, Select, Switch, Table, Tag, type TableProps } from "antd";
import dayjs, { type Dayjs } from "dayjs";
import { Pencil, Plus, ReceiptText, RefreshCw, Ticket, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";
import { formatMoneyFen } from "@/services/api/billing";

import {
    createAdminBillingCoupon,
    deleteAdminBillingCoupon,
    listAdminBillingCoupons,
    listAdminCouponRedemptions,
    updateAdminBillingCoupon,
    type AdminBillingCoupon,
    type AdminBillingCouponInput,
    type AdminCouponRedemption,
} from "./api";

/** 券码只允许小写字母、数字与连字符，避免大小写不一致导致结算时匹配不上。 */
const couponCodePattern = /^[a-z0-9-]+$/;

const kindOptions = [
    { value: "AMOUNT", label: "满减（直接抵扣金额）" },
    { value: "PERCENT", label: "折扣（按比例打折）" },
];

/**
 * 折扣率用万分比存整数：8 折 = 8000（即 80%），避免用 0.8 这种浮点参与金额计算。
 * 前台展示与后台编辑都在这一个换算关系上，界面里要写清楚，否则运营很容易填成 80。
 */
function discountZheOf(value: number) {
    return value / 1000;
}

function describeValue(coupon: AdminBillingCoupon) {
    return coupon.kind === "AMOUNT" ? formatMoneyFen(coupon.value) : `${discountZheOf(coupon.value)} 折`;
}

type CouponFormValues = {
    code: string;
    name: string;
    kind: "AMOUNT" | "PERCENT";
    amountYuan: number;
    discountZhe: number;
    minAmountYuan: number;
    totalQuota: number;
    perUserLimit: number;
    range: [Dayjs, Dayjs];
    enabled: boolean;
};

function valuesOf(coupon: AdminBillingCoupon): CouponFormValues {
    return {
        code: coupon.code,
        name: coupon.name,
        kind: coupon.kind,
        // 金额按「元」编辑，折扣按「折」编辑，提交时各自换算回整数存储单位。
        amountYuan: coupon.value / 100,
        discountZhe: discountZheOf(coupon.value),
        minAmountYuan: coupon.minAmountFen / 100,
        totalQuota: coupon.totalQuota,
        perUserLimit: coupon.perUserLimit,
        range: [dayjs(coupon.startsAt), dayjs(coupon.expiresAt)],
        enabled: coupon.enabled,
    };
}

function inputOf(values: CouponFormValues): AdminBillingCouponInput {
    const [startsAt, expiresAt] = values.range;
    return {
        code: values.code.trim(),
        name: values.name.trim(),
        kind: values.kind,
        value: values.kind === "AMOUNT" ? Math.round(Number(values.amountYuan ?? 0) * 100) : Math.round(Number(values.discountZhe ?? 0) * 1000),
        minAmountFen: Math.round(Number(values.minAmountYuan ?? 0) * 100),
        totalQuota: Number(values.totalQuota ?? 0),
        perUserLimit: Number(values.perUserLimit ?? 0),
        startsAt: startsAt.toISOString(),
        expiresAt: expiresAt.toISOString(),
        enabled: values.enabled,
    };
}

/**
 * 优惠券管理。
 *
 * 券的形态只有两种，但换算单位不同：满减是「元 → 分」，折扣是「折 → 万分比」。
 * 界面必须把这两个换算写清楚，并且一次只显示一种输入框，否则最典型的线上事故就是
 * 运营把"8 折"填进面额框，结果变成 ¥8 的满减券被大额订单薅走。
 */
export function CouponsPane() {
    const [coupons, setCoupons] = useState<AdminBillingCoupon[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [editorOpen, setEditorOpen] = useState(false);
    const [editingCoupon, setEditingCoupon] = useState<AdminBillingCoupon | null>(null);
    const [saving, setSaving] = useState(false);
    const [deleteTarget, setDeleteTarget] = useState<AdminBillingCoupon | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState("");
    const [form] = Form.useForm<CouponFormValues>();
    const kind = Form.useWatch("kind", form) ?? "AMOUNT";

    const [redemptionTarget, setRedemptionTarget] = useState<AdminBillingCoupon | null>(null);
    const [redemptions, setRedemptions] = useState<AdminCouponRedemption[]>([]);
    const [redemptionTotal, setRedemptionTotal] = useState(0);
    const [redemptionPage, setRedemptionPage] = useState(1);
    const [redemptionPageSize, setRedemptionPageSize] = useState(20);
    const [redemptionsLoading, setRedemptionsLoading] = useState(false);

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminBillingCoupons();
            setCoupons(payload.coupons ?? []);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载优惠券失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const loadRedemptions = useCallback(async (couponId: string, nextPage: number, nextPageSize: number) => {
        setRedemptionsLoading(true);
        setError("");
        try {
            const payload = await listAdminCouponRedemptions(couponId, { page: nextPage, pageSize: nextPageSize });
            setRedemptions(payload.redemptions ?? []);
            setRedemptionTotal(payload.total ?? 0);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载核销记录失败");
        } finally {
            setRedemptionsLoading(false);
        }
    }, []);

    useEffect(() => {
        if (!redemptionTarget) return;
        void loadRedemptions(redemptionTarget.id, redemptionPage, redemptionPageSize);
    }, [redemptionTarget, redemptionPage, redemptionPageSize, loadRedemptions]);

    const openEditor = useCallback(
        (coupon: AdminBillingCoupon | null) => {
            setEditingCoupon(coupon);
            setEditorOpen(true);
            setError("");
            setNotice("");
            const range = coupon ? valuesOf(coupon).range : [dayjs(), dayjs().add(30, "day")];
            form.setFieldsValue(
                coupon
                    ? valuesOf(coupon)
                    : {
                          code: "",
                          name: "",
                          kind: "AMOUNT",
                          amountYuan: 0,
                          discountZhe: 10,
                          minAmountYuan: 0,
                          totalQuota: 0,
                          perUserLimit: 1,
                          range: range as [Dayjs, Dayjs],
                          enabled: true,
                      },
            );
        },
        [form],
    );

    const submit = async (values: CouponFormValues) => {
        setSaving(true);
        setError("");
        setNotice("");
        try {
            if (editingCoupon) {
                await updateAdminBillingCoupon(editingCoupon.id, inputOf(values));
                setNotice(`优惠券「${values.name.trim()}」已更新。`);
            } else {
                await createAdminBillingCoupon(inputOf(values));
                setNotice(`优惠券「${values.name.trim()}」已创建。`);
            }
            setEditorOpen(false);
            setEditingCoupon(null);
            await load();
        } catch (saveError) {
            setError(saveError instanceof Error ? saveError.message : "保存优惠券失败");
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
            await deleteAdminBillingCoupon(deleteTarget.id);
            setNotice(`优惠券「${deleteTarget.name}」已删除。`);
            setDeleteTarget(null);
            await load();
        } catch (deleteFailure) {
            // 已核销过的券删除会被服务端拒绝（会影响历史订单的抵扣追溯），原因照原样展示。
            setDeleteError(deleteFailure instanceof Error ? deleteFailure.message : "删除优惠券失败");
        } finally {
            setDeleting(false);
        }
    };

    const columns: TableProps<AdminBillingCoupon>["columns"] = [
        {
            title: "券码 / 名称",
            key: "coupon",
            width: 220,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-console-mono">{row.code}</span>
                    <span className="admin-user-sub">{row.name}</span>
                </span>
            ),
        },
        {
            title: "类型 / 面额",
            key: "value",
            width: 160,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{describeValue(row)}</span>
                    <span className="admin-user-sub">{row.kind === "AMOUNT" ? "满减" : "折扣"}</span>
                </span>
            ),
        },
        {
            title: "门槛",
            dataIndex: "minAmountFen",
            key: "minAmountFen",
            width: 130,
            render: (value: number) => (value > 0 ? `满 ${formatMoneyFen(value)} 可用` : "无门槛"),
        },
        {
            title: "已用 / 总量",
            key: "quota",
            width: 130,
            render: (_, row) => `${formatCount(row.usedCount)} / ${row.totalQuota > 0 ? formatCount(row.totalQuota) : "不限"}`,
        },
        {
            title: "每人限领",
            dataIndex: "perUserLimit",
            key: "perUserLimit",
            width: 100,
            render: (value: number) => (value > 0 ? `${formatCount(value)} 张` : "不限"),
        },
        {
            title: "有效期",
            key: "period",
            width: 220,
            render: (_, row) => (
                <span className="admin-user-sub">
                    {formatDateTime(row.startsAt)} ~ {formatDateTime(row.expiresAt)}
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
            title: "操作",
            key: "actions",
            width: 230,
            render: (_, row) => (
                <div className="admin-settings-inline">
                    <Button size="small" type="text" icon={<ReceiptText className="size-3.5" />} onClick={() => { setRedemptionPage(1); setRedemptionTarget(row); }}>
                        核销记录
                    </Button>
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

    const redemptionColumns: TableProps<AdminCouponRedemption>["columns"] = [
        { title: "账号", dataIndex: "userId", key: "userId", width: 220, render: (value: string) => <span className="admin-console-mono">{value}</span> },
        { title: "订单", dataIndex: "orderId", key: "orderId", width: 220, render: (value: string) => <span className="admin-console-mono">{value}</span> },
        { title: "抵扣额", dataIndex: "discountFen", key: "discountFen", width: 120, render: (value: number) => <b>{formatMoneyFen(value)}</b> },
        { title: "核销时间", dataIndex: "redeemedAt", key: "redeemedAt", width: 180, render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span> },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">优惠券管理</h2>
                    <p className="admin-section-desc">
                        满减券按「元」填写面额（保存时换算成「分」）；折扣券按「折」填写，例如 8 折会存成万分比 8000。金额一律以整数分存储，避免浮点折扣在对账时丢精度。
                    </p>
                </div>
                <div className="admin-settings-inline">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button type="primary" icon={<Plus className="size-3.5" />} onClick={() => openEditor(null)}>
                        新建优惠券
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
                        <Ticket className="size-4" />
                        <b style={{ fontSize: "var(--fs-body)" }}>全部优惠券</b>
                    </span>
                    <span className="admin-user-sub">共 {formatCount(coupons.length)} 张</span>
                </div>
                <Table<AdminBillingCoupon>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={coupons}
                    columns={columns}
                    scroll={{ x: 1300 }}
                    pagination={false}
                />
            </div>

            <Modal
                open={editorOpen}
                width={720}
                title={editingCoupon ? `编辑优惠券 · ${editingCoupon.name}` : "新建优惠券"}
                okText={editingCoupon ? "保存" : "创建"}
                cancelText="取消"
                confirmLoading={saving}
                onOk={() => form.submit()}
                onCancel={() => {
                    setEditorOpen(false);
                    setEditingCoupon(null);
                }}
            >
                <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} className="admin-form-narrow">
                    <Form.Item label="券码" name="code" rules={[{ required: true, message: "请填写券码" }, { pattern: couponCodePattern, message: "券码只允许小写字母、数字与连字符" }]}>
                        <Input placeholder="launch-2026" />
                    </Form.Item>
                    <Form.Item label="名称" name="name" rules={[{ required: true, message: "请填写券名称" }]}>
                        <Input placeholder="上线庆 · 满 100 减 20" />
                    </Form.Item>
                    <Form.Item label="类型" name="kind" rules={[{ required: true, message: "请选择券类型" }]} extra="切换类型后只显示对应的一种输入框，避免把折扣填进金额。">
                        <Select options={kindOptions} />
                    </Form.Item>
                    <Form.Item
                        label="面额（元）"
                        name="amountYuan"
                        hidden={kind !== "AMOUNT"}
                        extra="按「元」填写，保存时自动换算成「分」，例如 20 元 = 2000 分。"
                        rules={kind === "AMOUNT" ? [{ required: true, message: "请填写面额" }] : undefined}
                    >
                        <InputNumber min={0} precision={2} step={1} style={{ width: 220 }} />
                    </Form.Item>
                    <Form.Item
                        label="折扣率（折）"
                        name="discountZhe"
                        hidden={kind !== "PERCENT"}
                        extra="按「折」填写：8 折填 8，保存时换算成万分比 8000（即打 8 折）。"
                        rules={kind === "PERCENT" ? [{ required: true, message: "请填写折扣率" }, { type: "number", min: 0.1, max: 9.9, message: "折扣率需在 0.1 ~ 9.9 折之间" }] : undefined}
                    >
                        <InputNumber min={0.1} max={9.9} precision={1} step={0.5} style={{ width: 220 }} />
                    </Form.Item>
                    <Form.Item label="使用门槛（元）" name="minAmountYuan" extra="0 表示无门槛；按「元」填写，保存时换算成「分」。">
                        <InputNumber min={0} precision={2} style={{ width: 220 }} />
                    </Form.Item>
                    <Form.Item label="发放总量" name="totalQuota" extra="0 表示不限量。">
                        <InputNumber min={0} precision={0} style={{ width: 220 }} />
                    </Form.Item>
                    <Form.Item label="每人限领" name="perUserLimit" extra="0 表示不限制。">
                        <InputNumber min={0} precision={0} style={{ width: 220 }} />
                    </Form.Item>
                    <Form.Item label="有效期" name="range" rules={[{ required: true, message: "请选择有效期" }]}>
                        <DatePicker.RangePicker showTime style={{ width: 420 }} />
                    </Form.Item>
                    <Form.Item label="启用" name="enabled" valuePropName="checked">
                        <Switch />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={deleteTarget !== null}
                title="删除优惠券？"
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
                        即将删除优惠券「{deleteTarget?.name}」（{deleteTarget?.code}）。删除后不可恢复；
                        已经产生过核销记录的券会被服务端拒绝删除，此时请改用「停用」保留历史抵扣记录。
                    </p>
                    {deleteError ? (
                        <div className="admin-notice is-error">
                            <span>{deleteError}</span>
                        </div>
                    ) : null}
                </div>
            </Modal>

            <Drawer
                open={redemptionTarget !== null}
                size={720}
                title={redemptionTarget ? `核销记录 · ${redemptionTarget.name}（${redemptionTarget.code}）` : "核销记录"}
                onClose={() => setRedemptionTarget(null)}
            >
                <div className="flex flex-col gap-3">
                    <span className="admin-user-sub">共 {formatCount(redemptionTotal)} 条核销记录</span>
                    <Table<AdminCouponRedemption>
                        rowKey="id"
                        size="small"
                        loading={redemptionsLoading}
                        dataSource={redemptions}
                        columns={redemptionColumns}
                        scroll={{ x: 740 }}
                        pagination={{
                            current: redemptionPage,
                            pageSize: redemptionPageSize,
                            total: redemptionTotal,
                            size: "small",
                            showSizeChanger: true,
                            showTotal: (value) => `共 ${value} 条`,
                            onChange: (nextPage, nextSize) => {
                                setRedemptionPage(nextPage);
                                setRedemptionPageSize(nextSize);
                            },
                        }}
                    />
                </div>
            </Drawer>
        </div>
    );
}
