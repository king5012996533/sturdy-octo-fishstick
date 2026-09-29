import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

/**
 * 积分中心是唯一的充值入口。
 *
 * 这一组盯的是"入口只有一处"：旧的订阅与充值页已经下线，任何一处把 /billing 的导航项
 * 或页面重新加回来，用户就会重新看到两个在卖同一批货的入口——而平台的计费只有一个口径
 * （积分），多出来的那个入口只会让人以为还要买别的东西。
 */
describe("积分中心", () => {
    test("充值与订阅只有一个入口", () => {
        const sidebar = read("src/components/layout/workspace-sidebar-nav.tsx");
        expect(sidebar).toContain('to: "/wallet"');
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
        expect(orders).toContain("payBillingOrder(");
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
});
