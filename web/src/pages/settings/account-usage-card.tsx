import { Button } from "antd";
import { useCallback, useEffect, useState } from "react";

import { formatBytes, formatCount, formatDateTime, formatTokens } from "@/lib/format-usage";
import { http } from "@/services/api/request";

type AccountUsageView = {
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
    agreements: Array<{ agreementType: string; version: string; acceptedAt: string }>;
};

const agreementLabels: Record<string, string> = {
    TERMS: "用户协议",
    PRIVACY: "隐私政策",
};

/**
 * 「用量与协议留痕」：收在「更多设置」里的只读账。
 *
 * 这些数字平时没人看，但"我用了多少"和"我同意过哪一版协议"是用户能自己核对的凭据，
 * 因此不能删，只把它们从第一屏挪走。整张卡只读，不摆任何看起来能改的控件。
 */
export function AccountUsageCard() {
    const [view, setView] = useState<AccountUsageView | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        try {
            setView(await http.get<AccountUsageView>("/finance/account"));
            setError("");
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载用量失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const windowLabel = view?.usage ? `近 ${view.usage.days} 天` : "";

    return (
        <section className="account-card" aria-labelledby="account-usage-title">
            <div className="account-card-head">
                <h2 id="account-usage-title" className="account-card-title">用量与协议</h2>
                <Button size="small" type="text" loading={loading} onClick={() => void load()}>刷新</Button>
            </div>

            {error ? <p className="account-error">{error}</p> : null}

            {view?.usage ? (
                <>
                    <p className="account-inline-note">{windowLabel} · 模型与上游成本由平台统一提供</p>
                    <div className="account-metrics">
                        <div className="account-metric">
                            <span className="account-metric-label">模型调用</span>
                            <span className="account-metric-value">{formatCount(view.usage.calls)}</span>
                            <span className="account-metric-note">失败 {formatCount(view.usage.failedCalls)}</span>
                        </div>
                        <div className="account-metric">
                            <span className="account-metric-label">Token</span>
                            <span className="account-metric-value">{formatTokens(view.usage.inputTokens + view.usage.outputTokens)}</span>
                            <span className="account-metric-note">
                                输入 {formatTokens(view.usage.inputTokens)} · 输出 {formatTokens(view.usage.outputTokens)}
                            </span>
                        </div>
                        <div className="account-metric">
                            <span className="account-metric-label">存储</span>
                            <span className="account-metric-value">{formatBytes(view.usage.storedBytes)}</span>
                            <span className="account-metric-note">素材 {formatCount(view.usage.assets)} 个</span>
                        </div>
                        <div className="account-metric">
                            <span className="account-metric-label">画布</span>
                            <span className="account-metric-value">{formatCount(view.usage.canvases)}</span>
                            <span className="account-metric-note">{windowLabel}活跃 {formatCount(view.usage.activeCanvases)}</span>
                        </div>
                    </div>
                </>
            ) : (
                <p className="account-inline-note">{loading ? "正在读取用量…" : "暂时读不到用量。"}</p>
            )}

            <div className="account-field">
                <span className="account-field-label">协议签署记录</span>
                {view && view.agreements.length > 0 ? (
                    <ul className="account-agreements">
                        {view.agreements.map((agreement) => (
                            <li key={`${agreement.agreementType}-${agreement.version}-${agreement.acceptedAt}`}>
                                <span>{agreementLabels[agreement.agreementType] ?? agreement.agreementType}</span>
                                <span className="account-agreement-version">{agreement.version}</span>
                                <span className="account-card-aside">{formatDateTime(agreement.acceptedAt)}</span>
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p className="account-inline-note">暂无记录</p>
                )}
            </div>
        </section>
    );
}
