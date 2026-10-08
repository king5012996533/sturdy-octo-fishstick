import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");
const lines = (path: string) => read(path).split("\n").length;

/**
 * 积分中心是唯一的充值入口。
 *
 * 这一组盯的是"入口只有一处"：旧的订阅与充值页已经下线，任何一处把 /billing 的导航项
 * 或页面重新加回来，用户就会重新看到两个在卖同一批货的入口——而平台的计费只有一个口径
 * （积分），多出来的那个入口只会让人以为还要买别的东西。
 */
describe("积分中心", () => {
    test("充值与订阅只有一个入口", () => {
        // 充值入口收在账户菜单里，不在侧栏：侧栏只留工作区入口（首页/项目/资产）。
        const accountMenu = read("src/features/hosted-auth/sidebar-footer.tsx");
        expect(accountMenu).toContain('to="/wallet"');
        expect(accountMenu).not.toContain("/billing");
        const sidebar = read("src/components/layout/workspace-sidebar-nav.tsx");
        expect(sidebar).not.toContain('to: "/wallet"');
        expect(sidebar).not.toContain("/billing");
        expect(sidebar).not.toContain("订阅与充值");

        const router = read("src/router.tsx");
        expect(router).toContain('import("@/pages/wallet")');
        expect(router).not.toContain('import("@/pages/billing")');
        // 旧链接保留一次重定向，既不 404 也不再渲染订阅页。
        expect(router).toContain('{ path: "/billing", element: <Navigate to="/wallet" replace /> }');
    });

    test("充值货架只卖纯积分包，不把订阅当积分卖", () => {
        const api = read("src/services/api/credit.ts");
        expect(api).toContain("plan.periodDays === 0");
        expect(api).toContain("plan.credits + plan.giftCredits > 0");
    });

    test("订单区承接充值凭据：待支付才能继续支付或取消", () => {
        const orders = read("src/pages/wallet/wallet-orders.tsx");
        expect(orders).toContain("listMyBillingOrders(");
        // 继续支付必须走页面级支付弹窗：订单区再实现一遍收银台，就会出现两套二维码与两套轮询。
        expect(orders).toContain("onOpenPayment(");
        expect(orders).not.toContain("payBillingOrder(");
        expect(orders).toContain("cancelBillingOrder(");
        expect(orders).toContain("继续支付");
        expect(orders).toContain("取消订单");
        expect(orders).toContain('order.status === "PENDING"');
        // 金额一律走 formatMoneyFen；全局 antd message 在本项目里是关闭的，反馈落在页面上。
        expect(orders).toContain("formatMoneyFen(");
        expect(orders).not.toContain("message.success");
        expect(orders).not.toContain("App.useApp");
    });

    test("订单状态用中文 Tag 映射，未知状态不炸行", () => {
        const orders = read("src/pages/wallet/wallet-orders.tsx");
        for (const label of ["待支付", "已支付", "已取消", "已退款", "失败"]) {
            expect(orders).toContain(label);
        }
        expect(orders).toContain("statusView(");
    });

    test("积分中心把订单区挂在充值区之下、流水区之上", () => {
        const page = read("src/pages/wallet/index.tsx");
        const topUp = page.indexOf("<CreditTopUpSection");
        const orders = page.indexOf("<CreditOrdersSection");
        const ledger = page.indexOf("<CreditLedgerSection");
        expect(topUp).toBeGreaterThan(-1);
        expect(orders).toBeGreaterThan(topUp);
        expect(ledger).toBeGreaterThan(orders);
    });

    test("四个 Zone 各占一个文件，页面只做编排", () => {
        const page = read("src/pages/wallet/index.tsx");
        expect(page).toContain('from "./wallet-balance"');
        // 余额卡曾经整段写在页面文件里：页面既是编排又是实现，改一处排版要通读四种数据流。
        expect(page).not.toContain("wallet-metric");
        for (const file of ["index", "wallet-balance", "wallet-top-up", "wallet-orders", "wallet-ledger", "wallet-pay-dialog"]) {
            expect(lines(`src/pages/wallet/${file}.tsx`)).toBeLessThan(400);
        }
    });

    test("收款码不再常驻充值区，改由点击「立即充值」后的支付弹窗承接", () => {
        const topUp = read("src/pages/wallet/wallet-top-up.tsx");
        expect(topUp).not.toContain("TopUpPaymentQR");
        expect(topUp).toContain("onOpenPayment(order)");
        // 弹窗挂在页面级：档位卡只负责下单，付款界面只有一份实现。
        const page = read("src/pages/wallet/index.tsx");
        expect(page).toContain("<WalletPayDialog");
        expect(page).toContain("onOpenPayment={setPayOrder}");
    });

    test("支付弹窗把订单号、金额、收款码、到账轮询放在一处", () => {
        const dialog = read("src/pages/wallet/wallet-pay-dialog.tsx");
        for (const label of ["订单号", "应付金额", "扫码付款", "刷新支付状态", "取消订单", "已复制"]) {
            expect(dialog).toContain(label);
        }
        expect(dialog).toContain("payBillingOrder(");
        expect(dialog).toContain("getBillingOrder(");
        expect(dialog).toContain("cancelBillingOrder(");
        // 备注订单号是手工渠道唯一的对账依据，付款界面必须把它讲明白。
        expect(dialog).toContain("备注里填订单号");
        expect(dialog).toContain("window.setInterval");
    });

    test("同一档位已有待支付订单时不再新开一单", () => {
        const topUp = read("src/pages/wallet/wallet-top-up.tsx");
        expect(topUp).toContain("listMyBillingOrders(");
        expect(topUp).toContain('order.status === "PENDING"');
        expect(topUp).toContain("onOpenPayment(pending)");
    });

    test("余额卡是一张卡，卡里不再套第二个盒子", () => {
        const balance = read("src/pages/wallet/wallet-balance.tsx");
        const css = read("src/pages/wallet/wallet-product.css");
        for (const label of ["剩余积分", "累计获得", "累计消耗", "充值", "查看积分流水"]) {
            expect(balance).toContain(label);
        }
        // 累计数与读数靠一道 hairline 分栏，而不是在卡里再画一个带描边的框——
        // 盒子套盒子正是这一块看起来像后台表单的原因。
        expect(css).toContain(".wallet-balance-stats");
        expect(css).not.toContain(".wallet-metrics");
    });
});
