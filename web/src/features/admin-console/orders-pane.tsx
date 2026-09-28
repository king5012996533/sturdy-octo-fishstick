import { Button, Collapse, Form, Input, Modal, Select, Switch, Table, Tag, type TableProps } from "antd";
import { CreditCard, RefreshCw, RotateCcw, Search, Wallet } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";
import { formatMoneyFen } from "@/services/api/billing";

import {
    listAdminBillingOrders,
    listAdminPaymentChannels,
    markAdminBillingOrderPaid,
    refundAdminBillingOrder,
    updateAdminPaymentChannel,
    type AdminBillingOrder,
    type AdminBillingOrderPage,
    type AdminPaymentChannel,
} from "./api";

const statusOptions = [
    { value: "", label: "全部状态" },
    { value: "PENDING", label: "待支付" },
    { value: "PAID", label: "已支付" },
    { value: "REFUNDED", label: "已退款" },
    { value: "CANCELED", label: "已取消" },
    { value: "FAILED", label: "支付失败" },
];

const statusViews: Record<AdminBillingOrder["status"], { label: string; color: string }> = {
    PENDING: { label: "待支付", color: "gold" },
    PAID: { label: "已支付", color: "green" },
    REFUNDED: { label: "已退款", color: "blue" },
    CANCELED: { label: "已取消", color: "default" },
    FAILED: { label: "支付失败", color: "red" },
};

const sourceLabels: Record<string, string> = {
    database: "后台配置",
    environment: "环境变量",
    console: "仅写日志",
};

/**
 * 密钥字段靠字段名识别：服务端只回 hasSecret 标记、不回密钥明文，
 * 所以这里必须知道哪些键是"留空即保持原值"的写敏感字段，避免把空串当成"清空密钥"提交。
 */
const secretKeyPattern = /secret|key|password|token|mch|appid|private/i;

function isSecretKey(key: string) {
    return secretKeyPattern.test(key);
}

function userLabel(order: AdminBillingOrder) {
    return order.userName || order.userEmail || order.userPhone || order.userId;
}

type MarkPaidFormValues = { remark: string };
type RefundFormValues = { reason: string };

/**
 * 订单管理。
 *
 * 两条写路径都刻意收窄：`mark-paid` 只解决"渠道掉单、钱其实到账了"这一种情况，
 * 因此备注必填说明依据；退款是不可逆的资金动作，原因必填以便对账时能追溯是谁批的。
 * 顶部指标卡读的是 `page.revenue`——它是全量经营读数，不随后台筛选变化，
 * 否则运营一筛选就会以为"今天只收了 39 块"。
 */
export function OrdersPane() {
    const [orders, setOrders] = useState<AdminBillingOrder[]>([]);
    const [revenue, setRevenue] = useState<AdminBillingOrderPage["revenue"] | null>(null);
    const [total, setTotal] = useState(0);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [status, setStatus] = useState("");
    const [planCode, setPlanCode] = useState("");
    const [keywordInput, setKeywordInput] = useState("");
    const [keyword, setKeyword] = useState("");
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [busyId, setBusyId] = useState("");
    const [markPaidTarget, setMarkPaidTarget] = useState<AdminBillingOrder | null>(null);
    const [refundTarget, setRefundTarget] = useState<AdminBillingOrder | null>(null);
    const [markPaidForm] = Form.useForm<MarkPaidFormValues>();
    const [refundForm] = Form.useForm<RefundFormValues>();

    const [channels, setChannels] = useState<AdminPaymentChannel[]>([]);
    const [channelDrafts, setChannelDrafts] = useState<Record<string, Record<string, string>>>({});
    const [channelEnabled, setChannelEnabled] = useState<Record<string, boolean>>({});
    const [channelBusy, setChannelBusy] = useState("");
    const [channelNotice, setChannelNotice] = useState("");
    const [channelError, setChannelError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminBillingOrders({ status, keyword, planCode, page, pageSize });
            setOrders(payload.orders ?? []);
            setTotal(payload.total ?? 0);
            setRevenue(payload.revenue ?? null);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载订单失败");
        } finally {
            setLoading(false);
        }
    }, [status, keyword, planCode, page, pageSize]);

    useEffect(() => {
        void load();
    }, [load]);

    const applyChannels = useCallback((payload: AdminPaymentChannel[]) => {
        setChannels(payload);
        const drafts: Record<string, Record<string, string>> = {};
        const enabled: Record<string, boolean> = {};
        payload.forEach((channel) => {
            const draft: Record<string, string> = {};
            Object.entries(channel.config ?? {}).forEach(([key, value]) => {
                // 密钥永不回显：输入框一律从空开始，留空保存即保持原值。
                draft[key] = isSecretKey(key) ? "" : value;
            });
            drafts[channel.channel] = draft;
            enabled[channel.channel] = channel.enabled;
        });
        setChannelDrafts(drafts);
        setChannelEnabled(enabled);
    }, []);

    const loadChannels = useCallback(async () => {
        setChannelError("");
        try {
            const payload = await listAdminPaymentChannels();
            applyChannels(payload.channels ?? []);
        } catch (loadError) {
            setChannelError(loadError instanceof Error ? loadError.message : "加载支付渠道失败");
        }
    }, [applyChannels]);

    useEffect(() => {
        void loadChannels();
    }, [loadChannels]);

    const saveChannel = async (channel: AdminPaymentChannel) => {
        setChannelBusy(channel.channel);
        setChannelError("");
        setChannelNotice("");
        try {
            const draft = channelDrafts[channel.channel] ?? {};
            const config: Record<string, string> = {};
            Object.entries(draft).forEach(([key, value]) => {
                const trimmed = value.trim();
                // 留空表示"不修改"，不把空串塞回去，否则会把已配置的密钥覆盖成空。
                if (trimmed) config[key] = trimmed;
            });
            const payload = await updateAdminPaymentChannel(channel.channel, {
                enabled: channelEnabled[channel.channel] ?? channel.enabled,
                config,
            });
            applyChannels(payload.channels ?? []);
            setChannelNotice(`渠道「${channel.channel}」配置已保存。`);
        } catch (saveError) {
            setChannelError(saveError instanceof Error ? saveError.message : "保存支付渠道失败");
        } finally {
            setChannelBusy("");
        }
    };

    const submitMarkPaid = async (values: MarkPaidFormValues) => {
        if (!markPaidTarget) return;
        setBusyId(markPaidTarget.id);
        setError("");
        setNotice("");
        try {
            await markAdminBillingOrderPaid(markPaidTarget.id, values.remark.trim());
            setNotice(`订单 ${markPaidTarget.orderNo} 已标记为已支付。`);
            setMarkPaidTarget(null);
            markPaidForm.resetFields();
            await load();
        } catch (markError) {
            setError(markError instanceof Error ? markError.message : "标记已支付失败");
        } finally {
            setBusyId("");
        }
    };

    const submitRefund = async (values: RefundFormValues) => {
        if (!refundTarget) return;
        setBusyId(refundTarget.id);
        setError("");
        setNotice("");
        try {
            await refundAdminBillingOrder(refundTarget.id, values.reason.trim());
            setNotice(`订单 ${refundTarget.orderNo} 已发起退款。`);
            setRefundTarget(null);
            refundForm.resetFields();
            await load();
        } catch (refundError) {
            setError(refundError instanceof Error ? refundError.message : "退款失败");
        } finally {
            setBusyId("");
        }
    };

    const columns: TableProps<AdminBillingOrder>["columns"] = [
        {
            title: "订单号",
            dataIndex: "orderNo",
            key: "orderNo",
            width: 200,
            render: (value: string, row) => (
                <span className="admin-user-cell">
                    <span className="admin-console-mono">{value}</span>
                    {row.couponCode ? <span className="admin-user-sub">券 {row.couponCode}</span> : null}
                </span>
            ),
        },
        {
            title: "用户",
            key: "user",
            width: 220,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{userLabel(row)}</span>
                    <span className="admin-user-sub">{row.userEmail || row.userPhone || row.userId}</span>
                </span>
            ),
        },
        {
            title: "套餐",
            key: "plan",
            width: 160,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{row.planName}</span>
                    <span className="admin-user-sub">{row.planCode}</span>
                </span>
            ),
        },
        {
            title: "原价 / 优惠 / 实付",
            key: "amount",
            width: 200,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span className="admin-user-name">{formatMoneyFen(row.payableFen)}</span>
                    <span className="admin-user-sub">
                        {formatMoneyFen(row.amountFen)} · 优惠 {formatMoneyFen(row.discountFen)}
                    </span>
                </span>
            ),
        },
        {
            title: "状态",
            dataIndex: "status",
            key: "status",
            width: 110,
            render: (value: AdminBillingOrder["status"]) => {
                const view = statusViews[value] ?? { label: value, color: "default" };
                return <Tag color={view.color}>{view.label}</Tag>;
            },
        },
        {
            title: "渠道",
            key: "provider",
            width: 150,
            render: (_, row) => (
                <span className="admin-user-cell">
                    <span>{row.provider || "—"}</span>
                    {row.providerOrderNo ? <span className="admin-user-sub">{row.providerOrderNo}</span> : null}
                </span>
            ),
        },
        {
            title: "创建时间",
            dataIndex: "createdAt",
            key: "createdAt",
            width: 180,
            render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span>,
        },
        {
            title: "支付时间",
            dataIndex: "paidAt",
            key: "paidAt",
            width: 180,
            render: (value?: string) => <span className="admin-user-sub">{value ? formatDateTime(value) : "—"}</span>,
        },
        {
            title: "操作",
            key: "actions",
            width: 140,
            fixed: "right",
            render: (_, row) => {
                if (row.status === "PENDING") {
                    return (
                        <Button
                            size="small"
                            type="text"
                            icon={<Wallet className="size-3.5" />}
                            loading={busyId === row.id}
                            onClick={() => {
                                markPaidForm.setFieldsValue({ remark: "" });
                                setMarkPaidTarget(row);
                            }}
                        >
                            标记已支付
                        </Button>
                    );
                }
                if (row.status === "PAID") {
                    return (
                        <Button
                            size="small"
                            type="text"
                            danger
                            icon={<RotateCcw className="size-3.5" />}
                            loading={busyId === row.id}
                            onClick={() => {
                                refundForm.setFieldsValue({ reason: "" });
                                setRefundTarget(row);
                            }}
                        >
                            退款
                        </Button>
                    );
                }
                return <span className="admin-user-sub">—</span>;
            },
        },
    ];

    const channelItems = channels.map((channel) => ({
        key: channel.channel,
        label: (
            <span className="admin-settings-inline">
                <b>{channel.channel}</b>
                <Tag color={channel.source === "database" ? "geekblue" : channel.source === "environment" ? "gold" : "default"}>
                    {sourceLabels[channel.source] ?? channel.source}
                </Tag>
                <Tag color={channel.ready ? "green" : channel.enabled ? "orange" : "default"}>
                    {channel.ready ? "可下单" : channel.enabled ? "已启用但配置不完整" : "未启用"}
                </Tag>
            </span>
        ),
        children: (
            <div className="flex flex-col gap-3">
                <div className="admin-gateway-status">
                    <span className="admin-gateway-detail">{channel.detail || "—"}</span>
                </div>
                <div className="admin-gateway-grid">
                    {Object.keys(channel.config ?? {}).length === 0 ? (
                        <span className="admin-user-sub">该渠道没有可后台配置的字段。</span>
                    ) : (
                        Object.keys(channel.config).map((key) => (
                            <label className="admin-settings-field" key={key}>
                                <span className="admin-settings-field-label">{key}</span>
                                {isSecretKey(key) ? (
                                    <Input.Password
                                        value={channelDrafts[channel.channel]?.[key] ?? ""}
                                        placeholder={channel.hasSecret ? "已设置，留空表示不修改" : "未设置"}
                                        onChange={(event) =>
                                            setChannelDrafts((current) => ({
                                                ...current,
                                                [channel.channel]: { ...(current[channel.channel] ?? {}), [key]: event.target.value },
                                            }))
                                        }
                                    />
                                ) : (
                                    <Input
                                        value={channelDrafts[channel.channel]?.[key] ?? ""}
                                        onChange={(event) =>
                                            setChannelDrafts((current) => ({
                                                ...current,
                                                [channel.channel]: { ...(current[channel.channel] ?? {}), [key]: event.target.value },
                                            }))
                                        }
                                    />
                                )}
                            </label>
                        ))
                    )}
                </div>
                <div className="admin-settings-inline">
                    <span>启用</span>
                    <Switch
                        checked={channelEnabled[channel.channel] ?? channel.enabled}
                        onChange={(checked) => setChannelEnabled((current) => ({ ...current, [channel.channel]: checked }))}
                    />
                    <Button
                        type="primary"
                        icon={<CreditCard className="size-3.5" />}
                        loading={channelBusy === channel.channel}
                        onClick={() => void saveChannel(channel)}
                    >
                        保存渠道
                    </Button>
                    {channel.updatedAt ? <span className="admin-user-sub">最近更新 {formatDateTime(channel.updatedAt)}</span> : null}
                </div>
            </div>
        ),
    }));

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">订单管理</h2>
                    <p className="admin-section-desc">
                        指标卡读的是全量经营读数，不随下方筛选变化。「标记已支付」仅在支付渠道掉单、但款项实际到账时使用，备注必须写清到账依据，方便日后对账。
                    </p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                    刷新
                </Button>
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
                    <span className="admin-metric-label">近 30 天收款金额</span>
                    <span className="admin-metric-value">{formatMoneyFen(revenue?.paidAmountFen ?? 0)}</span>
                    <span className="admin-metric-note">退款 {formatMoneyFen(revenue?.refundedFen ?? 0)}</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">已支付订单数</span>
                    <span className="admin-metric-value">{formatCount(revenue?.paidOrders ?? 0)}</span>
                    <span className="admin-metric-note">全量口径</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">待支付订单数</span>
                    <span className="admin-metric-value">{formatCount(revenue?.pendingOrders ?? 0)}</span>
                    <span className="admin-metric-note">等待用户付款或渠道回调</span>
                </div>
            </div>

            <div className="admin-toolbar">
                <Select
                    value={status}
                    options={statusOptions}
                    style={{ width: 150 }}
                    onChange={(value) => {
                        setPage(1);
                        setStatus(value);
                    }}
                />
                <Input
                    allowClear
                    value={keywordInput}
                    placeholder="订单号 / 用户邮箱 / 手机号"
                    style={{ width: 260 }}
                    onChange={(event) => setKeywordInput(event.target.value)}
                    onPressEnter={() => {
                        setPage(1);
                        setKeyword(keywordInput.trim());
                    }}
                />
                <Input
                    allowClear
                    value={planCode}
                    placeholder="套餐 code"
                    style={{ width: 180 }}
                    onChange={(event) => {
                        setPage(1);
                        setPlanCode(event.target.value.trim());
                    }}
                />
                <Button
                    icon={<Search className="size-3.5" />}
                    onClick={() => {
                        setPage(1);
                        setKeyword(keywordInput.trim());
                    }}
                >
                    查询
                </Button>
                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {formatCount(total)} 条</span>
            </div>

            <div className="admin-card">
                <Table<AdminBillingOrder>
                    rowKey="id"
                    size="small"
                    loading={loading}
                    dataSource={orders}
                    columns={columns}
                    scroll={{ x: 1560 }}
                    pagination={{
                        current: page,
                        pageSize,
                        total,
                        size: "small",
                        showSizeChanger: true,
                        showTotal: (value) => `共 ${value} 条订单`,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setPageSize(nextSize);
                        },
                    }}
                />
            </div>

            <Collapse
                items={[
                    {
                        key: "payment-channels",
                        label: (
                            <span className="admin-settings-inline">
                                <CreditCard className="size-3.5" />
                                <b>支付渠道</b>
                                <span className="admin-user-sub">密钥只写不读，留空保存表示保持原值</span>
                            </span>
                        ),
                        children: (
                            <div className="flex flex-col gap-3">
                                {channelError ? (
                                    <div className="admin-notice is-error">
                                        <span>{channelError}</span>
                                    </div>
                                ) : null}
                                {channelNotice ? (
                                    <div className="admin-notice is-ok">
                                        <span>{channelNotice}</span>
                                    </div>
                                ) : null}
                                {channelItems.length === 0 ? (
                                    <span className="admin-user-sub">暂无可配置的支付渠道。</span>
                                ) : (
                                    <Collapse items={channelItems} />
                                )}
                            </div>
                        ),
                    },
                ]}
            />

            <Modal
                open={markPaidTarget !== null}
                title={markPaidTarget ? `标记已支付 · ${markPaidTarget.orderNo}` : "标记已支付"}
                okText="确认标记已支付"
                cancelText="取消"
                confirmLoading={busyId === markPaidTarget?.id}
                onOk={() => markPaidForm.submit()}
                onCancel={() => {
                    setMarkPaidTarget(null);
                    markPaidForm.resetFields();
                }}
            >
                <Form form={markPaidForm} layout="vertical" onFinish={(values) => void submitMarkPaid(values)}>
                    <Form.Item
                        label="补单备注"
                        name="remark"
                        rules={[{ required: true, message: "请填写到账依据，便于对账追溯" }]}
                        extra="仅用于渠道掉单时按真实到账补单：请写清渠道流水号或到账时间。"
                    >
                        <Input.TextArea rows={3} placeholder="例如：微信商户后台流水 4200…，2026-09-28 10:12 到账" />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={refundTarget !== null}
                title={refundTarget ? `退款 · ${refundTarget.orderNo}` : "退款"}
                okText="确认退款"
                okButtonProps={{ danger: true }}
                cancelText="取消"
                confirmLoading={busyId === refundTarget?.id}
                onOk={() => refundForm.submit()}
                onCancel={() => {
                    setRefundTarget(null);
                    refundForm.resetFields();
                }}
            >
                <Form form={refundForm} layout="vertical" onFinish={(values) => void submitRefund(values)}>
                    <Form.Item label="退款原因" name="reason" rules={[{ required: true, message: "请填写退款原因" }]} extra="退款不可撤销，原因会写入订单备注供对账追溯。">
                        <Input.TextArea rows={3} placeholder="例如：用户申请退订" />
                    </Form.Item>
                </Form>
            </Modal>
        </div>
    );
}
