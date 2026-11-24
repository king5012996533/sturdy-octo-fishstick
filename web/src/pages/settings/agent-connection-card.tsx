import { App, Button, Form, Input, InputNumber, Popconfirm, Select, Tag } from "antd";
import { Check, Copy, KeyRound, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { AppModal } from "@/components/ui/product/app-modal";
import { formatDateTime } from "@/lib/format-usage";
import {
    approveAgentBudgetRequest,
    createMcpToken,
    getMcpStatus,
    listAgentBudgetRequests,
    listMcpTokens,
    revokeMcpToken,
    type AgentBudgetRequest,
    type CreateMcpTokenResult,
    type McpStatus,
    type McpToken,
} from "@/services/api/mcp-integration";
import { ApiError } from "@/services/api/request";

const expiryOptions = [
    { value: 7, label: "7 天" },
    { value: 30, label: "30 天" },
    { value: 90, label: "90 天" },
    { value: 365, label: "365 天" },
];

const permissionOptions = [
    { value: "auto", label: "自主执行（受积分预算限制）" },
    { value: "request_approval", label: "每次生成需要审批" },
    { value: "read_only", label: "只读" },
];

type CreateTokenForm = {
    name: string;
    expiresInDays: number;
    permissionMode: "read_only" | "request_approval" | "auto";
    maxCreditsPerRun: number;
    maxCreditsPerDay: number;
};

/**
 * 「连接 Agent」：把当前账号接到 Claude Code / Claude Desktop 等 MCP 客户端。
 *
 * 连接凭据是一次性展示的个人访问令牌，后端只存哈希。令牌可以随时吊销；
 * 外部 Agent 发起的运行会带 `source=mcp` 回到 Agent 历史，用户能看见来源。
 */
export function AgentConnectionCard() {
    const { message } = App.useApp();
    const [status, setStatus] = useState<McpStatus | null>(null);
    const [tokens, setTokens] = useState<McpToken[]>([]);
    const [budgetRequests, setBudgetRequests] = useState<AgentBudgetRequest[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [createOpen, setCreateOpen] = useState(false);
    const [creating, setCreating] = useState(false);
    const [created, setCreated] = useState<CreateMcpTokenResult | null>(null);
    const [busyId, setBusyId] = useState("");
    const [approvingId, setApprovingId] = useState("");
    const [copied, setCopied] = useState("");
    const [form] = Form.useForm<CreateTokenForm>();

    const load = useCallback(async () => {
        setLoading(true);
        try {
            const [nextStatus, nextTokens, nextRequests] = await Promise.all([
                getMcpStatus(),
                listMcpTokens(),
                listAgentBudgetRequests(),
            ]);
            setStatus(nextStatus);
            setTokens(nextTokens.tokens);
            setBudgetRequests(nextRequests.requests);
            setError("");
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "读取连接状态失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const openCreate = () => {
        form.setFieldsValue({
            name: "Claude Code",
            expiresInDays: 30,
            permissionMode: "auto",
            maxCreditsPerRun: 1000,
            maxCreditsPerDay: 2000,
        });
        setCreateOpen(true);
    };

    const submitCreate = async () => {
        const values = await form.validateFields();
        setCreating(true);
        try {
            const result = await createMcpToken({
                name: values.name.trim(),
                expiresInDays: values.expiresInDays,
                permissionMode: values.permissionMode,
                maxCreditsPerRun: values.maxCreditsPerRun,
                maxCreditsPerDay: values.maxCreditsPerDay,
            });
            setCreated(result);
            setCreateOpen(false);
            await load();
        } catch (createError) {
            message.error(createError instanceof ApiError ? createError.message : "创建连接失败，请稍后重试");
        } finally {
            setCreating(false);
        }
    };

    const revoke = async (token: McpToken) => {
        setBusyId(token.id);
        try {
            await revokeMcpToken(token.id);
            message.success("连接已吊销");
            await load();
        } catch (revokeError) {
            message.error(revokeError instanceof ApiError ? revokeError.message : "吊销失败，请稍后重试");
        } finally {
            setBusyId("");
        }
    };

    const approveBudget = async (request: AgentBudgetRequest) => {
        setApprovingId(request.id);
        try {
            await approveAgentBudgetRequest(request.id);
            message.success("已提高单轮与每日额度；Agent 需要开始新一轮才能继续");
            await load();
        } catch (approveError) {
            message.error(approveError instanceof ApiError ? approveError.message : "批准失败，请稍后重试");
        } finally {
            setApprovingId("");
        }
    };

    const copy = async (key: string, value: string) => {
        try {
            await navigator.clipboard.writeText(value);
            setCopied(key);
            window.setTimeout(() => setCopied(""), 1600);
        } catch {
            message.error("复制失败，请手动选择文本");
        }
    };

    const baseUrl = status?.baseUrl || window.location.origin;
    const activeCount = tokens.filter((token) => !token.revokedAt && !isExpired(token)).length;
    const pendingRequests = budgetRequests.filter((request) => request.status === "pending");

    return (
        <section className="account-card" aria-labelledby="mcp-connection-title">
            <div className="account-card-head">
                <div className="min-w-0">
                    <h2 id="mcp-connection-title" className="account-card-title">连接 Agent（MCP）</h2>
                    <p className="account-sub mt-1">
                        让 Claude Code、Claude Desktop 等外部 Agent 直接操作这个 Kinotv 账号：读画布、运行 Agent、生成素材。
                    </p>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                    <Button icon={<RefreshCw className="size-4" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button type="primary" icon={<Plus className="size-4" />} onClick={openCreate}>
                        创建连接
                    </Button>
                </div>
            </div>

            {error ? <p className="account-error mt-3">{error}</p> : null}

            <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2">
                <span className="account-sub">
                    连接地址：<code className="account-code">{baseUrl}</code>
                </span>
                <span className="account-sub">{activeCount > 0 ? `当前有效连接 ${activeCount} 个` : "还没有有效连接"}</span>
            </div>

            {pendingRequests.length > 0 ? (
                <div className="mt-4 flex flex-col gap-3 rounded-lg border border-border/60 p-3">
                    <div>
                        <b>待批准的加额申请</b>
                        <p className="account-sub mt-1">Agent 只能提出申请，不能自行提高额度。批准后旧 Run 会因授权版本变化而停止，需开始新一轮。</p>
                    </div>
                    {pendingRequests.map((request) => (
                        <div key={request.id} className="flex flex-wrap items-start justify-between gap-3 border-t border-border/40 pt-3 first:border-t-0 first:pt-0">
                            <div className="flex min-w-0 flex-col gap-1">
                                <span>
                                    申请将单轮额度提高到 <b>{request.requestedCredits.toLocaleString("zh-CN")}</b> 积分 · Run{" "}
                                    <code className="account-code">{shortID(request.runId)}</code>
                                </span>
                                {request.reason ? <span className="account-sub">{request.reason}</span> : null}
                                <span className="account-sub">提交于 {formatDateTime(request.createdAt)}</span>
                            </div>
                            <Button
                                type="primary"
                                size="small"
                                loading={approvingId === request.id}
                                onClick={() => void approveBudget(request)}
                            >
                                批准加额
                            </Button>
                        </div>
                    ))}
                </div>
            ) : null}

            <div className="mt-4">
                {tokens.length > 0 ? (
                    <ul className="flex flex-col">
                        {tokens.map((token) => {
                            const expired = isExpired(token);
                            return (
                                <li key={token.id} className="flex items-start justify-between gap-3 border-t border-border/40 py-3 first:border-t-0 first:pt-0">
                                    <div className="flex min-w-0 flex-col gap-1">
                                        <div className="flex flex-wrap items-center gap-2">
                                            <KeyRound className="size-3.5" aria-hidden />
                                            <b className="truncate">{token.name}</b>
                                            <code className="account-sub font-mono">{token.prefix}…</code>
                                            {token.revokedAt ? (
                                                <Tag>已吊销</Tag>
                                            ) : expired ? (
                                                <Tag color="orange">已过期</Tag>
                                            ) : (
                                                <Tag color="green">有效</Tag>
                                            )}
                                        </div>
                                        <span className="account-sub">
                                            创建于 {formatDateTime(token.createdAt)} · 最近使用 {token.lastUsedAt ? formatDateTime(token.lastUsedAt) : "从未使用"} ·{" "}
                                            {token.expiresAt ? `有效期至 ${formatDateTime(token.expiresAt)}` : "永不过期"}
                                        </span>
                                        {token.permissionMode ? (
                                            <span className="account-sub">
                                                {permissionLabel(token.permissionMode)} · 单轮 {token.maxCreditsPerRun ?? 0} 积分 · 每日{" "}
                                                {token.maxCreditsPerDay ?? 0} 积分
                                            </span>
                                        ) : null}
                                    </div>
                                    {!token.revokedAt ? (
                                        <Popconfirm
                                            title="吊销这个连接？"
                                            description="使用该令牌的 Agent 会立即失去访问权限。"
                                            okText="吊销"
                                            cancelText="取消"
                                            onConfirm={() => void revoke(token)}
                                        >
                                            <Button size="small" type="text" danger loading={busyId === token.id} icon={<Trash2 className="size-3.5" />}>
                                                吊销
                                            </Button>
                                        </Popconfirm>
                                    ) : null}
                                </li>
                            );
                        })}
                    </ul>
                ) : (
                    <p className="account-sub">{loading ? "正在读取连接…" : "还没有连接。创建令牌后，把配置复制给 Claude 即可开始。"}</p>
                )}
            </div>

            <AppModal
                open={createOpen}
                title="创建 Agent 连接"
                okText="创建"
                confirmLoading={creating}
                onOk={() => void submitCreate()}
                onCancel={() => setCreateOpen(false)}
            >
                <Form form={form} layout="vertical" className="pt-2">
                    <Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true, message: "请填写连接名称" }]}>
                        <Input maxLength={100} placeholder="例如：Claude Code" />
                    </Form.Item>
                    <Form.Item name="expiresInDays" label="有效期" rules={[{ required: true, message: "请选择有效期" }]}>
                        <Select options={expiryOptions} />
                    </Form.Item>
                    <Form.Item name="permissionMode" label="权限模式" rules={[{ required: true, message: "请选择权限模式" }]}>
                        <Select options={permissionOptions} />
                    </Form.Item>
                    <div className="grid grid-cols-2 gap-3">
                        <Form.Item
                            name="maxCreditsPerRun"
                            label="单轮积分上限"
                            dependencies={["permissionMode"]}
                            rules={[
                                { required: true, message: "请填写单轮上限" },
                                ({ getFieldValue }) => ({
                                    validator(_, value: number) {
                                        if (getFieldValue("permissionMode") !== "auto" || value > 0) {
                                            return Promise.resolve();
                                        }
                                        return Promise.reject(new Error("自主执行必须设置单轮上限"));
                                    },
                                }),
                            ]}
                        >
                            <InputNumber className="w-full" min={0} max={10_000_000} precision={0} />
                        </Form.Item>
                        <Form.Item
                            name="maxCreditsPerDay"
                            label="每日积分上限"
                            dependencies={["permissionMode"]}
                            rules={[
                                { required: true, message: "请填写每日上限" },
                                ({ getFieldValue }) => ({
                                    validator(_, value: number) {
                                        if (getFieldValue("permissionMode") !== "auto" || value > 0) {
                                            return Promise.resolve();
                                        }
                                        return Promise.reject(new Error("自主执行必须设置每日上限"));
                                    },
                                }),
                            ]}
                        >
                            <InputNumber className="w-full" min={0} max={10_000_000} precision={0} />
                        </Form.Item>
                    </div>
                    <p className="account-sub">
                        每日额度跨所有 MCP 客户端与所有 Run 共享。自主执行时，Agent 只能在额度内工作；
                        需要加额时必须由你在这里批准，Agent 不能自行提高额度。
                    </p>
                    <p className="account-sub">
                        Token 只在创建成功时显示一次。后端只保存哈希，创建后无法再次查看；丢失后请吊销并重新创建。
                    </p>
                </Form>
            </AppModal>

            <AppModal
                open={created !== null}
                title="连接已创建"
                width={720}
                footer={
                    <Button type="primary" onClick={() => setCreated(null)}>
                        知道了
                    </Button>
                }
                onCancel={() => setCreated(null)}
            >
                {created ? (
                    <div className="flex flex-col gap-4 pt-2">
                        <div className="flex flex-col gap-2">
                            <span className="account-sub">访问令牌（只显示这一次）</span>
                            <div className="flex items-start gap-2">
                                <code className="mcp-token-code flex-1">{created.token}</code>
                                <Button
                                    icon={copied === "token" ? <Check className="size-4" /> : <Copy className="size-4" />}
                                    onClick={() => void copy("token", created.token)}
                                >
                                    {copied === "token" ? "已复制" : "复制"}
                                </Button>
                            </div>
                        </div>

                        <div className="flex flex-col gap-2">
                            <div className="flex items-center justify-between gap-2">
                                <span className="account-sub">Claude Code 配置</span>
                                <Button
                                    size="small"
                                    type="text"
                                    icon={copied === "claude" ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                                    onClick={() => void copy("claude", claudeCodeSnippet(baseUrl, created.token))}
                                >
                                    {copied === "claude" ? "已复制" : "复制"}
                                </Button>
                            </div>
                            <pre className="mcp-token-code">{claudeCodeSnippet(baseUrl, created.token)}</pre>
                        </div>

                        <div className="flex flex-col gap-2">
                            <div className="flex items-center justify-between gap-2">
                                <span className="account-sub">Claude Desktop 配置</span>
                                <Button
                                    size="small"
                                    type="text"
                                    icon={copied === "desktop" ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                                    onClick={() => void copy("desktop", claudeDesktopSnippet(baseUrl, created.token))}
                                >
                                    {copied === "desktop" ? "已复制" : "复制"}
                                </Button>
                            </div>
                            <pre className="mcp-token-code">{claudeDesktopSnippet(baseUrl, created.token)}</pre>
                        </div>

                        <p className="account-sub">
                            把配置里的 <code className="account-code">/absolute/path/to/kinotv-mcp</code> 换成你本机的 MCP 二进制路径。令牌是一次性展示的，关闭窗口后无法再次查看。
                        </p>
                    </div>
                ) : null}
            </AppModal>
        </section>
    );
}

function isExpired(token: McpToken) {
    return Boolean(token.expiresAt && new Date(token.expiresAt).getTime() <= Date.now());
}

function shortID(value: string) {
    return value.length > 12 ? `${value.slice(0, 12)}…` : value;
}

function permissionLabel(mode: NonNullable<McpToken["permissionMode"]>) {
    switch (mode) {
        case "auto":
            return "自主执行";
        case "request_approval":
            return "逐次审批";
        default:
            return "只读";
    }
}

function claudeCodeSnippet(baseUrl: string, token: string) {
    return `claude mcp add kinotv \\
  -e KINOTV_MCP_BASE_URL=${baseUrl} \\
  -e KINOTV_MCP_ACCESS_TOKEN=${token} \\
  -- /absolute/path/to/kinotv-mcp`;
}

function claudeDesktopSnippet(baseUrl: string, token: string) {
    return JSON.stringify(
        {
            mcpServers: {
                kinotv: {
                    command: "/absolute/path/to/kinotv-mcp",
                    env: {
                        KINOTV_MCP_BASE_URL: baseUrl,
                        KINOTV_MCP_ACCESS_TOKEN: token,
                    },
                },
            },
        },
        null,
        2,
    );
}
