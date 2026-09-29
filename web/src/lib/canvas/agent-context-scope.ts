import type { AgentContextKey } from "@/components/canvas/canvas-cloud-agent-settings";
import { CANVAS_NODE_MENTION_PATTERN } from "@/lib/canvas/canvas-resource-references";

/**
 * 决定这一轮 Agent 请求实际携带的上下文范围。
 *
 * 输入框里出现 `@[node:<id>]` 就说明用户这一轮的目标是画布内容。此时若仍不带画布范围，
 * 服务端不会注册任何画布工具，Agent 只能回答"我取不到这张图"——把一个可修的问题伪装成
 * 能力缺失。所以节点引用必须就地补上画布范围，与技能引用自动生效的规则保持一致。
 */
export function resolveAgentContextScope(scope: AgentContextKey[], prompt: string): AgentContextKey[] {
    if (scope.includes("canvas")) return scope;
    if (!CANVAS_NODE_MENTION_PATTERN.test(prompt)) return scope;
    return [...scope, "canvas"];
}
