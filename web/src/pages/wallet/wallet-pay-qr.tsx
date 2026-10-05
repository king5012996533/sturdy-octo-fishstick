import { useAppearanceStore } from "@/stores/use-appearance-store";

import { Callout } from "@/components/ui/product/callout";

import { WalletPanel } from "./wallet-kit";

/**
 * 充值区的收款二维码（运营在后台「站点设置 → 品牌与收款资源」上传）。
 *
 * 这是支付渠道接通前的兜底路径，所以它必须把"下单 → 扫码付款 → 运营确认"三步讲清楚；
 * 只放一张图，用户扫完码不知道该把订单号给谁，反而会产生新的客诉。
 *
 * 没配置就整块不渲染：没有收款码时这里不是"占位"，而是这条兜底路径根本不成立。
 */
export function TopUpPaymentQR() {
    const configured = useAppearanceStore((state) => state.appearance.paymentQrConfigured);
    const qrUrl = useAppearanceStore((state) => state.appearance.paymentQrUrl);
    if (!configured || !qrUrl) return null;

    return (
        <WalletPanel className="wallet-pay-qr">
            <img className="wallet-pay-qr-image" src={qrUrl} alt="充值收款二维码" loading="lazy" decoding="async" />
            <div className="wallet-pay-qr-copy">
                <h3>扫码充值（人工确认到账）</h3>
                <ol>
                    <li>先在上方选择档位并下单，记下订单号。</li>
                    <li>扫码付款，金额与订单金额一致。</li>
                </ol>
                {/* 备注是这条兜底路径唯一的对账依据：不写订单号，钱到了也对不出是哪一单该给谁充。 */}
                <Callout tone="warning">务必在付款备注里写上订单号，否则运营无法确认这笔钱充到哪个账号。</Callout>
                <p className="wallet-pay-qr-after">运营核对到账后积分自动入账，进度可在下方「充值订单」查看。</p>
            </div>
        </WalletPanel>
    );
}
