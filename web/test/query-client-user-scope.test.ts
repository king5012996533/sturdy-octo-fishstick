import { afterEach, describe, expect, test } from "bun:test";

import { appQueryClient, scopedQueryKeyHash } from "@/lib/query-client";

function setScope(scope: string) {
    (globalThis as unknown as { window: unknown }).window = {
        localStorage: {
            getItem: () => scope,
            setItem: () => {},
            removeItem: () => {},
        },
    };
}

afterEach(() => {
    delete (globalThis as unknown as { window?: unknown }).window;
});

describe("查询键的 user scope 隔离", () => {
    test("同一 scope 下相同键得到相同哈希", () => {
        setScope("user-a");
        expect(scopedQueryKeyHash(["projects", "paged"])).toBe(scopedQueryKeyHash(["projects", "paged"]));
    });

    test("不同 scope 下相同键得到不同哈希（这是不串号的关键）", () => {
        setScope("user-a");
        const a = scopedQueryKeyHash(["projects", "paged"]);
        setScope("user-b");
        const b = scopedQueryKeyHash(["projects", "paged"]);
        expect(a).not.toBe(b);
    });

    test("对象键顺序无关，保留 TanStack 原有哈希语义", () => {
        setScope("user-a");
        expect(scopedQueryKeyHash(["tasks", { status: "running", page: 1 }])).toBe(scopedQueryKeyHash(["tasks", { page: 1, status: "running" }]));
    });

    test("未登录（guest）也自成一组，不与真实用户混用", () => {
        setScope("guest");
        const guest = scopedQueryKeyHash(["projects"]);
        setScope("user-a");
        expect(guest).not.toBe(scopedQueryKeyHash(["projects"]));
    });
});

describe("QueryClient 已挂上按 scope 的哈希", () => {
    test("默认查询选项里带 queryKeyHashFn", () => {
        expect(appQueryClient.getDefaultOptions().queries?.queryKeyHashFn).toBe(scopedQueryKeyHash);
    });
});
