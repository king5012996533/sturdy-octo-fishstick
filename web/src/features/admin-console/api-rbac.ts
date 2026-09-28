import { http } from "@/services/api/request";

/**
 * 角色与权限接口（/api/admin/roles、/api/admin/users/:id/roles）。
 *
 * 与 api.ts 分开成独立文件：RBAC 是独立模块（后端也是一组独立文件），合并方按需接线，
 * 避免在共享的 api.ts 上产生冲突。这里的路径与后端 registerAdminRbacRoutes 一一对应。
 *
 * 这一层只做「类型 + 路径」映射，权限点是否合法、内置角色能否删除、角色并集怎么算，
 * 全部由服务端判定；前端能做的只是把服务端返回的原因原样展示出来。
 */

export type AdminPermission = {
    code: string;
    name: string;
    /** 分组只影响展示，后台按组做多选。 */
    group: string;
};

export type AdminRole = {
    id: string;
    code: string;
    name: string;
    description: string;
    /** 内置角色：不可删除、不可改标识。 */
    builtin: boolean;
    permissions: string[];
    /** 引用该角色的账号数，用来在删除前提示影响面。 */
    userCount: number;
    createdAt: string;
    updatedAt: string;
};

export type AdminRoleInput = {
    id?: string;
    code: string;
    name: string;
    description?: string;
    permissions: string[];
};

export type AdminUserRoles = {
    userId: string;
    roleCodes: string[];
    /** 角色权限并集，服务端已去重并按目录顺序排好。 */
    permissions: string[];
};

export function listAdminPermissions() {
    return http.get<{ permissions: AdminPermission[] }>("/admin/permissions");
}

export function listAdminRoles() {
    return http.get<{ roles: AdminRole[] }>("/admin/roles");
}

export function createAdminRole(input: AdminRoleInput) {
    return http.post<{ role: AdminRole }>("/admin/roles", input);
}

export function updateAdminRole(id: string, input: AdminRoleInput) {
    return http.put<{ role: AdminRole }>(`/admin/roles/${encodeURIComponent(id)}`, input);
}

export function deleteAdminRole(id: string) {
    return http.delete<{ id: string }>(`/admin/roles/${encodeURIComponent(id)}`);
}

export function getAdminUserRoles(userId: string) {
    return http.get<AdminUserRoles>(`/admin/users/${encodeURIComponent(userId)}/roles`);
}

export function assignAdminUserRoles(userId: string, roleCodes: string[]) {
    return http.put<AdminUserRoles>(`/admin/users/${encodeURIComponent(userId)}/roles`, { roleCodes });
}
