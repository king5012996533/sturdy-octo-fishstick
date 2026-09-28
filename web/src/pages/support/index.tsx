import { Button, Drawer, Input, Select, Tag } from "antd";
import { LifeBuoy, MessageSquare, RefreshCw, Send } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { PageHeader, PaginationBar, WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceLoadingState } from "@/components/layout/workspace-state";
import { Callout, type CalloutTone } from "@/components/ui/product/callout";
import { formatCount, formatDateTime } from "@/lib/format-usage";
import { cn } from "@/lib/utils";
import { createTicket, getTicket, listMyTickets, replyTicket, type SupportTicket, type SupportTicketCategory, type SupportTicketStatus } from "@/services/api/support";

/**
 * 用户端「工单与反馈」页：提交工单 → 在列表里跟踪状态 → 打开对话继续补充。
 *
 * 这一页只做编排与展示：分类、字数与状态流转全部由服务端裁决，前端不做任何"看起来
 * 合法就放行"的判断。全局 message 在本项目里是关闭的，所有反馈一律走页面内 Callout。
 */

const statusViews: Record<SupportTicketStatus, { label: string; color: string; hint: string }> = {
    OPEN: { label: "待处理", color: "gold", hint: "已提交，等待客服跟进" },
    PROCESSING: { label: "处理中", color: "blue", hint: "客服已回复" },
    RESOLVED: { label: "已解决", color: "green", hint: "客服已给出结论" },
    CLOSED: { label: "已关闭", color: "default", hint: "已关闭，不能再回复" },
};

const categoryLabels: Record<SupportTicketCategory, string> = {
    BUG: "功能异常",
    BILLING: "计费与订单",
    FEATURE: "功能建议",
    OTHER: "其他",
};

const categoryOptions: Array<{ value: SupportTicketCategory; label: string }> = [
    { value: "BUG", label: "功能异常" },
    { value: "BILLING", label: "计费与订单" },
    { value: "FEATURE", label: "功能建议" },
    { value: "OTHER", label: "其他" },
];

/* 服务端返回的中文文案要原样透出，只有拿不到文案时才回退到本地兜底。 */
function errorMessage(error: unknown, fallback: string) {
    return error instanceof Error && error.message ? error.message : fallback;
}

export function SupportPage() {
    const [category, setCategory] = useState<SupportTicketCategory>("BUG");
    const [title, setTitle] = useState("");
    const [contact, setContact] = useState("");
    const [body, setBody] = useState("");
    const [submitting, setSubmitting] = useState(false);
    const [notice, setNotice] = useState<{ tone: CalloutTone; text: string } | null>(null);

    const [tickets, setTickets] = useState<SupportTicket[]>([]);
    const [total, setTotal] = useState(0);
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(10);
    const [listLoading, setListLoading] = useState(true);
    const [listError, setListError] = useState("");
    const [reloadKey, setReloadKey] = useState(0);

    const [detail, setDetail] = useState<SupportTicket | null>(null);
    const [detailLoading, setDetailLoading] = useState(false);
    const [detailError, setDetailError] = useState("");
    const [replyBody, setReplyBody] = useState("");
    const [replyBusy, setReplyBusy] = useState(false);

    useEffect(() => {
        let cancelled = false;
        setListLoading(true);
        setListError("");
        listMyTickets({ page, pageSize })
            .then((payload) => {
                if (cancelled) return;
                setTickets(payload.tickets ?? []);
                setTotal(payload.total ?? 0);
            })
            .catch((error) => {
                if (cancelled) return;
                setTickets([]);
                setTotal(0);
                setListError(errorMessage(error, "工单加载失败"));
            })
            .finally(() => {
                if (!cancelled) setListLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [page, pageSize, reloadKey]);

    const reload = useCallback(() => setReloadKey((value) => value + 1), []);

    const openDetail = async (ticket: SupportTicket) => {
        setDetail(ticket);
        setReplyBody("");
        setDetailError("");
        setDetailLoading(true);
        try {
            // 列表不带对话流，展开时拉一次详情，确保读到的是最新回复。
            setDetail(await getTicket(ticket.id));
        } catch (error) {
            setDetailError(errorMessage(error, "工单详情加载失败"));
        } finally {
            setDetailLoading(false);
        }
    };

    const submitTicket = async () => {
        if (submitting) return;
        if (!title.trim()) {
            setNotice({ tone: "error", text: "请填写问题标题。" });
            return;
        }
        if (!body.trim()) {
            setNotice({ tone: "error", text: "请填写问题描述。" });
            return;
        }
        setSubmitting(true);
        setNotice(null);
        try {
            const created = await createTicket({ category, title: title.trim(), body: body.trim(), contact: contact.trim() || undefined });
            setTitle("");
            setBody("");
            setContact("");
            setNotice({ tone: "success", text: `工单已提交，工单号 ${created.ticketNo}。我们会尽快跟进。` });
            setPage(1);
            reload();
            await openDetail(created);
        } catch (error) {
            setNotice({ tone: "error", text: errorMessage(error, "提交工单失败，请稍后重试。") });
        } finally {
            setSubmitting(false);
        }
    };

    const submitReply = async () => {
        if (!detail || replyBusy) return;
        const content = replyBody.trim();
        if (!content) {
            setDetailError("回复内容不能为空。");
            return;
        }
        setReplyBusy(true);
        setDetailError("");
        try {
            const updated = await replyTicket(detail.id, content);
            setDetail(updated);
            setReplyBody("");
            setNotice({ tone: "success", text: "已发送，客服会在看到后回复你。" });
            reload();
        } catch (error) {
            setDetailError(errorMessage(error, "回复失败，请稍后重试。"));
        } finally {
            setReplyBusy(false);
        }
    };

    const canReply = detail !== null && detail.status !== "CLOSED";

    return (
        <WorkspacePage>
            <PageHeader title="工单与反馈" description="遇到问题或想提建议，提交工单后在这里跟踪处理进度" />

            {/* 全局 message 在本项目里是关闭的，所有反馈都必须落在页面内。 */}
            {notice ? (
                <Callout tone={notice.tone} className="mt-3" onClose={() => setNotice(null)}>
                    {notice.text}
                </Callout>
            ) : null}

            <section className="mt-4 rounded-[var(--r-xl)] border border-border bg-surface p-4" aria-label="提交工单">
                <div className="flex items-center gap-2">
                    <LifeBuoy className="size-4 text-foreground/60" />
                    <h2 className="text-[var(--fs-heading)] font-semibold text-foreground">提交工单</h2>
                </div>
                <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <label className="flex flex-col gap-1">
                        <span className="text-[var(--fs-label)] text-foreground/58">问题分类</span>
                        <Select value={category} options={categoryOptions} onChange={(value) => setCategory(value)} />
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-[var(--fs-label)] text-foreground/58">联系方式（可选）</span>
                        <Input value={contact} maxLength={120} placeholder="邮箱或手机号，便于客服在账号之外联系你" onChange={(event) => setContact(event.target.value)} />
                    </label>
                </div>
                <label className="mt-3 flex flex-col gap-1">
                    <span className="text-[var(--fs-label)] text-foreground/58">标题</span>
                    <Input value={title} maxLength={80} showCount placeholder="一句话描述问题" onChange={(event) => setTitle(event.target.value)} />
                </label>
                <label className="mt-3 flex flex-col gap-1">
                    <span className="text-[var(--fs-label)] text-foreground/58">问题描述</span>
                    <Input.TextArea value={body} rows={5} maxLength={2000} showCount placeholder="发生了什么、在哪个页面、什么时候开始的，写清楚有助于我们更快定位" onChange={(event) => setBody(event.target.value)} />
                </label>
                <div className="mt-3 flex items-center gap-2">
                    <Button type="primary" icon={<Send className="size-3.5" />} loading={submitting} onClick={() => void submitTicket()}>
                        提交工单
                    </Button>
                    <span className="text-[var(--fs-label)] text-foreground/50">提交后可在下方列表里跟踪状态并追加回复。</span>
                </div>
            </section>

            <section className="mt-6" aria-label="我的工单">
                <div className="flex items-center justify-between gap-2">
                    <h2 className="text-[var(--fs-heading)] font-semibold text-foreground">我的工单</h2>
                    <Button icon={<RefreshCw className="size-3.5" />} loading={listLoading} onClick={reload}>
                        刷新
                    </Button>
                </div>

                {listError ? (
                    <Callout tone="error" className="mt-3" onClose={() => setListError("")}>
                        {listError}
                    </Callout>
                ) : null}

                {listLoading && !tickets.length ? <WorkspaceLoadingState label="正在加载工单" detail="读取你提交过的反馈" rows={2} /> : null}

                {!listLoading && !tickets.length ? (
                    <p className="mt-3 rounded-[var(--r-lg)] border border-border bg-surface-secondary px-3.5 py-3 text-[var(--fs-caption)] text-foreground/58">
                        还没有提交过工单。遇到问题可以在上方表单里描述，我们会尽快处理。
                    </p>
                ) : null}

                {tickets.length ? (
                    <div className="mt-3 flex flex-col gap-2">
                        {tickets.map((ticket) => {
                            const status = statusViews[ticket.status];
                            return (
                                <button
                                    type="button"
                                    key={ticket.id}
                                    className={cn(
                                        "flex flex-col gap-2 rounded-[var(--r-lg)] border bg-surface px-3.5 py-3 text-left transition-colors sm:flex-row sm:items-center sm:justify-between",
                                        detail?.id === ticket.id ? "border-foreground/45" : "border-border hover:border-foreground/30",
                                    )}
                                    onClick={() => void openDetail(ticket)}
                                >
                                    <div className="min-w-0">
                                        <div className="flex flex-wrap items-center gap-2">
                                            <span className="font-mono text-[var(--fs-caption)] text-foreground">{ticket.ticketNo}</span>
                                            <Tag className="m-0" color={status.color}>
                                                {status.label}
                                            </Tag>
                                            <Tag className="m-0">{categoryLabels[ticket.category] ?? ticket.category}</Tag>
                                        </div>
                                        <p className="mt-1 truncate text-[var(--fs-body)] text-foreground">{ticket.title}</p>
                                        <p className="mt-0.5 text-[var(--fs-tiny)] text-foreground/45">
                                            提交 {formatDateTime(ticket.createdAt)} · 更新 {formatDateTime(ticket.updatedAt)} · {status.hint}
                                        </p>
                                    </div>
                                    <span className="flex shrink-0 items-center gap-1 text-[var(--fs-label)] text-foreground/60">
                                        <MessageSquare className="size-3.5" />
                                        {formatCount(ticket.replies.length)} 条回复
                                    </span>
                                </button>
                            );
                        })}
                    </div>
                ) : null}

                <PaginationBar
                    current={page}
                    pageSize={pageSize}
                    total={total}
                    pageSizeOptions={[10, 20, 50]}
                    onChange={(nextPage, nextPageSize) => {
                        setPage(nextPageSize !== pageSize ? 1 : nextPage);
                        setPageSize(nextPageSize);
                    }}
                />
            </section>

            <Drawer
                open={detail !== null}
                size={640}
                title={detail ? `工单 ${detail.ticketNo}` : "工单详情"}
                onClose={() => {
                    setDetail(null);
                    setDetailError("");
                    setReplyBody("");
                }}
            >
                {detail ? (
                    <div className="flex flex-col gap-3">
                        {detailError ? (
                            <Callout tone="error" onClose={() => setDetailError("")}>
                                {detailError}
                            </Callout>
                        ) : null}
                        <div className="flex flex-wrap items-center gap-2">
                            <Tag className="m-0" color={statusViews[detail.status].color}>
                                {statusViews[detail.status].label}
                            </Tag>
                            <Tag className="m-0">{categoryLabels[detail.category] ?? detail.category}</Tag>
                            <span className="text-[var(--fs-label)] text-foreground/50">{statusViews[detail.status].hint}</span>
                        </div>
                        <div className="rounded-[var(--r-lg)] bg-surface-secondary px-3.5 py-3">
                            <h3 className="text-[var(--fs-body-lg)] font-semibold text-foreground">{detail.title}</h3>
                            <p className="mt-1 whitespace-pre-wrap text-[var(--fs-caption)] leading-6 text-foreground/75">{detail.body}</p>
                            <p className="mt-2 text-[var(--fs-tiny)] text-foreground/45">
                                提交 {formatDateTime(detail.createdAt)}
                                {detail.contact ? ` · 联系方式 ${detail.contact}` : ""}
                            </p>
                        </div>

                        <div className="flex flex-col gap-2">
                            <span className="text-[var(--fs-label)] text-foreground/58">对话记录</span>
                            {detail.replies.length === 0 ? <p className="text-[var(--fs-caption)] text-foreground/50">还没有回复，补充信息后客服会尽快跟进。</p> : null}
                            {detail.replies.map((reply) => (
                                <div
                                    key={reply.id}
                                    className={cn(
                                        "rounded-[var(--r-lg)] border px-3 py-2.5",
                                        reply.authorRole === "STAFF" ? "border-border bg-surface-secondary" : "border-border bg-surface",
                                    )}
                                >
                                    <div className="flex items-center justify-between gap-2">
                                        <span className="text-[var(--fs-label)] font-medium text-foreground">
                                            {reply.authorRole === "STAFF" ? reply.authorName || "客服" : "我"}
                                        </span>
                                        <span className="text-[var(--fs-tiny)] text-foreground/45">{formatDateTime(reply.createdAt)}</span>
                                    </div>
                                    <p className="mt-1 whitespace-pre-wrap text-[var(--fs-caption)] leading-6 text-foreground/75">{reply.body}</p>
                                </div>
                            ))}
                        </div>

                        {canReply ? (
                            <div className="flex flex-col gap-2">
                                <Input.TextArea value={replyBody} rows={3} maxLength={2000} showCount placeholder="补充信息或追问客服" onChange={(event) => setReplyBody(event.target.value)} />
                                <div className="flex items-center gap-2">
                                    <Button type="primary" icon={<Send className="size-3.5" />} loading={replyBusy} onClick={() => void submitReply()}>
                                        发送
                                    </Button>
                                    {detailLoading ? <span className="text-[var(--fs-label)] text-foreground/45">正在刷新…</span> : null}
                                </div>
                            </div>
                        ) : (
                            <Callout tone="info">工单已关闭，不能继续回复。如需继续沟通，请重新提交一张工单。</Callout>
                        )}
                    </div>
                ) : null}
            </Drawer>
        </WorkspacePage>
    );
}
