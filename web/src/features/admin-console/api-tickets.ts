import { http } from "@/services/api/request";

/**
 * 工单管理接口（/api/admin/tickets/*）。
 *
 * 这一层只做「类型 + 路径」的映射：状态流转是否合法、已关闭能不能回复都由服务端裁决，
 * 前端不自行判断，避免前后端对同一状态机各解释一次。
 */

export type AdminTicketStatus = "OPEN" | "PROCESSING" | "RESOLVED" | "CLOSED";

export type AdminTicketCategory = "BUG" | "BILLING" | "FEATURE" | "OTHER";

/** 回复作者角色：同一段对话里用户与客服的展示、判责完全不同。 */
export type AdminTicketReplyRole = "USER" | "STAFF";

export type AdminTicketReply = {
    id: string;
    authorId: string;
    authorRole: AdminTicketReplyRole;
    authorName: string;
    body: string;
    createdAt: string;
};

export type AdminTicket = {
    id: string;
    ticketNo: string;
    userId: string;
    userName: string;
    userEmail: string;
    userPhone: string;
    contact: string;
    category: AdminTicketCategory;
    title: string;
    body: string;
    status: AdminTicketStatus;
    assigneeId: string;
    createdAt: string;
    updatedAt: string;
    closedAt: string | null;
    replies: AdminTicketReply[];
};

/** 全量工单计数：指标卡读的是它，不随下方筛选变化。 */
export type AdminTicketCounts = {
    total: number;
    open: number;
    processing: number;
    resolved: number;
    closed: number;
};

export type AdminTicketPage = {
    tickets: AdminTicket[];
    total: number;
    page: number;
    pageSize: number;
    counts: AdminTicketCounts;
};

export function listAdminTickets(options: { status?: string; keyword?: string; page?: number; pageSize?: number } = {}) {
    return http.get<AdminTicketPage>("/admin/tickets", {
        params: {
            status: options.status || undefined,
            keyword: options.keyword?.trim() || undefined,
            page: options.page,
            pageSize: options.pageSize,
        },
    });
}

export async function getAdminTicket(id: string) {
    const payload = await http.get<{ ticket: AdminTicket }>(`/admin/tickets/${encodeURIComponent(id)}`);
    return payload.ticket;
}

/** 客服回复：首次回复会把「待处理」自动推进到「处理中」。 */
export async function replyAdminTicket(id: string, body: string) {
    const payload = await http.post<{ ticket: AdminTicket }>(`/admin/tickets/${encodeURIComponent(id)}/replies`, { body });
    return payload.ticket;
}

export async function updateAdminTicketStatus(id: string, status: AdminTicketStatus) {
    const payload = await http.patch<{ ticket: AdminTicket }>(`/admin/tickets/${encodeURIComponent(id)}/status`, { status });
    return payload.ticket;
}
