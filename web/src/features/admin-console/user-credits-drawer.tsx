import { Button, Drawer, Form, Input, InputNumber, Radio, Select, Table, Tag, type TableProps } from "antd";
import { Coins, RefreshCw, Wallet } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import { adjustAdminCredits, getAdminCreditAccount, isInsufficientCreditsError, listAdminCreditLedger, type AdminCreditKind, type AdminCreditLedgerEntry, type AdminCreditWallet, type AdminUser } from "./api";
import { creditKindOptions, creditKindViews, creditRefLabel, creditSignedInk, formatSignedCredits } from "./credit-presentation";

/** 充值/扣减方向：界面上一律填正数，方向由这里决定符号，避免"再输一次负号"这种坑。 */
type AdjustDirection = "in" | "out";

const quickAmounts = [50, 100, 500, 1000];

export type UserCreditsDrawerProps = {
    /** 目标账号；为 null 表示抽屉关闭。 */
    user: AdminUser | null;
    onClose: () => void;
    /** 余额变动后通知外层刷新列表，让用户管理表格里的余额立刻跟上。 */
    onAdjusted?: (userId: string) => void;
};

/**
 * 用户管理里的积分抽屉。
 *
 * 运营处理"用户说充不上钱"的过程是：找到人 → 看他还剩多少、用掉了多少 → 需要时补一笔。
 * 这三步原来散在两个页面里（用户管理找人不看钱、积分管理看钱不能搜人），中间还要
 * 手工把账号 ID 抄过去。抽屉把它们合成一个动作，抄 ID 这一步就没有了。
 *
 * 余额只在服务端算，这里不本地加减：抽屉显示的永远是一次读取的结果。
 */
export function UserCreditsDrawer({ user, onClose, onAdjusted }: UserCreditsDrawerProps) {
    const userId = user?.id ?? "";
    const [wallet, setWallet] = useState<AdminCreditWallet | null>(null);
    const [walletLoading, setWalletLoading] = useState(false);
    const [walletError, setWalletError] = useState("");

    const [entries, setEntries] = useState<AdminCreditLedgerEntry[]>([]);
    const [ledgerTotal, setLedgerTotal] = useState(0);
    const [ledgerPage, setLedgerPage] = useState(1);
    const [ledgerPageSize, setLedgerPageSize] = useState(10);
    const [ledgerKind, setLedgerKind] = useState("");
    const [ledgerLoading, setLedgerLoading] = useState(false);
    const [ledgerError, setLedgerError] = useState("");

    const [direction, setDirection] = useState<AdjustDirection>("in");
    const [submitting, setSubmitting] = useState(false);
    const [adjustError, setAdjustError] = useState("");
    const [notice, setNotice] = useState("");
    const [form] = Form.useForm<{ amount?: number; note?: string }>();
    const amount = Form.useWatch("amount", form);

    const loadWallet = useCallback(async (targetId: string) => {
        if (!targetId) return;
        setWalletLoading(true);
        setWalletError("");
        try {
            setWallet(await getAdminCreditAccount(targetId));
        } catch (error) {
            setWalletError(error instanceof Error ? error.message : "加载积分账户失败");
        } finally {
            setWalletLoading(false);
        }
    }, []);

    const loadLedger = useCallback(
        async (targetId: string) => {
            if (!targetId) return;
            setLedgerLoading(true);
            setLedgerError("");
            try {
                const payload = await listAdminCreditLedger(targetId, { kind: ledgerKind, page: ledgerPage, pageSize: ledgerPageSize });
                setEntries(payload.entries ?? []);
                setLedgerTotal(payload.total ?? 0);
            } catch (error) {
                setLedgerError(error instanceof Error ? error.message : "加载积分流水失败");
            } finally {
                setLedgerLoading(false);
            }
        },
        [ledgerKind, ledgerPage, ledgerPageSize],
    );

    // 换人即重置：上一个人留下的流水页码和筛选换到新账号上会直接查出一个空列表，
    // 运营看到的是"这个人没有任何流水"，而真实原因是筛选没清。
    useEffect(() => {
        if (!userId) return;
        setWallet(null);
        setLedgerPage(1);
        setLedgerKind("");
        setDirection("in");
        setAdjustError("");
        setNotice("");
        form.resetFields();
        void loadWallet(userId);
    }, [userId, form, loadWallet]);

    useEffect(() => {
        if (!userId) return;
        void loadLedger(userId);
    }, [userId, loadLedger]);

    // 列表行上的余额只是进入抽屉时的快照；抽屉打开期间以自己读到的为准。
    const balance = wallet?.balance ?? user?.credit?.balance ?? 0;

    const submitAdjust = async (values: { amount?: number; note?: string }) => {
        if (!userId) return;
        const magnitude = Math.trunc(Number(values.amount ?? 0));
        const note = (values.note ?? "").trim();
        if (magnitude <= 0) {
            setAdjustError("数量必须是大于 0 的整数，加还是减由上面的方向决定。");
            return;
        }
        const signed = direction === "out" ? -magnitude : magnitude;
        setSubmitting(true);
        setAdjustError("");
        setNotice("");
        try {
            const result = await adjustAdminCredits({ userId, amount: signed, note });
            setNotice(`已${direction === "out" ? "扣减" : "充值"} ${formatCount(magnitude)} 积分，当前余额 ${formatCount(result.wallet.balance)}。`);
            setWallet(result.wallet);
            form.resetFields(["amount"]);
            setDirection("in");
            setLedgerPage(1);
            await loadLedger(userId);
            onAdjusted?.(userId);
        } catch (error) {
            // 402 是"会扣成负数"，不是系统故障：这句话运营能直接转述给用户。
            setAdjustError(isInsufficientCreditsError(error) ? "余额不足，扣减后不能为负数。" : error instanceof Error ? error.message : "调整积分失败");
        } finally {
            setSubmitting(false);
        }
    };

    const ledgerColumns: TableProps<AdminCreditLedgerEntry>["columns"] = [
        {
            title: "时间",
            dataIndex: "createdAt",
            key: "createdAt",
            width: 156,
            render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span>,
        },
        {
            title: "类型",
            dataIndex: "kind",
            key: "kind",
            width: 96,
            render: (kind: AdminCreditKind) => {
                const view = creditKindViews[kind];
                return view ? <Tag color={view.color}>{view.label}</Tag> : <Tag>{kind}</Tag>;
            },
        },
        {
            title: "变动",
            dataIndex: "amount",
            key: "amount",
            width: 96,
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
            width: 110,
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

    const signedPreview = direction === "out" ? -Math.trunc(Number(amount ?? 0)) : Math.trunc(Number(amount ?? 0));
    const previewBalance = balance + (Number.isFinite(signedPreview) ? signedPreview : 0);

    return (
        <Drawer
            open={user !== null}
            size={860}
            title={user ? `积分 · ${user.name || user.username || user.email || user.phone || user.id}` : "积分"}
            onClose={onClose}
            extra={
                <Button
                    size="small"
                    icon={<RefreshCw className="size-3.5" strokeWidth={1.75} />}
                    loading={walletLoading || ledgerLoading}
                    onClick={() => {
                        void loadWallet(userId);
                        void loadLedger(userId);
                    }}
                >
                    刷新
                </Button>
            }
        >
            <div className="flex flex-col gap-4">
                {walletError ? (
                    <div className="admin-notice is-error">
                        <span>{walletError}</span>
                    </div>
                ) : null}
                {notice ? (
                    <div className="admin-notice is-ok">
                        <span>{notice}</span>
                    </div>
                ) : null}

                <div className="admin-meta-grid">
                    <div className="admin-kv">
                        <span className="admin-kv-label">当前余额</span>
                        <span className="admin-kv-value is-mono" style={{ color: balance < 0 ? "#ffb4b4" : undefined }}>
                            {walletLoading && !wallet ? "…" : formatCount(balance)}
                        </span>
                    </div>
                    <div className="admin-kv">
                        <span className="admin-kv-label">累计获得</span>
                        <span className="admin-kv-value is-mono">{formatCount(wallet?.lifetimeIn ?? user?.credit?.lifetimeIn ?? 0)}</span>
                    </div>
                    <div className="admin-kv">
                        <span className="admin-kv-label">累计消耗</span>
                        <span className="admin-kv-value is-mono">{formatCount(wallet?.lifetimeOut ?? user?.credit?.lifetimeOut ?? 0)}</span>
                    </div>
                    <div className="admin-kv">
                        <span className="admin-kv-label">最近变动</span>
                        <span className="admin-kv-value is-mono">{formatDateTime(wallet?.updatedAt ?? user?.credit?.updatedAt)}</span>
                    </div>
                </div>

                <div className="admin-card">
                    <div className="admin-card-head">
                        <span className="flex min-w-0 items-center gap-2">
                            <Wallet className="size-4" strokeWidth={1.75} />
                            <b style={{ fontSize: "var(--fs-body)" }}>充值 / 扣减</b>
                        </span>
                        <span className="admin-user-sub">每笔都会写进流水与审计，原因必填</span>
                    </div>
                    <Form form={form} layout="vertical" className="admin-form-narrow" onFinish={(values) => void submitAdjust(values)}>
                        <Form.Item label="方向">
                            <Radio.Group
                                value={direction}
                                onChange={(event) => setDirection(event.target.value as AdjustDirection)}
                                options={[
                                    { value: "in", label: "充值" },
                                    { value: "out", label: "扣减" },
                                ]}
                                optionType="button"
                            />
                        </Form.Item>
                        <Form.Item
                            label="数量（积分）"
                            name="amount"
                            rules={[
                                { required: true, message: "请填写积分数量" },
                                {
                                    validator: (_, value) =>
                                        Number(value ?? 0) > 0 ? Promise.resolve() : Promise.reject(new Error("数量必须是大于 0 的整数")),
                                },
                            ]}
                            extra={
                                Number(amount ?? 0) > 0
                                    ? `变动后余额：${formatCount(previewBalance)}（${formatSignedCredits(signedPreview)}）`
                                    : "1 分 = 1 积分，不做元/分换算。"
                            }
                        >
                            <InputNumber precision={0} min={1} style={{ width: 200 }} placeholder="例如 500" />
                        </Form.Item>
                        <div className="admin-settings-inline" style={{ marginBottom: 16 }}>
                            {quickAmounts.map((value) => (
                                <Button
                                    key={value}
                                    size="small"
                                    onClick={() => {
                                        setDirection("in");
                                        form.setFieldValue("amount", value);
                                    }}
                                >
                                    +{formatCount(value)}
                                </Button>
                            ))}
                        </div>
                        {direction === "out" && Number(amount ?? 0) > 0 && previewBalance < 0 ? (
                            <div className="admin-notice is-error" style={{ marginBottom: 16 }}>
                                <span>扣减后余额会变成负数，服务端会拒绝这次操作。</span>
                            </div>
                        ) : null}
                        <Form.Item label="原因" name="note" rules={[{ required: true, message: "请填写原因" }]} extra="会原样写进审计和这条流水的说明里，事后只能靠它解释这笔账。">
                            <Input.TextArea rows={2} placeholder="例如：客服补偿 / 活动补发 / 误扣退回" />
                        </Form.Item>
                        {adjustError ? (
                            <div className="admin-notice is-error" style={{ marginBottom: 16 }}>
                                <span>{adjustError}</span>
                            </div>
                        ) : null}
                        <Button type="primary" htmlType="submit" loading={submitting} icon={<Coins className="size-3.5" strokeWidth={1.75} />}>
                            {direction === "out" ? "确认扣减" : "确认充值"}
                        </Button>
                    </Form>
                </div>

                <div className="admin-card">
                    <div className="admin-card-head">
                        <span className="flex min-w-0 items-center gap-2">
                            <Coins className="size-4" strokeWidth={1.75} />
                            <b style={{ fontSize: "var(--fs-body)" }}>积分流水</b>
                        </span>
                        <span className="admin-user-sub">共 {formatCount(ledgerTotal)} 条</span>
                    </div>
                    {ledgerError ? (
                        <div className="admin-notice is-error" style={{ marginBottom: 12 }}>
                            <span>{ledgerError}</span>
                        </div>
                    ) : null}
                    <div className="admin-toolbar" style={{ marginBottom: 12 }}>
                        <Select
                            value={ledgerKind}
                            options={creditKindOptions}
                            style={{ width: 150 }}
                            onChange={(value) => {
                                setLedgerPage(1);
                                setLedgerKind(value);
                            }}
                        />
                    </div>
                    <Table<AdminCreditLedgerEntry>
                        rowKey="id"
                        size="small"
                        loading={ledgerLoading}
                        dataSource={entries}
                        columns={ledgerColumns}
                        scroll={{ x: 620 }}
                        pagination={{
                            current: ledgerPage,
                            pageSize: ledgerPageSize,
                            total: ledgerTotal,
                            size: "small",
                            showSizeChanger: true,
                            pageSizeOptions: [10, 20, 50],
                            showTotal: (value) => `共 ${value} 条流水`,
                            onChange: (nextPage, nextSize) => {
                                setLedgerPage(nextPage);
                                setLedgerPageSize(nextSize);
                            },
                        }}
                    />
                </div>
            </div>
        </Drawer>
    );
}
