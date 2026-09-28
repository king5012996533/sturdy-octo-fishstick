import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

describe("用户端订阅 / 充值页", () => {
    test("导出 BillingPage，并且只依赖既有的用户端计费接口", () => {
        const page = read("src/pages/billing/index.tsx");
        expect(page).toContain("export function BillingPage");
        expect(page).toContain('from "@/services/api/billing"');
        expect(page).toContain("getBillingPlans(");
        expect(page).toContain("getBillingEntitlements(");
        expect(page).toContain("listMyBillingOrders(");
    });

    test("结算流程串起试算、下单与发起支付", () => {
        const page = read("src/pages/billing/index.tsx");
        expect(page).toContain("quoteBillingOrder(");
        expect(page).toContain("createBillingOrder(");
        expect(page).toContain("payBillingOrder(");
        expect(page).toContain("cancelBillingOrder(");
        // 有收银台地址时用新窗口打开支付渠道页面。
        expect(page).toContain('window.open(launch.payUrl, "_blank"');
        expect(page).toContain("支付完成后回来查看订单状态");
    });

    test("券不可用只提示不拦截，仍可按原价下单", () => {
        const page = read("src/pages/billing/index.tsx");
        expect(page).toContain("couponError");
        expect(page).toContain("仍可按原价下单");
        // MANUAL 渠道没有收银台，必须提示等待运营确认而不是报错。
        expect(page).toContain('launch.provider === "MANUAL"');
    });

    test("金额一律走 formatMoneyFen，反馈不依赖全局 message", () => {
        const page = read("src/pages/billing/index.tsx");
        expect(page).toContain("formatMoneyFen(");
        // 全局 antd message 在这个项目里是关闭的，反馈必须落在页面上。
        expect(page).not.toContain("message.success");
        expect(page).not.toContain("message.error");
        expect(page).not.toContain("App.useApp");
    });

    test("订单状态用中文 Tag 映射，待支付才能继续支付或取消", () => {
        const page = read("src/pages/billing/index.tsx");
        expect(page).toContain("待支付");
        expect(page).toContain("已支付");
        expect(page).toContain("已取消");
        expect(page).toContain("已退款");
        expect(page).toContain("失败");
        expect(page).toContain("PENDING:");
        expect(page).toContain("PAID:");
        expect(page).toContain("继续支付");
        expect(page).toContain("取消订单");
    });

    test("配额 0 是「不限」而不是「零」，且无订阅时给引导文案", () => {
        const page = read("src/pages/billing/index.tsx");
        expect(page).toContain('return "不限"');
        expect(page).toContain("订阅后即可使用平台模型，无需自备密钥");
    });
});
