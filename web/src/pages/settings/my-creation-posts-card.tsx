import { App, Button, Popconfirm, Tag } from "antd";
import { RefreshCw, Share2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { formatDateTime } from "@/lib/format-usage";
import { ApiError } from "@/services/api/request";
import { listMyCreationPosts, withdrawCreationPost, type CreationPostRecord } from "@/services/api/creation-posts";

/**
 * 「我的投稿」：自己发到灵感广场的作品与它们的审核状态。
 *
 * 这张卡片存在的理由不是"列表好看"，而是审核有等待期：投稿提交后不会立刻上广场，
 * 没有这一页，用户唯一的反馈就是提交那一刻的提示，之后只能靠去广场里翻自己那条
 * 有没有出现来判断结果——被驳回的更是永远等不到答案。
 *
 * 驳回理由原样回显：服务端只说命中哪一类、不回显命中的词，这里也不做二次加工。
 */
const reviewLabels: Record<string, { text: string; color: string }> = {
    PENDING: { text: "待审核", color: "gold" },
    APPROVED: { text: "已通过", color: "green" },
    REJECTED: { text: "未通过", color: "red" },
};

const modeLabels: Record<string, string> = { image: "图像", video: "视频", text: "文本" };

export function MyCreationPostsCard() {
    // 撤回结果走 ConfigProvider 内的 message，深色主题下不至于弹出浅色提示条。
    const { message } = App.useApp();
    const [posts, setPosts] = useState<CreationPostRecord[] | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    // 本地/桌面工作区根本没有投稿接口，404 与 401 都是"这个功能不存在"而不是故障，
    // 与其在设置页摆一张永远读不出数据的卡片，不如整块不出现。
    const [available, setAvailable] = useState(true);

    const load = useCallback(async () => {
        setLoading(true);
        try {
            const { posts: records } = await listMyCreationPosts();
            setPosts(records);
            setError("");
            setAvailable(true);
        } catch (loadError) {
            const status = loadError instanceof ApiError ? loadError.status : undefined;
            if (status === 404 || status === 401 || status === 403) {
                setAvailable(false);
                return;
            }
            setError(loadError instanceof Error ? loadError.message : "读取投稿失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    if (!available) return null;

    const withdraw = async (post: CreationPostRecord) => {
        try {
            const { posts: remaining } = await withdrawCreationPost(post.id);
            setPosts(remaining);
            message.success("已撤回这条投稿");
        } catch (withdrawError) {
            message.error(withdrawError instanceof Error ? withdrawError.message : "撤回失败，请稍后再试");
        }
    };

    return (
        <div className="settings-pane mx-auto mt-4 w-full max-w-2xl">
            <div className="settings-pane-header">
                <div className="min-w-0">
                    <h2>我的投稿</h2>
                    <p>发布到灵感广场的作品，审核通过后会出现在精选灵感里，其他用户可以一键复用它的提示词。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button icon={<RefreshCw className="size-4" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                </div>
            </div>

            {error ? (
                <div className="account-notice is-error">
                    <span>{error}</span>
                </div>
            ) : null}

            <section className="account-block">
                <h3 className="account-block-title">投稿记录</h3>
                {posts && posts.length > 0 ? (
                    <ul className="account-agreements">
                        {posts.map((post) => {
                            const review = reviewLabels[post.reviewStatus] ?? { text: post.reviewStatus || "待审核", color: "default" };
                            return (
                                <li key={post.id} className="flex flex-col gap-1">
                                    <div className="flex items-center gap-2">
                                        <Share2 className="size-3.5" aria-hidden />
                                        <b className="truncate" title={post.title}>
                                            {post.title}
                                        </b>
                                        <Tag color={review.color}>{review.text}</Tag>
                                        <span className="account-sub">{modeLabels[post.mode] ?? post.mode}</span>
                                        <span className="account-sub">{formatDateTime(post.createdAt)}</span>
                                        <Popconfirm title="撤回这条投稿？" description="撤回后它会从广场消失，且不可恢复。" okText="撤回" cancelText="取消" onConfirm={() => void withdraw(post)}>
                                            <Button size="small" type="text" danger>
                                                撤回
                                            </Button>
                                        </Popconfirm>
                                    </div>
                                    {post.reviewStatus === "REJECTED" && post.reviewNote ? <span className="account-sub">驳回理由：{post.reviewNote}</span> : null}
                                    {post.reuseCount > 0 ? <span className="account-sub">已被复用 {post.reuseCount} 次</span> : null}
                                </li>
                            );
                        })}
                    </ul>
                ) : (
                    <p className="account-sub">{loading ? "正在读取投稿记录…" : "还没有投稿。在「素材库」里打开一件作品，选择“发布到灵感广场”即可。"}</p>
                )}
            </section>
        </div>
    );
}
