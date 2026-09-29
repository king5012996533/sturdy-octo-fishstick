import { http } from "@/services/api/request";

/**
 * 用户端计费接口（套餐、下单、我的订单）。
 *
 * 金额一律是"分"的整数，格式化只发生在展示层：把元转成浮点再参与计算，会在折扣与
 * 对账时丢精度，而这里每一分都要能和支付渠道的流水对上。
 */

export type BillingPlan = {
    id: string;
    code: string;
    name: string;
    description: string;
    sortOrder: number;
    enabled: boolean;
    priceFen: number;
    periodDays: number;
    /** 购买后到账的积分（整数分）。periodDays 为 0 且这里大于 0 时是纯积分包。 */
    credits: number;
    /** 平台额外赠送的积分，与到账积分分开入账。 */
    giftCredits: number;
    quotaCalls: number;
    quotaStorageMb: number;
    quotaMembers: number;
    createdAt: string;
    updatedAt: string;
};

export type BillingEntitlements = {
    active: boolean;
    planCode: string;
    planName: string;
    expiresAt?: string;
    quotaCalls: number;
    quotaStorageMb: number;
    quotaMembers: number;
};

export type BillingOrderStatus = "PENDING" | "PAID" | "CANCELED" | "REFUNDED" | "FAILED";

export type BillingOrder = {
    id: string;
    orderNo: string;
    userId: string;
    userName: string;
    userEmail: string;
    userPhone: string;
    planId: string;
    planCode: string;
    planName: string;
    amountFen: number;
    discountFen: number;
    payableFen: number;
    couponCode: string;
    /** 下单时的积分快照：套餐后来改价改赠送，这笔已付订单仍按当时承诺到账。 */
    credits: number;
    giftCredits: number;
    status: BillingOrderStatus;
    provider: string;
    providerOrderNo: string;
    paidAt?: string;
    expiresAt: string;
    remark: string;
    createdAt: string;
};

export type BillingOrderPage = {
    orders: BillingOrder[];
    total: number;
    page: number;
    pageSize: number;
};

export type BillingCouponQuote = {
    amountFen: number;
    discountFen: number;
    payableFen: number;
    /** 券不可用时的原因；用于在结算面板里就地提示，而不是等下单失败才报错。 */
    couponError?: string;
};

export type BillingPaymentLaunch = {
    order: BillingOrder;
    provider: string;
    /** 聚合网关返回的收银台地址；MANUAL 渠道为空，由运营手工补单。 */
    payUrl?: string;
    /** 渠道自定义参数（例如二维码内容），原样透传给前端渲染。 */
    payParams?: Record<string, string>;
};

export async function getBillingPlans() {
    const payload = await http.get<{ plans: BillingPlan[] }>("/finance/plans");
    return payload.plans ?? [];
}

export async function getBillingEntitlements() {
    const payload = await http.get<{ entitlements: BillingEntitlements }>("/finance/entitlements");
    return payload.entitlements;
}

export async function quoteBillingOrder(input: { planCode: string; couponCode?: string }) {
    const payload = await http.post<{ quote: BillingCouponQuote }>("/finance/coupons/quote", input);
    return payload.quote;
}

export async function createBillingOrder(input: { planCode: string; couponCode?: string }) {
    const payload = await http.post<{ order: BillingOrder }>("/payments/orders", input);
    return payload.order;
}

export async function listMyBillingOrders(options: { page?: number; pageSize?: number } = {}) {
    return http.get<BillingOrderPage>("/payments/orders", {
        params: { page: options.page, pageSize: options.pageSize },
    });
}

export async function getBillingOrder(id: string) {
    const payload = await http.get<{ order: BillingOrder }>(`/payments/orders/${encodeURIComponent(id)}`);
    return payload.order;
}

export async function cancelBillingOrder(id: string) {
    const payload = await http.post<{ order: BillingOrder }>(`/payments/orders/${encodeURIComponent(id)}/cancel`);
    return payload.order;
}

/** 发起支付：返回聚合网关收银台地址（或手工补单渠道的空结果）。 */
export async function payBillingOrder(id: string) {
    return http.post<BillingPaymentLaunch>(`/payments/orders/${encodeURIComponent(id)}/pay`);
}

/** 分 → 元，仅用于展示。 */
export function formatMoneyFen(fen: number) {
    const value = Number.isFinite(fen) ? fen : 0;
    return `¥${(value / 100).toFixed(2)}`;
}
