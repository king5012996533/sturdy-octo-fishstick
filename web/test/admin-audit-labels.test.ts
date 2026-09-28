import { describe, expect, test } from "bun:test";

import { auditActionGroup } from "../src/features/admin-console/audit-pane";

/**
 * 审计动作中文归类。
 *
 * 归类靠前缀匹配、且顺序即优先级，两个坑都不报错：漏一个前缀只是少个中文标签，
 * 顺序写反则会静默分错组。这里把后端当前会写出的动作串钉住。
 * 新增写操作时同步补在这里，动作串以后端 recordAudit/appendAdminAudit 为准。
 */
const backendActions = [
    // internal/hosted
    "agreement.publish",
    "asset.moderate",
    "canvas.moderation",
    "coupon.create",
    "coupon.delete",
    "coupon.update",
    "gateway.test",
    "gateway.update",
    "login-method.update",
    "order.mark-paid",
    "order.refund",
    "payment-channel.update",
    "plan.create",
    "plan.delete",
    "plan.update",
    "role.create",
    "role.delete",
    "role.update",
    "subscription.grant",
    "template.create",
    "template.delete",
    "template.update",
    "ticket.reply",
    "ticket.status",
    "user.logout",
    "user.password",
    "user.role",
    "user.roles.update",
    "user.status",
    // internal/app + internal/platform
    "api_log.query_provider_task",
    "appearance.reset",
    "appearance.update",
    "feature_availability.update",
    "logical_model.archive",
    "logical_model.create",
    "logical_model.update",
    "plugin.availability.update",
    "plugin.install",
    "plugin.uninstall",
    "response_interception.update",
    "runtime_policy.reset",
    "runtime_policy.update",
];

describe("审计日志动作归类", () => {
    test("后端会写出的动作全部有中文分组", () => {
        const unmapped = backendActions.filter((action) => auditActionGroup(action).label === action);
        expect(unmapped).toEqual([]);
    });

    test("同类动作落到同一分组，且不会跨组串味", () => {
        expect(auditActionGroup("user.roles.update").label).toBe("角色分配");
        expect(auditActionGroup("user.role").label).toBe("账号角色");
        expect(auditActionGroup("payment-channel.update").label).toBe("支付渠道");
        expect(auditActionGroup("channel.update").label).toBe("渠道与模型");
        expect(auditActionGroup("gateway.update").label).toBe("聚合网关");
    });

    test("未知动作原样回显，便于发现遗漏", () => {
        expect(auditActionGroup("mystery.action").label).toBe("mystery.action");
        expect(auditActionGroup("mystery.action").color).toBe("default");
    });
});
