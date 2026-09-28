import { http } from "@/services/api/request";

/**
 * 用户端工单接口（/api/support/tickets/*）。
 *
 * 会话即身份：请求体与查询串里都不带 userId，所以"查我的工单"不可能是"遍历别人的工单"。
 * 状态流转、越权与已关闭判定全部由服务端裁决，前端只负责展示与提交。
 */

export type SupportTicketStatus = "OPEN" | "PROCESSING" | "RESOLVED" | "CLOSED";

export type SupportTicketCategory = "BUG" | "BILLING" | "FEATURE" | "OTHER";

export type SupportTicketReplyRole = "USER" | "STAFF";

export type SupportTicketReply = {
    id: string;
    authorId: string;
    authorRole: SupportTicketReplyRole;
    authorName: string;
    body: string;
    createdAt: string;
};

export type SupportTicket = {
    id: string;
    ticketNo: string;
    userId: string;
    userName: string;
    userEmail: string;
    userPhone: string;
    contact: string;
    category: SupportTicketCategory;
    title: string;
    body: string;
    status: SupportTicketStatus;
    assigneeId: string;
    createdAt: string;
    updatedAt: string;
    closedAt: string | null;
    replies: SupportTicketReply[];
};

export type SupportTicketPage = {
    tickets: SupportTicket[];
    total: number;
    page: number;
    pageSize: number;
};

export type SupportTicketInput = {
    category: SupportTicketCategory;
    title: string;
    body: string;
    /** 联系方式可选：方便客服在账号之外联系用户。 */
    contact?: string;
};

export function listMyTickets(options: { page?: number; pageSize?: number } = {}) {
    return http.get<SupportTicketPage>("/support/tickets", {
        params: { page: options.page, pageSize: options.pageSize },
    });
}

export async function createTicket(input: SupportTicketInput) {
    const payload = await http.post<{ ticket: SupportTicket }>("/support/tickets", input);
    return payload.ticket;
}

export async function getTicket(id: string) {
    const payload = await http.get<{ ticket: SupportTicket }>(`/support/tickets/${encodeURIComponent(id)}`);
    return payload.ticket;
}

export async function replyTicket(id: string, body: string) {
    const payload = await http.post<{ ticket: SupportTicket }>(`/support/tickets/${encodeURIComponent(id)}/replies`, { body });
    return payload.ticket;
}
