import { Alert, Button, Input, Modal, message } from "antd";
import { useEffect, useMemo, useState } from "react";

import { publishCreationPost, type CreationPostRecord } from "@/services/api/creation-posts";

/**
 * 把一件生成产物发布到灵感广场。
 *
 * 这个弹窗承担三件事，缺一件都会让投稿体验断掉：
 *  1. 收集"可复用的那一半"——标题、说明与提示词。产物只是结果，别人真正能复用的是
 *     提示词，所以提示词是必填项，标题只负责让卡片在广场上被认出来。
 *  2. 在提交之前写清审核口径：内容先过关键词、再进人工队列，色情/政治/宗教一类
 *     不会通过。用户按下提交前就知道代价，比提交后收到一条驳回要好。
 *  3. 把审核结论当场回显：驳回要能看到理由，待审要明确"通过了才会出现在广场"。
 */
export type PublishableAsset = {
    resourceId: string;
    kind: "image" | "video";
    defaultTitle: string;
    defaultPrompt: string;
};

const TITLE_MAX_LEN = 40;

export function PublishInspirationModal({ target, onClose, onPublished }: { target: PublishableAsset | null; onClose: () => void; onPublished?: () => void }) {
    const [title, setTitle] = useState("");
    const [description, setDescription] = useState("");
    const [prompt, setPrompt] = useState("");
    const [category, setCategory] = useState("");
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState("");
    const [result, setResult] = useState<CreationPostRecord | null>(null);

    // 每次换一件产物都要重置：沿用上一件的标题与提示词会让用户以为"改的是这一件"。
    // 依赖取 resourceId 而不是对象本身：调用方每次渲染都会新建一个 target 对象，
    // 按对象身份做依赖会在用户打字时的任意一次重渲染里把输入清空。
    const targetId = target?.resourceId ?? "";
    useEffect(() => {
        if (!target) return;
        setTitle(target.defaultTitle);
        setDescription("");
        setPrompt(target.defaultPrompt);
        setCategory("");
        setError("");
        setResult(null);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [targetId]);

    const mode = target?.kind === "video" ? "video" : "image";
    const canSubmit = useMemo(() => title.trim().length > 0 && prompt.trim().length > 0 && !submitting, [prompt, submitting, title]);

    // 「再发一条」与换产物走同一套重置：只清空结果而不清空输入，等于把上一条原样再投一次。
    const resetForm = () => {
        setTitle(target?.defaultTitle ?? "");
        setDescription("");
        setPrompt(target?.defaultPrompt ?? "");
        setCategory("");
        setError("");
        setResult(null);
    };

    const submit = async () => {
        if (!target) return;
        if (!title.trim()) {
            setError("请填写标题");
            return;
        }
        if (!prompt.trim()) {
            setError("请填写提示词：别人复用的就是它");
            return;
        }
        setSubmitting(true);
        setError("");
        try {
            const { post } = await publishCreationPost({
                resourceId: target.resourceId,
                title: title.trim(),
                description: description.trim(),
                prompt: prompt.trim(),
                mode,
                category: category.trim(),
            });
            setResult(post);
            onPublished?.();
            if (post.reviewStatus === "REJECTED") message.warning("这条投稿未通过内容预检");
            else message.success("已提交，审核通过后会出现在灵感广场");
        } catch (submitError) {
            setError(submitError instanceof Error ? submitError.message : "投稿失败，请稍后再试");
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <Modal
            open={Boolean(target)}
            title={result ? "投稿已提交" : "发布到灵感广场"}
            okText={result ? "完成" : "提交审核"}
            cancelText={result ? "再发一条" : "取消"}
            confirmLoading={submitting}
            onCancel={onClose}
            onOk={() => {
                if (!result) return void submit();
                onClose();
            }}
            cancelButtonProps={{ onClick: result ? () => resetForm() : undefined }}
        >
            {result ? (
                <div className="flex flex-col gap-3">
                    {result.reviewStatus === "REJECTED" ? (
                        <Alert type="warning" showIcon message="未通过内容预检" description={result.reviewNote || "内容不符合广场规范，无法通过审核。"} />
                    ) : (
                        <Alert type="success" showIcon message="已进入人工审核队列" description="审核通过后，这条作品会出现在精选灵感广场，其他用户可以一键复用它的提示词。" />
                    )}
                    <p className="text-[var(--fs-tiny)] text-muted-foreground">你可以在「设置 → 我的投稿」里查看审核状态，或随时撤回这条投稿。</p>
                </div>
            ) : (
                <div className="flex flex-col gap-3">
                    <label className="flex flex-col gap-1">
                        <span className="text-[var(--fs-tiny)] text-muted-foreground">标题（最多 {TITLE_MAX_LEN} 字）</span>
                        <Input value={title} maxLength={TITLE_MAX_LEN} showCount placeholder="例如：雨夜霓虹的电影感开场" onChange={(event) => setTitle(event.target.value)} />
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-[var(--fs-tiny)] text-muted-foreground">一句话说明（选填）</span>
                        <Input value={description} maxLength={200} placeholder="描述画面风格、构图或情绪" onChange={(event) => setDescription(event.target.value)} />
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-[var(--fs-tiny)] text-muted-foreground">提示词（必填）</span>
                        <Input.TextArea value={prompt} autoSize={{ minRows: 4, maxRows: 10 }} placeholder="别人一键复用时，落进创作框的就是这段提示词" onChange={(event) => setPrompt(event.target.value)} />
                    </label>
                    <label className="flex flex-col gap-1">
                        <span className="text-[var(--fs-tiny)] text-muted-foreground">分类（选填）</span>
                        <Input value={category} maxLength={20} placeholder="例如：赛博朋克 / 短剧开场" onChange={(event) => setCategory(event.target.value)} />
                    </label>
                    {error ? <Alert type="error" showIcon message={error} /> : null}
                    <Alert type="info" showIcon message="审核说明" description="投稿先过关键词预检，再进入人工审核。色情低俗、政治敏感、宗教宣扬以及赌博、毒品、暴力恐怖一类内容不会通过；审核通过前不会出现在广场上。" />
                </div>
            )}
        </Modal>
    );
}
