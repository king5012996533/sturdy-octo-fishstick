import localforage from "localforage";
import type { StateStorage } from "zustand/middleware";

import { getActiveUserScope, scopedStorageKey } from "@/lib/user-scope";

localforage.config({
    name: "infinite-canvas",
    storeName: "app_state",
});

export function localForageStorageForScope(scope?: string): StateStorage {
    const keyFor = (name: string) => scopedStorageKey(name, scope);
    return {
        getItem: async (name) => {
            if (typeof window === "undefined") return null;
            return (await localforage.getItem<string>(keyFor(name))) || null;
        },
        setItem: async (name, value) => {
            if (typeof window === "undefined") return;
            await localforage.setItem(keyFor(name), value);
        },
        removeItem: async (name) => {
            if (typeof window === "undefined") return;
            await localforage.removeItem(keyFor(name));
        },
    };
}

export const localForageStorage: StateStorage = localForageStorageForScope();

/**
 * 列出某个作用域下以 `prefix` 开头的存储名（不含作用域后缀）。
 * 用于在本地记录损坏、拿不到索引时按前缀找回关联数据。
 */
export async function listScopedStorageNames(prefix: string, scope = getActiveUserScope()): Promise<string[]> {
    if (typeof window === "undefined") return [];
    const suffix = `:user:${scope}`;
    const keys = await localforage.keys();
    return keys
        .filter((key) => key.endsWith(suffix))
        .map((key) => key.slice(0, -suffix.length))
        .filter((name) => name.startsWith(prefix));
}
