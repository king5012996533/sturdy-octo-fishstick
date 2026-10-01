import { Button } from "antd";
import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";

import { formatCount } from "@/lib/format-usage";
import { getCreditWallet, type CreditWallet } from "@/services/api/credit";

/**
 * 「积分」：账号页第一张卡，也是整页唯一的大字号。
 *
 * 这里是"我还剩多少"的答案，因此只放一个读数、一行累计、一条去积分中心的路。
 * 把充值档位、订单、流水都搬进来会立刻把这一页变回一张对账单——那些留在 /wallet，
 * 想看的人点进去就有。
 */
export function AccountCreditsCard() {
    const [wallet, setWallet] = useState<CreditWallet | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        try {
            setWallet(await getCreditWallet());
            setError("");
        } catch (loadError) {
            // 服务端的中文文案原样透出：本地再翻译一层，就会在规则改动后说错原因。
            setError(loadError instanceof Error ? loadError.message : "读取积分失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    return (
        <section className="account-card" aria-labelledby="account-credits-title">
            <div className="account-card-head">
                <h2 id="account-credits-title" className="account-card-title">积分</h2>
                {/* 余额是会被后台调整改变的数字，写一个更新时间比让用户自己怀疑更省事。 */}
                {wallet?.updatedAt ? <span className="account-card-aside">更新于 {formatUpdatedAt(wallet.updatedAt)}</span> : null}
            </div>

            <p className="account-credits-value" aria-live="polite">
                {wallet ? formatCount(wallet.balance) : loading ? "—" : "0"}
            </p>
            {wallet ? (
                <p className="account-credits-meta">
                    累计获得 {formatCount(wallet.lifetimeIn)} · 累计消耗 {formatCount(wallet.lifetimeOut)}
                </p>
            ) : null}

            {error ? <p className="account-error">{error}</p> : null}

            <div className="account-credits-actions">
                {/* 本地/桌面构建里没有 /wallet（路由随托管开关被摇掉），也就没有可跳的明细页。 */}
                {__BEEFTV_HOSTED_AUTH__ ? (
                    <Link to="/wallet">
                        <Button>{wallet ? "充值 / 查看明细" : "去积分中心"}</Button>
                    </Link>
                ) : null}
                {error ? <Button type="text" onClick={() => void load()}>重试</Button> : null}
            </div>
        </section>
    );
}

/** 只要到分钟：账号页的"更新于"是为了说明这个数字是新的，不是为了留审计时间。 */
function formatUpdatedAt(value: string) {
    const at = new Date(value);
    if (Number.isNaN(at.getTime())) return "刚刚";
    return `${at.getMonth() + 1} 月 ${at.getDate()} 日 ${String(at.getHours()).padStart(2, "0")}:${String(at.getMinutes()).padStart(2, "0")}`;
}
