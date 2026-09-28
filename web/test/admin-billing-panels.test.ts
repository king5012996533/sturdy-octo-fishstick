import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

const panePaths = {
    plans: "src/features/admin-console/plans-pane.tsx",
    orders: "src/features/admin-console/orders-pane.tsx",
    coupons: "src/features/admin-console/coupons-pane.tsx",
};

describe("后台计费面板", () => {
    test("三个面板文件存在，且导出名与合并方 import 的一致", () => {
        expect(existsSync(resolve(root, panePaths.plans))).toBe(true);
        expect(existsSync(resolve(root, panePaths.orders))).toBe(true);
        expect(existsSync(resolve(root, panePaths.coupons))).toBe(true);

        expect(read(panePaths.plans)).toContain("export function PlansPane()");
        expect(read(panePaths.orders)).toContain("export function OrdersPane()");
        expect(read(panePaths.coupons)).toContain("export function CouponsPane()");
    });

    test("全局 antd message 在项目里是关闭的，反馈必须落在页面上", () => {
        for (const path of Object.values(panePaths)) {
            const source = read(path);
            expect(source).not.toContain("message.success");
            expect(source).not.toContain("message.error");
            // 反馈统一走页内提示条。
            expect(source).toContain("admin-notice");
        }
    });

    test("订单面板用 formatMoneyFen 展示金额，指标卡读 page.revenue", () => {
        const orders = read(panePaths.orders);
        expect(orders).toContain("formatMoneyFen");
        expect(orders).toContain("revenue");
        expect(orders).toContain("paidAmountFen");
        expect(orders).toContain("paidOrders");
        expect(orders).toContain("pendingOrders");
        // 内部金额保持"分"，不允许在展示层之外把元当成计算单位。
        expect(orders).toContain("payableFen");
        expect(orders).toContain("markAdminBillingOrderPaid");
        expect(orders).toContain("refundAdminBillingOrder");
        expect(orders).toContain("仅用于渠道掉单时按真实到账补单");
    });

    test("订单面板的支付渠道区块密钥只写不读", () => {
        const orders = read(panePaths.orders);
        expect(orders).toContain("listAdminPaymentChannels");
        expect(orders).toContain("updateAdminPaymentChannel");
        expect(orders).toContain("已设置，留空表示不修改");
        expect(orders).toContain("未设置");
    });

    test("优惠券面板走核销记录接口，并写清折与万分比的换算", () => {
        const coupons = read(panePaths.coupons);
        expect(coupons).toContain("listAdminCouponRedemptions");
        expect(coupons).toContain("createAdminBillingCoupon");
        expect(coupons).toContain("updateAdminBillingCoupon");
        expect(coupons).toContain("deleteAdminBillingCoupon");
        expect(coupons).toContain("万分比");
        expect(coupons).toContain("8000");
    });

    test("套餐面板价格按元输入、提交前换算成分", () => {
        const plans = read(panePaths.plans);
        expect(plans).toContain("createAdminBillingPlan");
        expect(plans).toContain("updateAdminBillingPlan");
        expect(plans).toContain("deleteAdminBillingPlan");
        expect(plans).toContain("Math.round");
        expect(plans).toContain("priceFen");
        // code 校验：只允许小写字母数字连字符。
        expect(plans).toContain("/^[a-z0-9-]+$/");
    });

    test("删除动作都有二次确认，并如实展示服务端拒绝原因", () => {
        for (const path of [panePaths.plans, panePaths.coupons]) {
            const source = read(path);
            expect(source).toContain("删除");
            expect(source).toContain("确认删除");
            expect(source).toContain("取消");
            // 服务端拒绝（例如"该套餐已有订单，请改为停用"）要原样落到弹窗里，
            // 并引导运营改用「停用」而不是硬报错。
            expect(source).toContain("is-error");
            expect(source).toContain("改用");
            expect(source).toContain("停用");
        }
    });
});
