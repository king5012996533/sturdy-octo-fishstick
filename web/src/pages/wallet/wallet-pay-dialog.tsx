import { Button } from "antd";
import { Check, CheckCircle2, Copy, ExternalLink } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { AppModal } from "@/components/ui/product/app-modal";
import { Callout, type CalloutTone } from "@/components/ui/product/callout";
import { formatCount } from "@/lib/format-usage";
import { cancelBillingOrder, formatMoneyFen, getBillingOrder, payBillingOrder, type BillingPaymentLaunch, type BillingOrder, type BillingOrderStatus } from "@/services/api/billing";
import { useAppearanceStore } from "@/stores/use-appearance-store";

import { errorMessage } from "./wallet-kit";

/**
 * 充值支付弹窗 —— 用户点「立即充值」之后唯一要做的事。
 *
 * 之前的做法是把收款码常驻在充值区顶部：用户还没下单就先看到一张码，下单按钮又另起一行，
 * 两边都不像"下一步该干什么"，于是同一档位被连着下两单、后台堆待支付订单。
 *
 * 现在的顺序是单向的：选档位 → 点充值 → 弹窗里拿订单号并扫码 → 轮询到账。收款码只在
 * 付这一单的时候出现，订单号、金额、备注三件事在同一个视野里，不需要用户自己拼。
 *
 * 弹窗同时服务两种渠道：有收银台地址（聚合/微信/支付宝）就给跳转按钮，没有（MANUAL）
 * 就走扫码兜底。两种都不算失败——下单已经成功，这里说"支付失败"只会让人重复付款。
 */

const MANUAL_POLL_MS = 4000;

export function WalletPayDialog({ order, onClose, onSettled, onNotice }: { order: BillingOrder | null; onClose: () => void; onSettled: () => void; onNotice: (notice: { tone: CalloutTone; text: string }) => void }) {
    const qrUrl = useAppearanceStore((state) => state.appearance.paymentQrUrl);

    const orderId = order?.id ?? "";
    const [launch, setLaunch] = useState<BillingPaymentLaunch | null>(null);
    const [preparing, setPreparing] = useState(false);
    const [prepareError, setPrepareError] = useState("");
    const [status, setStatus] = useState<BillingOrderStatus>("PENDING");
    const [busy, setBusy] = useState(false);
    const [copied, setCopied] = useState(false);
    const [retryKey, setRetryKey] = useState(0);
    const settledRef = useRef(false);

    // 每次换成另一笔订单都要把上一条的状态清干净，否则第二单会带着第一单的二维码和已到账勾出现。
    useEffect(() => {
        settledRef.current = false;
        setCopied(false);
        setPrepareError("");
        setLaunch(null);
        setStatus(order?.status ?? "PENDING");
    }, [orderId, order?.status]);

    // 打开时发起一次支付，拿收银台信息。MANUAL 渠道 service 层返回成功但 payUrl 为空，
    // 那条空结果本身就是"走扫码兜底"的信号，不是错误。
    useEffect(() => {
        if (!orderId) return;
        let cancelled = false;
        setPreparing(true);
        setPrepareError("");
        payBillingOrder(orderId)
            .then((result) => {
                if (cancelled) return;
                setLaunch(result);
                if (result?.order?.status) setStatus(result.order.status);
            })
            .catch((cause) => {
                if (cancelled) return;
                setPrepareError(errorMessage(cause, "发起支付失败，请稍后重试。"));
            })
            .finally(() => {
                if (!cancelled) setPreparing(false);
            });
        return () => {
            cancelled = true;
        };
    }, [orderId, retryKey]);

    // 待支付期间轮询订单状态：上游到账后运营会在后台补单，用户不需要手动刷页面才发现积分到了。
    useEffect(() => {
        if (!orderId || status !== "PENDING") return;
        let cancelled = false;
        const timer = window.setInterval(() => {
            getBillingOrder(orderId)
                .then((next) => {
                    if (!cancelled && next?.status) setStatus(next.status);
                })
                .catch(() => {
                    // 轮询是后台行为：一次失败不该在弹窗里弹错误，下一轮会自己恢复。
                });
        }, MANUAL_POLL_MS);
        return () => {
            cancelled = true;
            window.clearInterval(timer);
        };
    }, [orderId, status]);

    // 到账只结算一次：轮询与手动刷新可能同时命中 PAID，重复推进 revision 会让页面来回重排。
    useEffect(() => {
        if (status !== "PAID" || settledRef.current) return;
        settledRef.current = true;
        onSettled();
    }, [status, onSettled]);

    const refreshStatus = useCallback(async () => {
        if (!orderId) return;
        setBusy(true);
        try {
            const next = await getBillingOrder(orderId);
            setStatus(next.status);
            if (next.status === "PENDING") onNotice({ tone: "info", text: "还没有查到这笔订单的到账记录，付款后通常几分钟内自动入账。" });
        } catch (cause) {
            onNotice({ tone: "error", text: errorMessage(cause, "订单状态查询失败，请稍后重试。") });
        } finally {
            setBusy(false);
        }
    }, [orderId, onNotice]);

    const copyOrderNo = useCallback(() => {
        if (!order) return;
        void navigator.clipboard?.writeText(order.orderNo).catch(() => {});
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1600);
    }, [order]);

    const cancelOrder = useCallback(async () => {
        if (!order || busy) return;
        setBusy(true);
        try {
            await cancelBillingOrder(order.id);
            onNotice({ tone: "success", text: `订单 ${order.orderNo} 已取消。` });
            onSettled();
            onClose();
        } catch (cause) {
            onNotice({ tone: "error", text: errorMessage(cause, "取消订单失败，请稍后重试。") });
        } finally {
            setBusy(false);
        }
    }, [order, busy, onNotice, onSettled, onClose]);

    if (!order) return null;

    const payUrl = launch?.payUrl ?? "";
    const channelQrUrl = launch?.payParams?.qrCodeUrl || launch?.payParams?.qrUrl || "";
    const qrImage = channelQrUrl || qrUrl;
    const paid = status === "PAID";
    const settledOther = status === "CANCELED" || status === "REFUNDED" || status === "FAILED";
    const statusLabel: Record<string, string> = { CANCELED: "已取消", REFUNDED: "已退款", FAILED: "支付失败" };

    return (
        <AppModal
            open
            centered
            width={460}
            destroyOnHidden
            title="完成支付"
            footer={
                paid || settledOther ? (
                    <Button type="primary" onClick={onClose}>
                        完成
                    </Button>
                ) : (
                    <div className="wallet-pay-dialog-actions">
                        <Button disabled={busy} onClick={() => void cancelOrder()}>
                            取消订单
                        </Button>
                        <Button type="primary" loading={busy} onClick={() => void refreshStatus()}>
                            刷新支付状态
                        </Button>
                    </div>
                )
            }
            onCancel={onClose}
            flush
        >
            <div className="wallet-pay-dialog">
                <div className="wallet-pay-dialog-order">
                    <span className="wallet-pay-dialog-order-label">订单号</span>
                    <code className="wallet-pay-dialog-order-no">{order.orderNo}</code>
                    <button type="button" className="wallet-pay-dialog-copy" onClick={copyOrderNo}>
                        {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
                        {copied ? "已复制" : "复制"}
                    </button>
                </div>

                {paid ? (
                    <div className="wallet-pay-dialog-done">
                        <CheckCircle2 aria-hidden className="wallet-pay-dialog-done-icon" />
                        <h3>充值成功，积分已到账</h3>
                        <p>可关闭本窗口，在下方「充值订单」与积分流水里逐笔核对。</p>
                    </div>
                ) : settledOther ? (
                    <Callout tone="warning">{`这笔订单已${statusLabel[status] ?? "结束"}，请关闭窗口后重新下单。`}</Callout>
                ) : (
                    <>
                        <div className="wallet-pay-dialog-amount">
                            <span>应付金额</span>
                            <strong>{formatMoneyFen(order.payableFen)}</strong>
                            <span className="wallet-pay-dialog-credits">
                                到账 {formatCount(order.credits)} 积分
                                {order.giftCredits > 0 ? `（含赠送 ${formatCount(order.giftCredits)}）` : ""}
                            </span>
                        </div>

                        {prepareError ? (
                            <Callout tone="error" action={<Button size="small" onClick={() => setRetryKey((value) => value + 1)}>重试</Button>}>
                                {prepareError}
                            </Callout>
                        ) : preparing ? (
                            <p className="wallet-pay-dialog-hint">正在准备支付信息…</p>
                        ) : payUrl ? (
                            <div className="wallet-pay-dialog-redirect">
                                <p className="wallet-pay-dialog-hint">支付渠道已就绪，请在新开的收银台页面完成付款，完成后本窗口会自动更新。</p>
                                <Button type="primary" icon={<ExternalLink aria-hidden />} onClick={() => window.open(payUrl, "_blank", "noopener,noreferrer")}>
                                    打开收银台
                                </Button>
                            </div>
                        ) : qrImage ? (
                            <div className="wallet-pay-dialog-pay">
                                <img className="wallet-pay-dialog-qr" src={qrImage} alt="收款二维码" decoding="async" />
                                <div className="wallet-pay-dialog-steps">
                                    <h3>扫码付款</h3>
                                    <ol>
                                        <li>打开微信或支付宝，扫左侧二维码。</li>
                                        <li>转账金额与上方「应付金额」一致。</li>
                                        <li>
                                            备注里填订单号 <strong>{order.orderNo}</strong>。
                                        </li>
                                    </ol>
                                    <Callout tone="warning">不填订单号，运营无法判断这笔钱充到哪个账号。</Callout>
                                </div>
                            </div>
                        ) : (
                            <p className="wallet-pay-dialog-hint">这笔订单由运营人工确认到账，付款时请在备注里写明订单号，确认后积分自动入账。</p>
                        )}

                        <p className="wallet-pay-dialog-foot-note">本窗口每 4 秒自动查询一次到账状态，到账后积分立即入账；进度也可在下方「充值订单」查看。</p>
                    </>
                )}
            </div>
        </AppModal>
    );
}
