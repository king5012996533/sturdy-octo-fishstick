import { http } from "@/services/api/request";

export type McpTokenScope = "agent" | "canvas" | "tasks" | "models";

export type McpToken = {
    id: string;
    name: string;
    prefix: string;
    grantId?: string;
    scopes: McpTokenScope[];
    permissionMode?: "read_only" | "request_approval" | "auto";
    maxCreditsPerRun?: number;
    maxCreditsPerDay?: number;
    expiresAt?: string;
    lastUsedAt?: string;
    createdAt: string;
    revokedAt?: string;
};

export type McpStatus = {
    enabled: boolean;
    baseUrl: string;
    tokenCount: number;
};

export type CreateMcpTokenInput = {
    name: string;
    expiresInDays: number;
    permissionMode?: "read_only" | "request_approval" | "auto";
    maxCreditsPerRun?: number;
    maxCreditsPerDay?: number;
};

export type CreateMcpTokenResult = {
    token: string;
    tokenView: McpToken;
};

export type AgentBudgetRequest = {
    id: string;
    grantId: string;
    runId: string;
    requestedCredits: number;
    reason: string;
    status: "pending" | "approved" | "denied";
    decidedAt?: string;
    createdAt: string;
};

export function getMcpStatus() {
    return http.get<McpStatus>("/integrations/mcp/status");
}

export function listMcpTokens() {
    return http.get<{ tokens: McpToken[] }>("/integrations/mcp/tokens");
}

export function createMcpToken(input: CreateMcpTokenInput) {
    return http.post<CreateMcpTokenResult>("/integrations/mcp/tokens", input);
}

export function revokeMcpToken(id: string) {
    return http.delete<{ revoked: boolean }>(`/integrations/mcp/tokens/${encodeURIComponent(id)}`);
}

export function listAgentBudgetRequests() {
    return http.get<{ requests: AgentBudgetRequest[] }>("/agent/budget-requests");
}

export function approveAgentBudgetRequest(id: string) {
    return http.post<{ grant: unknown; resumeRequired: boolean }>(
        `/agent/budget-requests/${encodeURIComponent(id)}/approve`,
        {},
    );
}
