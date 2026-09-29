import { App, ConfigProvider, theme as antdTheme } from "antd";
import { ArrowLeft, BadgePercent, Boxes, Coins, FileSignature, Gauge, Images, KeyRound, LayoutDashboard, LayoutTemplate, LifeBuoy, Package, RadioTower, Receipt, ScrollText, Send, Settings, ShieldAlert, ShieldCheck, SlidersHorizontal, Sparkles, TicketPercent, Users, type LucideIcon } from "lucide-react";
import { useMemo } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { useAppearanceStore } from "@/stores/use-appearance-store";
import { useUserStore } from "@/stores/use-user-store";

import "./admin-console.css";
import { AgreementsPane } from "./agreements-pane";
import { AssetsPane } from "./assets-pane";
import { AuditPane } from "./audit-pane";
import { CanvasPane } from "./canvas-pane";
import { ChannelsPane } from "./channels-pane";
import { CouponsPane } from "./coupons-pane";
import { CreditsPane } from "./credits-pane";
import { DashboardPane } from "./dashboard-pane";
import { FeaturesPane } from "./features-pane";
import { GatewaysPane } from "./gateways-pane";
import { InspirationsPane } from "./inspirations-pane";
import { LoginMethodsPane } from "./login-methods-pane";
import { OrdersPane } from "./orders-pane";
import { PolicyPane } from "./policy-pane";
import { PlansPane } from "./plans-pane";
import { PricingPane } from "./pricing-pane";
import { RolesPane } from "./roles-pane";
import { SettingsPane } from "./settings-pane";
import { TemplatesPane } from "./templates-pane";
import { TicketsPane } from "./tickets-pane";
import { UsersPane } from "./users-pane";
import { VendorsPane } from "./vendors-pane";

type ConsoleSectionKey = "dashboard" | "users" | "roles" | "canvases" | "assets" | "templates" | "inspirations" | "login-methods" | "agreements" | "gateways" | "plans" | "orders" | "credits" | "coupons" | "tickets" | "settings" | "vendors" | "channels" | "pricing" | "features" | "policy" | "audit";

const consoleSections: Array<{ key: ConsoleSectionKey; label: string; description: string; icon: LucideIcon; pane: () => React.JSX.Element }> = [
    { key: "dashboard", label: "仪表盘", description: "用户、调用量与存储读数", icon: LayoutDashboard, pane: () => <DashboardPane /> },
    { key: "users", label: "用户管理", description: "账号、角色与封禁", icon: Users, pane: () => <UsersPane /> },
    { key: "roles", label: "角色与权限", description: "角色定义与权限点分配", icon: ShieldCheck, pane: () => <RolesPane /> },
    { key: "canvases", label: "内容审核", description: "画布内容与处置", icon: ShieldAlert, pane: () => <CanvasPane /> },
    { key: "assets", label: "素材管理", description: "上传素材与处置状态", icon: Images, pane: () => <AssetsPane /> },
    { key: "templates", label: "模板管理", description: "画布模板上下架与推荐位", icon: LayoutTemplate, pane: () => <TemplatesPane /> },
    { key: "inspirations", label: "精选灵感", description: "首页广场内容与推荐位", icon: Sparkles, pane: () => <InspirationsPane /> },
    { key: "login-methods", label: "登录方式", description: "验证码、密码与第三方通道", icon: KeyRound, pane: () => <LoginMethodsPane /> },
    { key: "agreements", label: "协议管理", description: "条款版本与签署留痕", icon: FileSignature, pane: () => <AgreementsPane /> },
    { key: "gateways", label: "验证码网关", description: "邮件与短信投递通道", icon: Send, pane: () => <GatewaysPane /> },
    { key: "plans", label: "订阅套餐", description: "可售套餐与配额", icon: Package, pane: () => <PlansPane /> },
    { key: "orders", label: "订单管理", description: "收款、补单与退款", icon: Receipt, pane: () => <OrdersPane /> },
    { key: "credits", label: "积分管理", description: "余额、流水与人工调整", icon: Coins, pane: () => <CreditsPane /> },
    { key: "coupons", label: "优惠券", description: "折扣券与核销记录", icon: TicketPercent, pane: () => <CouponsPane /> },
    { key: "tickets", label: "工单与反馈", description: "用户反馈与处理流转", icon: LifeBuoy, pane: () => <TicketsPane /> },
    { key: "settings", label: "站点设置", description: "品牌、Logo 与备案信息", icon: Settings, pane: () => <SettingsPane /> },
    // 厂商是配置入口，渠道是它的执行层：先用「模型厂商」把上游与密钥挂进来，再看
    // 「渠道与模型」里落成的 system channel，顺序反过来会让人以为要手填裸地址。
    { key: "vendors", label: "模型厂商", description: "上游厂商、凭据与模型目录", icon: Boxes, pane: () => <VendorsPane /> },
    { key: "channels", label: "渠道与模型", description: "上游地址、密钥与可售模型", icon: RadioTower, pane: () => <ChannelsPane /> },
    { key: "pricing", label: "模型定价", description: "计费倍率与模型单价", icon: BadgePercent, pane: () => <PricingPane /> },
    { key: "features", label: "功能开放", description: "决定前台形态的开关", icon: SlidersHorizontal, pane: () => <FeaturesPane /> },
    { key: "policy", label: "运行时策略", description: "配额、超时与频控", icon: Gauge, pane: () => <PolicyPane /> },
    { key: "audit", label: "审计日志", description: "管理员写操作留痕", icon: ScrollText, pane: () => <AuditPane /> },
];

function isConsoleSectionKey(value: string | null): value is ConsoleSectionKey {
    return consoleSections.some((section) => section.key === value);
}

/**
 * 平台运营后台。
 *
 * 独立于用户工作台渲染（AppWorkspaceShell 对 /admin 隐藏侧栏与顶栏）：管理员在这里
 * 配置的是全平台的模型来源，和"我自己的工作区"是两件事，混在同一条导航里会让
 * 后台写操作看起来像个人设置。
 */
export function AdminConsolePage() {
    const navigate = useNavigate();
    const [searchParams, setSearchParams] = useSearchParams();
    const brandName = useAppearanceStore((state) => state.appearance.brandName);
    const user = useUserStore((state) => state.user);

    const requested = searchParams.get("section");
    const activeSection = isConsoleSectionKey(requested) ? requested : "dashboard";
    const activePane = useMemo(() => consoleSections.find((section) => section.key === activeSection)?.pane(), [activeSection]);

    const selectSection = (key: ConsoleSectionKey) => {
        const next = new URLSearchParams(searchParams);
        next.set("section", key);
        setSearchParams(next, { replace: true });
    };

    return (
        <ConfigProvider
            // 后台恒为暗色：渠道与密钥表格在浅底上会把状态色和危险操作压得看不清，
            // 而且这块界面与登录页共用一套皮肤令牌。
            theme={{
                algorithm: antdTheme.darkAlgorithm,
                token: { borderRadius: 8, fontSize: 13, controlHeight: 32, colorBgElevated: "#0d0f13", colorBorder: "rgba(255,255,255,0.14)" },
            }}
        >
            <App message={{ duration: 3, maxCount: 3 }}>
                <div className="admin-console">
                    <header className="admin-console-topbar">
                        <div className="admin-console-brand">
                            <span className="admin-console-brand-mark" aria-hidden>
                                {(brandName || "K").slice(0, 1).toUpperCase()}
                            </span>
                            <span className="admin-console-brand-text">
                                <b className="admin-console-wordmark">{brandName} 管理后台</b>
                                <span className="admin-console-mono">platform console</span>
                            </span>
                        </div>
                        <div className="admin-console-topbar-actions">
                            {user ? (
                                <span className="admin-console-identity" title={user.displayName || user.username}>
                                    <span className="admin-console-identity-avatar" aria-hidden>
                                        {(user.displayName || user.username || "A").slice(0, 1).toUpperCase()}
                                    </span>
                                    <span className="admin-console-identity-name">{user.displayName || user.username}</span>
                                </span>
                            ) : null}
                            <button type="button" className="admin-console-rail-item" onClick={() => navigate("/")}>
                                <ArrowLeft className="size-4 shrink-0" />
                                <span>返回工作台</span>
                            </button>
                        </div>
                    </header>

                    <div className="admin-console-main">
                        <nav className="admin-console-rail" aria-label="管理后台分区">
                            <span className="admin-console-rail-heading admin-console-mono">配置分区</span>
                            {consoleSections.map((section) => {
                                const Icon = section.icon;
                                const active = section.key === activeSection;
                                return (
                                    <button
                                        key={section.key}
                                        type="button"
                                        className={`admin-console-rail-item${active ? " is-active" : ""}`}
                                        aria-current={active ? "page" : undefined}
                                        onClick={() => selectSection(section.key)}
                                    >
                                        <Icon className="size-4 shrink-0" />
                                        <span className="flex min-w-0 flex-col">
                                            <span>{section.label}</span>
                                            <span className="admin-console-rail-item-desc">{section.description}</span>
                                        </span>
                                    </button>
                                );
                            })}
                        </nav>

                        <main className="admin-console-stage">{activePane}</main>
                    </div>
                </div>
            </App>
        </ConfigProvider>
    );
}
