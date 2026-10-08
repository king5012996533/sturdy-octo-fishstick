import { Button } from "antd";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { Callout, type CalloutTone } from "@/components/ui/product/callout";
import { cn } from "@/lib/utils";
import { getCreditWallet, type CreditWallet } from "@/services/api/credit";
import type { BillingOrder } from "@/services/api/billing";
import { useUserStore } from "@/stores/use-user-store";

import { BalanceCard } from "./wallet-balance";
import { CreditLedgerSection } from "./wallet-ledger";
import { CreditOrdersSection } from "./wallet-orders";
import { WalletPayDialog } from "./wallet-pay-dialog";
import { CreditTopUpSection } from "./wallet-top-up";
import { WalletSectionHead, errorMessage } from "./wallet-kit";

/**
 * 用户端积分中心（/wallet）：余额 → 充值 → 订单 → 流水，四级信息层级。
 *
 * 余额是唯一主角，充值是它的下一步动作，订单是付款凭据，流水是账目明细。四个 Zone 各自
 * 加载、各自失败、各自重试：账目接口抖动不能让用户看不到余额，反之亦然。
 *
 * 本文件只负责编排四个 Zone：每个 Zone 的实现留在自己的文件里，段落顺序即信息顺序。
 */

export function WalletPage() {
    const accountName = useUserStore((state) => state.user?.displayName || state.user?.username || "");

    const [wallet, setWallet] = useState<CreditWallet | null>(null);
    const [walletLoading, setWalletLoading] = useState(true);
    const [walletError, setWalletError] = useState("");
    const [notice, setNotice] = useState<{ tone: CalloutTone; text: string } | null>(null);
    // 一个计数器驱动三个 Zone：刷新、充值成功后余额/档位/流水一起重取，读数不会互相矛盾。
    const [revision, setRevision] = useState(0);
    // 支付弹窗挂在页面级：充值区下单和订单区「继续支付」进的是同一个弹窗，收款码只有一份实现。
    const [payOrder, setPayOrder] = useState<BillingOrder | null>(null);
    const topUpRef = useRef<HTMLDivElement | null>(null);
    const ledgerRef = useRef<HTMLDivElement | null>(null);

    useEffect(() => {
        let cancelled = false;
        setWalletLoading(true);
        setWalletError("");
        getCreditWallet()
            .then((next) => {
                if (!cancelled) setWallet(next);
            })
            .catch((cause) => {
                if (!cancelled) setWalletError(errorMessage(cause, "余额加载失败，请稍后重试。"));
            })
            .finally(() => {
                if (!cancelled) setWalletLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [revision]);

    const reloadAll = useCallback(() => setRevision((value) => value + 1), []);
    const scrollToTopUp = useCallback(() => {
        topUpRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }, []);
    const scrollToLedger = useCallback(() => {
        ledgerRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }, []);

    return (
        <WorkspacePage>
            {/* 负边距正好抵消 WorkspacePage 的 px-3 py-3：横幅铺满内容区，从这一屏的第一个像素开始。
                标题压在剧照上，与首页是同一套语言；外壳页头在这条路由上不再出现。 */}
            <section className="wallet-hero -mx-3 -mt-3 sm:-mx-4 sm:-mt-4 xl:-mx-5" aria-labelledby="wallet-hero-title">
                <img className="wallet-hero-art" src="/home/hero.webp" alt="" aria-hidden="true" draggable={false} />
                <div className="wallet-hero-copy">
                    <p className="wallet-hero-brand">Creation credits</p>
                    <h1 id="wallet-hero-title" className="wallet-hero-title">
                        积分中心
                    </h1>
                    <p className="wallet-hero-sub">账户积分由平台统一计费：每次生成按模型用量扣减，失败自动退回。</p>
                    <div className="wallet-hero-actions">
                        <button type="button" className="wallet-hero-action" disabled={walletLoading} onClick={reloadAll}>
                            <RefreshCw className={cn(walletLoading && "animate-spin")} aria-hidden />
                            刷新
                        </button>
                        {accountName ? <span className="wallet-hero-account">{accountName}</span> : null}
                    </div>
                </div>
            </section>

            {/* 全局 message 在本项目里是关闭的，所有反馈都必须落在页面内。 */}
            {notice ? (
                <Callout tone={notice.tone} className="mt-3" onClose={() => setNotice(null)}>
                    {notice.text}
                </Callout>
            ) : null}

            {/* Zone A 与下面三段共用同一套段落头：四段的节奏一致，余额靠读数大小而不是靠没有标题来当主角。 */}
            <section className="wallet-section wallet-section-lead" aria-label="账户余额">
                <WalletSectionHead eyebrow="Balance" title="账户余额" />
                <BalanceCard wallet={wallet} loading={walletLoading} error={walletError} onRetry={reloadAll} onTopUp={scrollToTopUp} onLedger={scrollToLedger} />
            </section>

            <div ref={topUpRef} className="scroll-mt-16">
                <CreditTopUpSection revision={revision} onNotice={setNotice} onSettled={reloadAll} onOpenPayment={setPayOrder} />
            </div>

            <CreditOrdersSection revision={revision} onNotice={setNotice} onOpenPayment={setPayOrder} />

            <div ref={ledgerRef} className="scroll-mt-16">
                <CreditLedgerSection revision={revision} onTopUp={scrollToTopUp} />
            </div>
            {payOrder ? <WalletPayDialog order={payOrder} onClose={() => setPayOrder(null)} onSettled={reloadAll} onNotice={setNotice} /> : null}
        </WorkspacePage>
    );
}
