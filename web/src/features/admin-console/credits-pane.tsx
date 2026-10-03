import { Button, Drawer, Form, Input, InputNumber, Modal, Select, Table, Tag, type TableProps } from "antd";
import { Coins, Pencil, RefreshCw, Search, Wallet } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import { adjustAdminCredits, isInsufficientCreditsError, listAdminCreditAccounts, listAdminCreditLedger, type AdminCreditAccount, type AdminCreditKind, type AdminCreditLedgerEntry } from "./api";
import { creditKindOptions, creditKindViews, creditNegativeInk, creditRefLabel, creditSignedInk, formatSignedCredits } from "./credit-presentation";

function displayNameOf(account: AdminCreditAccount) {
    return account.name || account.username || account.email || account.phone || account.userId;
}

function identifierOf(account: AdminCreditAccount) {
    return account.email || account.phone || account.username || account.userId;
}

/**
 * 积分管理。
 *
 * 积分是本平台的现金等价物，所以这一页的两条纪律是：余额只由后端算、页面只负责原样
 * 展示；任何一次余额变动都必须能回答"为什么"——手工调整因此强制填写原因，它同时也是
 * 审计里的依据。
 *
 * 流水必须点名账号，所以这里是"先选人、再看账"的两段式：列表给出全站余额排行，
 * 点进某个人才展开他的流水，避免一个没有上下文的"全站流水"页面。
 */
export function CreditsPane() {
    const [accounts, setAccounts] = useState<AdminCreditAccount[]>([]);
    const [total, setTotal] = useState(0);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(20);
    const [keywordInput, setKeywordInput] = useState("");
    const [keyword, setKeyword] = useState("");
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");

    const [ledgerTarget, setLedgerTarget] = useState<AdminCreditAccount | null>(null);
    const [ledgerEntries, setLedgerEntries] = useState<AdminCreditLedgerEntry[]>([]);
    const [ledgerTotal, setLedgerTotal] = useState(0);
    const [ledgerPage, setLedgerPage] = useState(1);
    const [ledgerPageSize, setLedgerPageSize] = useState(20);
    const [ledgerKind, setLedgerKind] = useState("");
    const [ledgerLoading, setLedgerLoading] = useState(false);
    const [ledgerError, setLedgerError] = useState("");

    const [adjustTarget, setAdjustTarget] = useState<AdminCreditAccount | null>(null);
    const [adjusting, setAdjusting] = useState(false);
    const [adjustError, setAdjustError] = useState("");
    const [adjustForm] = Form.useForm<{ amount?: number; note?: string }>();
    const adjustAmount = Form.useWatch("amount", adjustForm);

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminCreditAccounts({ keyword, page, pageSize });
            const rows = payload.accounts ?? [];
            setAccounts(rows);
            setTotal(payload.total ?? 0);
            return rows;
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载积分账户失败");
            return null;
        } finally {
            setLoading(false);
        }
    }, [keyword, page, pageSize]);

    useEffect(() => {
        void load();
    }, [load]);

    const loadLedger = useCallback(
        async (userId: string) => {
            setLedgerLoading(true);
            setLedgerError("");
            try {
                const payload = await listAdminCreditLedger(userId, { kind: ledgerKind, page: ledgerPage, pageSize: ledgerPageSize });
                setLedgerEntries(payload.entries ?? []);
                setLedgerTotal(payload.total ?? 0);
            } catch (loadError) {
                setLedgerError(loadError instanceof Error ? loadError.message : "加载积分流水失败");
            } finally {
                setLedgerLoading(false);
            }
        },
        [ledgerKind, ledgerPage, ledgerPageSize],
    );

    // 依赖里带 ledgerTarget 本身：调整后再把刷新过的账户行写回，流水会自动重取一次，
    // 运营不必手动刷新就能看到刚写进去的那条账。
    useEffect(() => {
        if (ledgerTarget) void loadLedger(ledgerTarget.userId);
    }, [ledgerTarget, loadLedger]);

    const openLedger = useCallback((account: AdminCreditAccount) => {
        setLedgerKind("");
        setLedgerPage(1);
        setLedgerError("");
        setLedgerTarget(account);
    }, []);

    const openAdjust = useCallback(
        (account: AdminCreditAccount) => {
            setAdjustError("");
            setAdjustTarget(account);
            // 弹窗带 destroyOnHidden：每次打开都是新的表单实例，必须显式清一次，
            // 否则上一次的金额与原因会留在下一次的输入框里。
            adjustForm.resetFields();
        },
        [adjustForm],
    );

    const submitAdjust = async (values: { amount?: number; note?: string }) => {
        if (!adjustTarget) return;
        const amount = Math.trunc(Number(values.amount ?? 0));
        const note = (values.note ?? "").trim();
        setAdjusting(true);
        setAdjustError("");
        setNotice("");
        try {
            const result = await adjustAdminCredits({ userId: adjustTarget.userId, amount, note });
            setNotice(`已为 ${displayNameOf(adjustTarget)} 调整 ${formatSignedCredits(amount)} 积分，当前余额 ${formatCount(result.wallet.balance)}。`);
            setAdjustTarget(null);
            adjustForm.resetFields();
            const rows = await load();
            if (rows && ledgerTarget) {
                const refreshed = rows.find((row) => row.userId === ledgerTarget.userId);
                if (refreshed) setLedgerTarget(refreshed);
            }
        } catch (adjustFailure) {
            // 402 是"扣成负数被拒"，不是系统故障：把它翻译成运营能立刻处理的一句人话。
            setAdjustError(isInsufficientCreditsError(adjustFailure) ? "余额不足，无法扣成负数。" : adjustFailure instanceof Error ? adjustFailure.message : "调整积分失败");
        } finally {
            setAdjusting(false);
        }
    };

    const accountColumns: TableProps<AdminCreditAccount>["columns"] = [
        {
            title: "账号",
            key: "account",
            width: 260,
            render: (_, account) => (
                <div className="admin-user-cell">
                    <span className="admin-user-avatar" aria-hidden>
                        {(displayNameOf(account) || "U").slice(0, 1).toUpperCase()}
                    </span>
                    <span className="flex min-w-0 flex-col">
                        <span className="admin-user-name">{displayNameOf(account)}</span>
                        <span className="admin-user-sub">{identifierOf(account)}</span>
                    </span>
                </div>
            ),
        },
        {
            title: "余额（积分）",
            dataIndex: "balance",
            key: "balance",
            width: 140,
            render: (value: number) => (
                <b className="admin-console-mono" style={{ fontSize: "var(--fs-caption)", color: value < 0 ? creditNegativeInk : "var(--admin-ink)" }}>
                    {formatCount(value)}
                </b>
            ),
        },
        {
            title: "累计获得",
            dataIndex: "lifetimeIn",
            key: "lifetimeIn",
            width: 120,
            render: (value: number) => <span className="admin-user-sub">{formatCount(value)}</span>,
        },
        {
            title: "累计消耗",
            dataIndex: "lifetimeOut",
            key: "lifetimeOut",
            width: 120,
            render: (value: number) => <span className="admin-user-sub">{formatCount(value)}</span>,
        },
        {
            title: "最近变动",
            dataIndex: "updatedAt",
            key: "updatedAt",
            width: 180,
            render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span>,
        },
        {
            title: "操作",
            key: "actions",
            width: 190,
            render: (_, account) => (
                <div className="admin-settings-inline">
                    <Button
                        size="small"
                        type="text"
                        icon={<Wallet className="size-3.5" strokeWidth={1.75} />}
                        onClick={(event) => {
                            event.stopPropagation();
                            openLedger(account);
                        }}
                    >
                        流水
                    </Button>
                    <Button
                        size="small"
                        type="text"
                        icon={<Pencil className="size-3.5" strokeWidth={1.75} />}
                        onClick={(event) => {
                            event.stopPropagation();
                            openAdjust(account);
                        }}
                    >
                        调整
                    </Button>
                </div>
            ),
        },
    ];

    const ledgerColumns: TableProps<AdminCreditLedgerEntry>["columns"] = [
        {
            title: "时间",
            dataIndex: "createdAt",
            key: "createdAt",
            width: 170,
            render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span>,
        },
        {
            title: "类型",
            dataIndex: "kind",
            key: "kind",
            width: 104,
            render: (kind: AdminCreditKind) => {
                const view = creditKindViews[kind];
                return view ? <Tag color={view.color}>{view.label}</Tag> : <Tag>{kind}</Tag>;
            },
        },
        {
            title: "变动",
            dataIndex: "amount",
            key: "amount",
            width: 110,
            render: (value: number) => (
                <b className="admin-console-mono" style={{ fontSize: "var(--fs-caption)", color: creditSignedInk(value) }}>
                    {formatSignedCredits(value)}
                </b>
            ),
        },
        {
            title: "变动后余额",
            dataIndex: "balanceAfter",
            key: "balanceAfter",
            width: 120,
            render: (value: number) => <span className="admin-user-sub">{formatCount(value)}</span>,
        },
        {
            title: "说明",
            key: "note",
            render: (_, entry) => {
                const ref = creditRefLabel(entry);
                return (
                    <span className="flex min-w-0 flex-col">
                        <span className="admin-user-name">{entry.note || "—"}</span>
                        {ref ? <span className="admin-user-sub">{ref}</span> : null}
                    </span>
                );
            },
        },
    ];

    const previewBalance = adjustTarget ? adjustTarget.balance + Math.trunc(Number(adjustAmount ?? 0)) : 0;

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">积分管理</h2>
                    <p className="admin-section-desc">默认按余额降序，等于一份全站消耗排行。余额只由服务端结算，这里的人工调整会写审计；扣成负数会被服务端拒绝。</p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" strokeWidth={1.75} />} loading={loading} onClick={() => void load()}>
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

            <div className="admin-toolbar">
                <Input
                    allowClear
                    value={keywordInput}
                    placeholder="用户名 / 昵称 / 邮箱 / 手机号 / 账号 ID"
                    style={{ width: 300 }}
                    onChange={(event) => setKeywordInput(event.target.value)}
                    onPressEnter={() => {
                        setPage(1);
                        setKeyword(keywordInput.trim());
                    }}
                />
                <Button
                    icon={<Search className="size-3.5" strokeWidth={1.75} />}
                    onClick={() => {
                        setPage(1);
                        setKeyword(keywordInput.trim());
                    }}
                >
                    查询
                </Button>
                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {formatCount(total)} 个账户</span>
            </div>

            <div className="admin-card">
                <div className="admin-card-head">
                    <span className="flex min-w-0 items-center gap-2">
                        <Coins className="size-4" strokeWidth={1.75} />
                        <b style={{ fontSize: "var(--fs-body)" }}>积分账户</b>
                    </span>
                    <span className="admin-user-sub">点任意一行查看该账号的流水</span>
                </div>
                <Table<AdminCreditAccount>
                    rowKey="userId"
                    size="small"
                    loading={loading}
                    dataSource={accounts}
                    columns={accountColumns}
                    scroll={{ x: 1010 }}
                    pagination={{
                        current: page,
                        pageSize,
                        total,
                        size: "small",
                        showSizeChanger: true,
                        showTotal: (value) => `共 ${value} 个账户`,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setPageSize(nextSize);
                        },
                    }}
                    onRow={(account) => ({
                        onClick: () => openLedger(account),
                        style: { cursor: "pointer" },
                    })}
                />
            </div>

            <Drawer
                open={ledgerTarget !== null}
                size={900}
                title={ledgerTarget ? `积分流水 · ${displayNameOf(ledgerTarget)}` : "积分流水"}
                onClose={() => setLedgerTarget(null)}
                extra={
                    ledgerTarget ? (
                        <Button size="small" icon={<Pencil className="size-3.5" strokeWidth={1.75} />} onClick={() => openAdjust(ledgerTarget)}>
                            调整积分
                        </Button>
                    ) : null
                }
            >
                <div className="flex flex-col gap-3">
                    <div className="admin-meta-grid">
                        <div className="admin-kv">
                            <span className="admin-kv-label">当前余额</span>
                            <span className="admin-kv-value is-mono">{formatCount(ledgerTarget?.balance ?? 0)}</span>
                        </div>
                        <div className="admin-kv">
                            <span className="admin-kv-label">累计获得</span>
                            <span className="admin-kv-value is-mono">{formatCount(ledgerTarget?.lifetimeIn ?? 0)}</span>
                        </div>
                        <div className="admin-kv">
                            <span className="admin-kv-label">累计消耗</span>
                            <span className="admin-kv-value is-mono">{formatCount(ledgerTarget?.lifetimeOut ?? 0)}</span>
                        </div>
                        <div className="admin-kv">
                            <span className="admin-kv-label">最近变动</span>
                            <span className="admin-kv-value is-mono">{formatDateTime(ledgerTarget?.updatedAt)}</span>
                        </div>
                    </div>

                    {ledgerError ? (
                        <div className="admin-notice is-error">
                            <span>{ledgerError}</span>
                        </div>
                    ) : null}

                    <div className="admin-toolbar">
                        <Select
                            value={ledgerKind}
                            options={creditKindOptions}
                            style={{ width: 150 }}
                            onChange={(value) => {
                                setLedgerPage(1);
                                setLedgerKind(value);
                            }}
                        />
                        <Button
                            icon={<RefreshCw className="size-3.5" strokeWidth={1.75} />}
                            loading={ledgerLoading}
                            onClick={() => {
                                if (ledgerTarget) void loadLedger(ledgerTarget.userId);
                            }}
                        >
                            刷新流水
                        </Button>
                        <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {formatCount(ledgerTotal)} 条</span>
                    </div>

                    <Table<AdminCreditLedgerEntry>
                        rowKey="id"
                        size="small"
                        loading={ledgerLoading}
                        dataSource={ledgerEntries}
                        columns={ledgerColumns}
                        scroll={{ x: 780 }}
                        pagination={{
                            current: ledgerPage,
                            pageSize: ledgerPageSize,
                            total: ledgerTotal,
                            size: "small",
                            showSizeChanger: true,
                            showTotal: (value) => `共 ${value} 条流水`,
                            onChange: (nextPage, nextSize) => {
                                setLedgerPage(nextPage);
                                setLedgerPageSize(nextSize);
                            },
                        }}
                    />
                </div>
            </Drawer>

            <Modal
                open={adjustTarget !== null}
                width={520}
                destroyOnHidden
                title={adjustTarget ? `调整积分 · ${displayNameOf(adjustTarget)}` : "调整积分"}
                okText="确认调整"
                cancelText="取消"
                confirmLoading={adjusting}
                onOk={() => adjustForm.submit()}
                onCancel={() => {
                    setAdjustTarget(null);
                    setAdjustError("");
                }}
            >
                <Form form={adjustForm} layout="vertical" onFinish={(values) => void submitAdjust(values)} className="admin-form-narrow">
                    <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.8 }}>当前余额 {formatCount(adjustTarget?.balance ?? 0)} 积分。正数为增加、负数为扣减，单位是「分（1 分 = 1 积分）」，不做元/分换算。</p>
                    <Form.Item
                        label="调整数量（积分）"
                        name="amount"
                        rules={[
                            { required: true, message: "请填写调整数量" },
                            {
                                validator: (_, value) => (Number(value ?? 0) === 0 ? Promise.reject(new Error("调整数量不能为 0，没有任何变动就不该留下一条账")) : Promise.resolve()),
                            },
                        ]}
                        extra={adjustTarget && Number(adjustAmount ?? 0) !== 0 ? `调整后余额：${formatCount(previewBalance)}（${formatSignedCredits(Number(adjustAmount ?? 0))}）` : "填写非 0 整数，例如 1000 或 -1000。"}
                    >
                        <InputNumber precision={0} style={{ width: 220 }} />
                    </Form.Item>
                    {adjustTarget && Number(adjustAmount ?? 0) !== 0 && previewBalance < 0 ? (
                        <div className="admin-notice is-error" style={{ marginBottom: 16 }}>
                            <span>调整后余额会变成负数，服务端会拒绝这次扣减（余额不足，无法扣成负数）。</span>
                        </div>
                    ) : null}
                    <Form.Item label="调整原因" name="note" rules={[{ required: true, message: "请填写调整原因" }]} extra="会原样写进审计与这条流水的说明里，事后只能靠它解释这笔账。">
                        <Input.TextArea rows={2} placeholder="例如：活动补发 / 客诉补偿 / 误扣退回" />
                    </Form.Item>
                </Form>
            </Modal>
        </div>
    );
}
