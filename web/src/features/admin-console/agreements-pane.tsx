import { Button, Input, Modal, Select, Table, Tabs, Tag, type TableProps } from "antd";
import { FileSignature, History, RefreshCw, Save, ShieldAlert } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import {
    getAdminAgreements,
    listAdminAgreementSignatures,
    publishAdminAgreements,
    type AdminAgreementDocument,
    type AdminAgreementSignature,
    type AdminAgreements,
    type AdminAgreementVersion,
} from "./api";

const agreementLabels: Record<string, string> = { TERMS: "用户协议", PRIVACY: "隐私政策" };

const signatureTypeOptions = [
    { value: "", label: "全部协议" },
    { value: "TERMS", label: "用户协议" },
    { value: "PRIVACY", label: "隐私政策" },
];

type Draft = {
    termsTitle: string;
    termsBody: string;
    privacyTitle: string;
    privacyBody: string;
};

function draftOf(agreements: AdminAgreements | null): Draft {
    const terms = agreements?.documents.find((document) => document.type === "TERMS");
    const privacy = agreements?.documents.find((document) => document.type === "PRIVACY");
    return {
        termsTitle: terms?.title ?? "用户协议",
        termsBody: terms?.body ?? "",
        privacyTitle: privacy?.title ?? "隐私政策",
        privacyBody: privacy?.body ?? "",
    };
}

function DocumentTabs({ documents }: { documents: AdminAgreementDocument[] }) {
    return (
        <Tabs
            size="small"
            items={documents.map((document) => ({
                key: document.type,
                label: document.title,
                children: <pre className="admin-agreement-body">{document.body}</pre>,
            }))}
        />
    );
}

/**
 * 协议管理。
 *
 * 关键语义写在这里，避免运营误操作：发布一版新的协议 = 全站强制重签——所有账号在
 * 前台都会被要求重新同意一次，因此发布按钮要二次确认并先亮出影响面。
 */
export function AgreementsPane() {
    const [agreements, setAgreements] = useState<AdminAgreements | null>(null);
    const [draft, setDraft] = useState<Draft>(draftOf(null));
    const [dirty, setDirty] = useState(false);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [confirmOpen, setConfirmOpen] = useState(false);
    const [historyTarget, setHistoryTarget] = useState<AdminAgreementVersion | null>(null);

    const [signatures, setSignatures] = useState<AdminAgreementSignature[]>([]);
    const [signatureTotal, setSignatureTotal] = useState(0);
    const [signaturePage, setSignaturePage] = useState(1);
    const [signaturePageSize, setSignaturePageSize] = useState(20);
    const [signatureType, setSignatureType] = useState("");
    const [signatureVersion, setSignatureVersion] = useState("");
    const [signatureKeywordInput, setSignatureKeywordInput] = useState("");
    const [signatureKeyword, setSignatureKeyword] = useState("");
    const [signaturesLoading, setSignaturesLoading] = useState(false);

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await getAdminAgreements();
            setAgreements(payload);
            // 正在编辑的内容不覆盖：后台刷新不该把运营写到一半的正文冲掉。
            setDraft((current) => (dirty ? current : draftOf(payload)));
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载协议失败");
        } finally {
            setLoading(false);
        }
    }, [dirty]);

    useEffect(() => {
        void load();
    }, [load]);

    const loadSignatures = useCallback(async (options: { page: number; pageSize: number; type: string; version: string; keyword: string }) => {
        setSignaturesLoading(true);
        try {
            const payload = await listAdminAgreementSignatures({
                page: options.page,
                pageSize: options.pageSize,
                type: options.type,
                version: options.version,
                keyword: options.keyword,
            });
            setSignatures(payload.signatures ?? []);
            setSignatureTotal(payload.total ?? 0);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载签署记录失败");
        } finally {
            setSignaturesLoading(false);
        }
    }, []);

    useEffect(() => {
        void loadSignatures({ page: signaturePage, pageSize: signaturePageSize, type: signatureType, version: signatureVersion, keyword: signatureKeyword });
    }, [loadSignatures, signaturePage, signaturePageSize, signatureType, signatureVersion, signatureKeyword]);

    // 输入即查询会把每一次按键都变成一次列表请求，这里做 300ms 防抖。
    useEffect(() => {
        const timer = setTimeout(() => {
            setSignaturePage(1);
            setSignatureKeyword(signatureKeywordInput.trim());
        }, 300);
        return () => clearTimeout(timer);
    }, [signatureKeywordInput]);

    const versionOptions = useMemo(() => {
        const items = [{ value: "", label: "全部版本" }];
        for (const item of agreements?.history ?? []) {
            items.push({ value: item.version, label: item.current ? `${item.version}（当前）` : item.version });
        }
        return items;
    }, [agreements]);

    const updateDraft = (patch: Partial<Draft>) => {
        setDraft((current) => ({ ...current, ...patch }));
        setDirty(true);
    };

    const publish = async () => {
        setSaving(true);
        setError("");
        setNotice("");
        try {
            const payload = await publishAdminAgreements(draft);
            setAgreements(payload);
            setDraft(draftOf(payload));
            setDirty(false);
            setConfirmOpen(false);
            setNotice(`已发布版本 ${payload.version}，全部账号需在下次进入时重新同意`);
        } catch (publishError) {
            setError(publishError instanceof Error ? publishError.message : "发布失败");
        } finally {
            setSaving(false);
        }
    };

    const signatureColumns: TableProps<AdminAgreementSignature>["columns"] = [
        {
            title: "账号",
            key: "account",
            render: (_, row) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-name">{row.name || row.email || row.phone || row.userId}</span>
                    <span className="admin-user-sub">{row.email || row.phone || row.userId}</span>
                </div>
            ),
        },
        { title: "协议", key: "agreementType", width: 120, render: (_, row) => <Tag>{agreementLabels[row.agreementType] ?? row.agreementType}</Tag> },
        { title: "版本", dataIndex: "version", key: "version", width: 120, render: (value: string) => <span className="admin-user-sub">{value}</span> },
        { title: "同意时间", dataIndex: "acceptedAt", key: "acceptedAt", width: 168, render: (value: string) => formatDateTime(value) },
        { title: "来源 IP", dataIndex: "ipAddress", key: "ipAddress", width: 140, render: (value: string) => <span className="admin-user-sub">{value || "—"}</span> },
        {
            title: "User-Agent",
            dataIndex: "userAgent",
            key: "userAgent",
            ellipsis: true,
            render: (value: string) => <span className="admin-user-sub">{value || "—"}</span>,
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">协议管理</h2>
                    <p className="admin-section-desc">用户协议与隐私政策的正文、版本与签署留痕。发布新版本会让全部账号重新同意一次，签署记录不可修改。</p>
                </div>
                <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                    刷新
                </Button>
            </div>

            {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}
            {notice ? <div className="admin-notice is-ok"><span>{notice}</span></div> : null}
            {agreements && !agreements.configured ? (
                <div className="admin-notice">
                    <ShieldAlert className="size-3.5 shrink-0" />
                    <span>当前用的是内置骨架正文（含【】占位），未经法务确认不要对外开放注册。</span>
                </div>
            ) : null}

            <div className="admin-metric-grid">
                <div className="admin-metric">
                    <span className="admin-metric-label">当前版本</span>
                    <span className="admin-metric-value">{agreements?.version ?? "—"}</span>
                    <span className="admin-metric-hint">{agreements?.publishedAt ? `发布于 ${formatDateTime(agreements.publishedAt)}` : "内置骨架版本，尚未发布"}</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">待重新同意</span>
                    <span className="admin-metric-value">{formatCount(agreements?.pendingUsers ?? 0)}</span>
                    <span className="admin-metric-hint">尚未接受当前版本的账号数</span>
                </div>
                <div className="admin-metric">
                    <span className="admin-metric-label">历史版本</span>
                    <span className="admin-metric-value">{formatCount(agreements?.history.length ?? 0)}</span>
                    <span className="admin-metric-hint">每一次发布都会留档</span>
                </div>
            </div>

            <Tabs
                items={[
                    {
                        key: "editor",
                        label: <span className="flex items-center gap-1"><FileSignature className="size-3.5" />正文与发布</span>,
                        children: (
                            <div className="flex flex-col gap-4">
                                <div className="admin-card admin-agreement-editor">
                                    <label className="admin-agreement-field">
                                        <span>用户协议标题</span>
                                        <Input maxLength={64} value={draft.termsTitle} onChange={(event) => updateDraft({ termsTitle: event.target.value })} />
                                    </label>
                                    <label className="admin-agreement-field">
                                        <span>用户协议正文</span>
                                        <Input.TextArea rows={12} value={draft.termsBody} onChange={(event) => updateDraft({ termsBody: event.target.value })} />
                                    </label>
                                    <label className="admin-agreement-field">
                                        <span>隐私政策标题</span>
                                        <Input maxLength={64} value={draft.privacyTitle} onChange={(event) => updateDraft({ privacyTitle: event.target.value })} />
                                    </label>
                                    <label className="admin-agreement-field">
                                        <span>隐私政策正文</span>
                                        <Input.TextArea rows={12} value={draft.privacyBody} onChange={(event) => updateDraft({ privacyBody: event.target.value })} />
                                    </label>
                                </div>
                                <div className="flex items-center gap-3">
                                    <Button type="primary" icon={<Save className="size-3.5" />} disabled={!dirty} onClick={() => setConfirmOpen(true)}>
                                        发布为新版本
                                    </Button>
                                    <span className="admin-ink-faint" style={{ fontSize: "var(--fs-caption)" }}>
                                        {dirty ? "有未发布的修改" : "与线上一致"}
                                    </span>
                                </div>
                            </div>
                        ),
                    },
                    {
                        key: "history",
                        label: <span className="flex items-center gap-1"><History className="size-3.5" />历史版本</span>,
                        children: (
                            <div className="admin-card">
                                <Table<AdminAgreementVersion>
                                    rowKey="version"
                                    size="small"
                                    loading={loading}
                                    dataSource={agreements?.history ?? []}
                                    pagination={false}
                                    columns={[
                                        { title: "版本", dataIndex: "version", key: "version", width: 160, render: (value: string, row) => <span className="flex items-center gap-2"><span className="admin-user-sub">{value}</span>{row.current ? <Tag color="green">当前</Tag> : null}</span> },
                                        { title: "发布时间", dataIndex: "publishedAt", key: "publishedAt", width: 180, render: (value: string) => formatDateTime(value) },
                                        { title: "发布人", dataIndex: "publishedBy", key: "publishedBy", width: 220, render: (value?: string) => <span className="admin-user-sub">{value || "—"}</span> },
                                        { title: "操作", key: "actions", width: 120, render: (_, row) => <Button size="small" type="text" onClick={() => setHistoryTarget(row)}>查看正文</Button> },
                                    ]}
                                />
                            </div>
                        ),
                    },
                    {
                        key: "signatures",
                        label: <span className="flex items-center gap-1"><ShieldAlert className="size-3.5" />签署记录</span>,
                        children: (
                            <div className="flex flex-col gap-3">
                                <div className="admin-toolbar">
                                    <Input
                                        allowClear
                                        value={signatureKeywordInput}
                                        placeholder="搜索邮箱 / 手机号 / 昵称"
                                        style={{ width: 260 }}
                                        onChange={(event) => setSignatureKeywordInput(event.target.value)}
                                    />
                                    <Select value={signatureType} options={signatureTypeOptions} style={{ width: 140 }} onChange={(value) => { setSignaturePage(1); setSignatureType(value); }} />
                                    <Select value={signatureVersion} options={versionOptions} style={{ width: 170 }} onChange={(value) => { setSignaturePage(1); setSignatureVersion(value); }} />
                                    <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>共 {formatCount(signatureTotal)} 条</span>
                                </div>
                                <div className="admin-card">
                                    <Table<AdminAgreementSignature>
                                        rowKey="id"
                                        size="small"
                                        loading={signaturesLoading}
                                        dataSource={signatures}
                                        columns={signatureColumns}
                                        scroll={{ x: 1080 }}
                                        pagination={{
                                            current: signaturePage,
                                            pageSize: signaturePageSize,
                                            total: signatureTotal,
                                            size: "small",
                                            showSizeChanger: true,
                                            showTotal: (value) => `共 ${value} 条记录`,
                                            onChange: (nextPage, nextSize) => {
                                                setSignaturePage(nextPage);
                                                setSignaturePageSize(nextSize);
                                            },
                                        }}
                                    />
                                </div>
                            </div>
                        ),
                    },
                ]}
            />

            <Modal
                open={confirmOpen}
                title="发布为新版本？"
                okText="确认发布"
                cancelText="取消"
                confirmLoading={saving}
                onOk={() => void publish()}
                onCancel={() => setConfirmOpen(false)}
            >
                <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.8 }}>
                    发布后会生成一个新版本号，正文立即对所有用户生效；
                    <b> 全部 {formatCount(agreements?.pendingUsers ?? 0)} 个尚未同意的账号</b>
                    会在下次进入时被要求重新同意，不同意就无法继续使用。已签署的历史记录不会被修改。
                </p>
            </Modal>

            <Modal
                open={historyTarget !== null}
                width={720}
                title={historyTarget ? `版本 ${historyTarget.version} · ${formatDateTime(historyTarget.publishedAt)}` : "历史版本"}
                footer={<Button onClick={() => setHistoryTarget(null)}>关闭</Button>}
                onCancel={() => setHistoryTarget(null)}
            >
                {historyTarget ? <DocumentTabs documents={historyTarget.documents} /> : null}
            </Modal>
        </div>
    );
}
