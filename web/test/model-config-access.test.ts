import { afterEach, describe, expect, test } from "bun:test";

import { canPersistModelConfig } from "@/lib/model-config-access";

/**
 * 平台级模型配置只有托管管理员能写。这条判断一旦漏掉，普通账号会不断收到后端 403，
 * 界面上表现为反复"保存失败"；判断过严则会让管理员改不动渠道。
 */
const hostedFlag = "__BEEFTV_HOSTED_AUTH__";

function setHostedBuild(enabled: boolean) {
    (globalThis as Record<string, unknown>)[hostedFlag] = enabled;
}

afterEach(() => {
    delete (globalThis as Record<string, unknown>)[hostedFlag];
});

describe("canPersistModelConfig", () => {
    test("本地构建任何角色都能写", () => {
        setHostedBuild(false);
        expect(canPersistModelConfig("user")).toBe(true);
        expect(canPersistModelConfig("admin")).toBe(true);
        expect(canPersistModelConfig(null)).toBe(true);
    });

    test("托管形态只有管理员能写", () => {
        setHostedBuild(true);
        expect(canPersistModelConfig("admin")).toBe(true);
        expect(canPersistModelConfig("user")).toBe(false);
    });

    test("托管形态下未登录不允许写", () => {
        setHostedBuild(true);
        expect(canPersistModelConfig(null)).toBe(false);
        expect(canPersistModelConfig(undefined)).toBe(false);
    });
});
