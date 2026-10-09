import { QueryClient, hashKey, type QueryKey } from "@tanstack/react-query";

import { getActiveUserScope } from "@/lib/user-scope";

/**
 * 把当前用户 scope 并入查询键的哈希。
 *
 * 查询键（如 ["projects","paged"]、["project-folders"]、["style-profiles"]）本身不含用户
 * 维度，此前只靠登录/登出整页跳转把内存缓存清掉来保证不串号。这条防线很脆：一旦出现页面内
 * 的账号切换，前一个账号的缓存会被后一个账号直接读到。
 *
 * 在哈希层统一加 scope，既不用逐个调用点改键，新加的查询也自动隔离。用 TanStack 自己的
 * hashKey 而不是 JSON.stringify，是为了保留"对象键顺序无关"的既有语义，避免命中率下降。
 */
export function scopedQueryKeyHash(queryKey: QueryKey): string {
    return hashKey([getActiveUserScope(), ...queryKey]);
}

export const appQueryClient = new QueryClient({
    defaultOptions: {
        queries: {
            staleTime: 30_000,
            retry: false,
            refetchOnWindowFocus: false,
            queryKeyHashFn: scopedQueryKeyHash,
        },
    },
});
