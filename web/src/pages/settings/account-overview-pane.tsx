import { Button } from "antd";
import { ArrowLeft, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";

import { formatBytes, formatCount, formatDateTime, formatTokens } from "@/lib/format-usage";
import { http } from "@/services/api/request";

import { AccountDeletionCard, type AccountDeletionChannel } from "./account-deletion-card";

type AccountAgreement = {
    agreementType: string;
    version: string;
    acceptedAt: string;
};

type AccountOverview = {
    account: {
        id: string;
        name: string;
        email: string;
        phone: string;
        role: string;
        loginMethod: string;
        createdAt: string;
    };
    usage: {
        canvases: number;
        activeCanvases: number;
        assets: number;
        storedBytes: number;
        calls: number;
        failedCalls: number;
        inputTokens: number;
        outputTokens: number;
        days: number;
    } | null;
    agreements: AccountAgreement[];
    billing: { mode: string; balance: number | null; note: string };
};

const agreementLabels: Record<string, string> = {
    TERMS: "用户协议",
    PRIVACY: "隐私政策",
};

const methodLabels: Record<string, string> = {
    EMAIL_CODE: "邮箱验证码",
    PHONE_CODE: "手机验证码",
    PASSWORD: "密码登录",
    GITHUB_OAUTH: "GitHub",
    GOOGLE_OAUTH: "Google",
    WECHAT_OAUTH: "微信",
};

function identityOf(payload: AccountOverview) {
    return [payload.account.name, payload.account.email || payload.account.phone].filter(Boolean).join(" · ") || payload.account.id;
}

/**
 * 账户与用量。
 *
 * 托管形态下用户没有可配置的模型：上游凭证、渠道与计费都由平台持有。这块页面
 * 回答的是用户真正关心也能验证的三件事——我是谁、我用了多少、我同意过哪一版协议。
 * 因此它只读，不提供任何写入入口，避免长成一个"看起来能改其实改不了"的表单。
 */
export function AccountOverviewPane() {
    const navigate = useNavigate();
    const [payload, setPayload] = useState<AccountOverview | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            setPayload(await http.get<AccountOverview>("/finance/account"));
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载账户信息失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const windowLabel = payload?.usage ? `近 ${payload.usage.days} 天` : "";

    return (
        <div className="settings-pane mx-auto w-full max-w-2xl">
            <div className="settings-pane-header">
                <div className="min-w-0">
                    <h2>账户与用量</h2>
                    <p>模型与上游成本由平台统一提供，无需在此配置；这里只展示你的账号、用量与协议签署记录。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button icon={<RefreshCw className="size-4" />} loading={loading} onClick={() => void load()}>
                        刷新
                    </Button>
                    <Button icon={<ArrowLeft className="size-4" />} onClick={() => navigate("/")}>
                        返回创作
                    </Button>
                </div>
            </div>

            {error ? (
                <div className="account-notice is-error">
                    <span>{error}</span>
                </div>
            ) : null}

            {payload ? (
                <>
                    <section className="account-block">
                        <h3 className="account-block-title">账号</h3>
                        <div className="account-identity">
                            <span className="account-avatar" aria-hidden>
                                {(payload.account.name || payload.account.email || "K").slice(0, 1).toUpperCase()}
                            </span>
                            <span className="flex min-w-0 flex-col">
                                <b className="account-name">{identityOf(payload)}</b>
                                <span className="account-sub">
                                    {methodLabels[payload.account.loginMethod] ?? payload.account.loginMethod}
                                    {payload.account.role === "ADMIN" ? " · 管理员" : ""}
                                </span>
                            </span>
                        </div>
                        <dl className="account-kv">
                            <div>
                                <dt>邮箱</dt>
                                <dd>{payload.account.email || "未绑定"}</dd>
                            </div>
                            <div>
                                <dt>手机号</dt>
                                <dd>{payload.account.phone || "未绑定"}</dd>
                            </div>
                            <div>
                                <dt>注册时间</dt>
                                <dd>{formatDateTime(payload.account.createdAt)}</dd>
                            </div>
                        </dl>
                    </section>

                    {payload.usage ? (
                        <section className="account-block">
                            <h3 className="account-block-title">用量（{windowLabel}）</h3>
                            <div className="account-metrics">
                                <div className="account-metric">
                                    <span className="account-metric-label">模型调用</span>
                                    <span className="account-metric-value">{formatCount(payload.usage.calls)}</span>
                                    <span className="account-metric-note">失败 {formatCount(payload.usage.failedCalls)}</span>
                                </div>
                                <div className="account-metric">
                                    <span className="account-metric-label">Token</span>
                                    <span className="account-metric-value">{formatTokens(payload.usage.inputTokens + payload.usage.outputTokens)}</span>
                                    <span className="account-metric-note">
                                        输入 {formatTokens(payload.usage.inputTokens)} · 输出 {formatTokens(payload.usage.outputTokens)}
                                    </span>
                                </div>
                                <div className="account-metric">
                                    <span className="account-metric-label">存储</span>
                                    <span className="account-metric-value">{formatBytes(payload.usage.storedBytes)}</span>
                                    <span className="account-metric-note">素材 {formatCount(payload.usage.assets)} 个</span>
                                </div>
                                <div className="account-metric">
                                    <span className="account-metric-label">画布</span>
                                    <span className="account-metric-value">{formatCount(payload.usage.canvases)}</span>
                                    <span className="account-metric-note">{windowLabel}活跃 {formatCount(payload.usage.activeCanvases)}</span>
                                </div>
                            </div>
                        </section>
                    ) : null}

                    <section className="account-block">
                        <h3 className="account-block-title">协议签署记录</h3>
                        {payload.agreements.length === 0 ? (
                            <p className="account-sub">暂无记录</p>
                        ) : (
                            <ul className="account-agreements">
                                {payload.agreements.map((agreement) => (
                                    <li key={`${agreement.agreementType}-${agreement.version}-${agreement.acceptedAt}`}>
                                        <span>{agreementLabels[agreement.agreementType] ?? agreement.agreementType}</span>
                                        <span className="account-agreement-version">{agreement.version}</span>
                                        <span className="account-sub">{formatDateTime(agreement.acceptedAt)}</span>
                                    </li>
                                ))}
                            </ul>
                        )}
                    </section>

                    <section className="account-block">
                        <h3 className="account-block-title">计费</h3>
                        <p className="account-sub">{payload.billing.note}</p>
                    </section>

                    <AccountDeletionCard channels={deletionChannelsOf(payload)} />
                </>
            ) : loading ? (
                <p className="account-sub">正在读取账户信息…</p>
            ) : null}
        </div>
    );
}

/**
 * 注销只能用账号自己绑定过的渠道确认身份，因此这里只把已绑定的邮箱/手机号交给
 * 面板：未绑定的渠道点进去也只会得到一句"未绑定"，不如一开始就不出现。
 */
function deletionChannelsOf(payload: AccountOverview): AccountDeletionChannel[] {
    const channels: AccountDeletionChannel[] = [];
    if (payload.account.email) channels.push({ methodType: "EMAIL_CODE", label: "邮箱", target: payload.account.email });
    if (payload.account.phone) channels.push({ methodType: "PHONE_CODE", label: "手机号", target: payload.account.phone });
    return channels;
}
