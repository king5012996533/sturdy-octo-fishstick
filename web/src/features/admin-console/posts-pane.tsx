import { Button, Input, Modal, Table, Tag, type TableProps } from "antd";
import { CheckCheck, RefreshCw, ShieldCheck, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import { approveAdminCreationPost, listAdminCreationPosts, rejectAdminCreationPost, type AdminCreationPost } from "./api-posts";

const modeMeta: Record<string, { label: string; color: string }> = {
    video: { label: "视频", color: "geekblue" },
    image: { label: "图片", color: "magenta" },
    text: { label: "文本", color: "cyan" },
};

/**
 * 投稿审核：用户投到灵感广场的作品在这里逐条裁决。
 *
 * 队列只装"还能改主意"的条目：通过即上架、驳回即留下理由并保持下架，两个结论都会
 * 立刻反映到投稿人自己的「我的投稿」里，所以驳回必须写清理由——一句默认的"不符合规范"
 * 对投稿人等于没有信息，下一版只会再撞一次。
 *
 * 关键词预筛命中的条目不会进这个队列，它们在投稿那一刻就已经判掉了；这里看到的
 * 都是需要人来判断的边界内容。
 */
export function PostsPane() {
    const [posts, setPosts] = useState<AdminCreationPost[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [busyId, setBusyId] = useState("");
    const [rejectTarget, setRejectTarget] = useState<AdminCreationPost | null>(null);
    const [rejectNote, setRejectNote] = useState("");
    const [rejecting, setRejecting] = useState(false);
    const [rejectError, setRejectError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await listAdminCreationPosts();
            setPosts(payload.posts ?? []);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载投稿队列失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const approve = async (post: AdminCreationPost) => {
        setBusyId(post.id);
        setError("");
        setNotice("");
        try {
            await approveAdminCreationPost(post.id);
            setNotice(`投稿「${post.title}」已通过，现在出现在精选灵感广场里。`);
            await load();
        } catch (approveError) {
            setError(approveError instanceof Error ? approveError.message : "通过投稿失败");
        } finally {
            setBusyId("");
        }
    };

    const confirmReject = async () => {
        if (!rejectTarget) return;
        setRejecting(true);
        setRejectError("");
        try {
            await rejectAdminCreationPost(rejectTarget.id, rejectNote.trim());
            setNotice(`投稿「${rejectTarget.title}」已驳回，理由会回显给投稿人。`);
            setRejectTarget(null);
            setRejectNote("");
            await load();
        } catch (rejectFailure) {
            // 驳回失败时把原因留在弹窗里，运营才能当场决定重试还是先处理别的。
            setRejectError(rejectFailure instanceof Error ? rejectFailure.message : "驳回投稿失败");
        } finally {
            setRejecting(false);
        }
    };

    const columns: TableProps<AdminCreationPost>["columns"] = [
        {
            title: "作品",
            key: "title",
            width: 320,
            render: (_, row) => (
                <span className="admin-post-cell">
                    {row.coverUrl ? <img className="admin-post-cover" src={row.coverUrl} alt="" loading="lazy" /> : <span className="admin-post-cover is-empty" aria-hidden />}
                    <span className="admin-user-cell">
                        <span className="admin-user-name">{row.title}</span>
                        <span className="admin-user-sub admin-inspiration-desc">{row.description || row.prompt}</span>
                    </span>
                </span>
            ),
        },
        {
            title: "模式",
            dataIndex: "mode",
            key: "mode",
            width: 80,
            render: (value: string) => {
                const meta = modeMeta[value];
                return meta ? <Tag color={meta.color}>{meta.label}</Tag> : <Tag>{value}</Tag>;
            },
        },
        { title: "分类", dataIndex: "category", key: "category", width: 120, render: (value: string) => value || "精选" },
        { title: "投稿人", dataIndex: "author", key: "author", width: 140, render: (value: string) => <span className="admin-user-sub">{value || "匿名"}</span> },
        {
            title: "提交时间",
            dataIndex: "createdAt",
            key: "createdAt",
            width: 170,
            render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span>,
        },
        {
            title: "操作",
            key: "actions",
            width: 170,
            render: (_, row) => (
                <div className="admin-settings-inline">
                    <Button size="small" type="text" icon={<CheckCheck className="size-3.5" />} loading={busyId === row.id} onClick={() => void approve(row)}>
                        通过
                    </Button>
                    <Button
                        size="small"
                        type="text"
                        danger
                        icon={<X className="size-3.5" />}
                        onClick={() => {
                            setRejectNote("");
                            setRejectError("");
                            setRejectTarget(row);
                        }}
                    >
                        驳回
                    </Button>
                </div>
            ),
        },
    ];

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">投稿审核</h2>
                    <p className="admin-section-desc">用户发布到灵感广场的作品队列。通过即上架，其他用户可以一键复用它的提示词；驳回必须写理由，理由会原文回显给投稿人。关键词预检命中的投稿在提交那一刻已判掉，不会出现在这里。</p>
                </div>
                <div className="admin-settings-inline">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>
                        刷新
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
                        <ShieldCheck className="size-4" />
                        <b style={{ fontSize: "var(--fs-body)" }}>待人队列（先到先审）</b>
                    </span>
                    <span className="admin-user-sub">共 {formatCount(posts.length)} 条</span>
                </div>
                <Table<AdminCreationPost> rowKey="id" size="small" loading={loading} dataSource={posts} columns={columns} scroll={{ x: 1000 }} pagination={false} locale={{ emptyText: "队列是空的：没有等待人工审核的投稿。" }} />
            </div>

            <Modal
                open={Boolean(rejectTarget)}
                title={rejectTarget ? `驳回投稿 · ${rejectTarget.title}` : "驳回投稿"}
                okText="确认驳回"
                cancelText="取消"
                okButtonProps={{ danger: true }}
                confirmLoading={rejecting}
                onOk={() => void confirmReject()}
                onCancel={() => {
                    setRejectTarget(null);
                    setRejectNote("");
                    setRejectError("");
                }}
            >
                <div className="flex flex-col gap-3">
                    <p className="admin-user-sub">理由会原文回显给投稿人，请写清是哪一部分不合适，不要只写“不符合规范”。</p>
                    <Input.TextArea value={rejectNote} autoSize={{ minRows: 3, maxRows: 6 }} maxLength={200} placeholder="例如：画面含真实人物肖像但没有授权说明" onChange={(event) => setRejectNote(event.target.value)} />
                    {rejectError ? <span className="admin-notice is-error">{rejectError}</span> : null}
                </div>
            </Modal>
        </div>
    );
}
