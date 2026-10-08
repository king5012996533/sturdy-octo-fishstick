import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";

import { AgentChatComposer, AgentWorkingMessage, agentWorkingLabel, type CloudAgentChatMessage } from "../src/components/canvas/canvas-cloud-agent-chat-ui";
import { canvasThemes } from "../src/lib/canvas-theme";

const chat = readFileSync(resolve(import.meta.dir, "../src/components/canvas/canvas-cloud-agent-chat-ui.tsx"), "utf8");
const panel = readFileSync(resolve(import.meta.dir, "../src/components/canvas/canvas-cloud-agent-panel.tsx"), "utf8");
const css = readFileSync(resolve(import.meta.dir, "../src/components/canvas/canvas-cloud-agent.css"), "utf8");

function message(partial: Partial<CloudAgentChatMessage> & { role: CloudAgentChatMessage["role"]; text: string }): CloudAgentChatMessage {
    return { id: partial.id || `${partial.role}-1`, ...partial };
}

describe("Agent 输入框发送键", () => {
    test("默认 Enter 发送，并且记住用户的切换", () => {
        expect(chat).toContain('const AGENT_SEND_ON_ENTER_KEY = "canvas:agent-send-on-enter"');
        // 读不到偏好时按默认值走，而不是让输入框失效。
        expect(chat).toContain('return localStorage.getItem(AGENT_SEND_ON_ENTER_KEY) !== "0";');
        expect(chat).toContain("sendOnEnter={sendOnEnter}");
        expect(chat).toContain("onClick={toggleSendOnEnter}");
        // 切换发送键不该把光标从输入框里踢出去。
        expect(chat).toContain("onMouseDown={(event) => event.preventDefault()}");
    });

    test("提示文案就是开关本身，两种习惯都能看懂", () => {
        expect(chat).toContain('"Enter 发送 · Shift+Enter 换行"');
        expect(chat).toContain('"⌘/Ctrl+Enter 发送 · Enter 换行"');
        expect(css).toContain(".agent-composer-send-hint-toggle");
        expect(css).toContain(".agent-composer-send-hint-toggle:focus-visible");
        expect(css).toContain(".agent-composer-send-hint-compact");
    });
});

describe("Agent 运行状态文案", () => {
    test("按最后一个真实事件描述在做什么", () => {
        expect(agentWorkingLabel([])).toBe("正在准备本轮");
        expect(agentWorkingLabel([message({ role: "user", text: "画一只猫" })])).toBe("正在处理当前画布");
        expect(agentWorkingLabel([message({ role: "assistant", text: "", reasoning: true })])).toBe("正在推理");
        expect(agentWorkingLabel([message({ role: "assistant", text: "好的" })])).toBe("正在写回复");
    });

    test("工具事件用真实摘要，取不到才退回通用文案", () => {
        // 生成任务提交后要一直告诉用户"任务已在跑"，而不是"正在执行工具"。
        expect(agentWorkingLabel([message({ role: "tool", text: "", detail: { toolName: "generate_media", eventType: "generation_task_created" } })])).toBe("媒体节点已创建，生成任务已提交");
        // 没有专属摘要的工具不能把"操作已完成"端出来。
        expect(agentWorkingLabel([message({ role: "tool", text: "" })])).toBe("正在继续下一步");
    });

    test("等待确认优先于一切进度", () => {
        expect(agentWorkingLabel([message({ role: "tool", text: "写入" })], true)).toBe("等待你确认这次写入");
    });

    test("面板接入运行状态文案，而不是写死一句", () => {
        expect(panel).toContain("agentWorkingLabel(messages, false)");
        expect(panel).not.toContain('label="正在处理当前画布"');
    });
});

describe("Agent 对话滚动跟手", () => {
    test("离开底部时才浮出回到最新，并且点击后恢复跟随", () => {
        expect(panel).toContain("setShowJumpToLatest(!nearBottom)");
        expect(panel).toContain("followRef.current = true;");
        expect(panel).toContain('behavior: "smooth"');
        expect(panel).toContain("回到最新");
        expect(css).toContain(".agent-jump-to-latest");
    });
});

describe("Agent 输入框渲染", () => {
    test("空闲时提示就是可点击的发送键开关", () => {
        const html = renderToStaticMarkup(
            <AgentChatComposer theme={canvasThemes.dark} prompt="" disabled={false} sending={false} placeholder="开始创作" onPromptChange={() => {}} onSubmit={() => {}} onAddFiles={() => {}} onRemoveAttachment={() => {}} />,
        );
        expect(html).toContain("agent-composer-send-hint-toggle");
        expect(html).toContain("Enter 发送 · Shift+Enter 换行");
        expect(html).toContain('aria-label="切换发送快捷键"');
        // 默认 448px 面板收起整句提示，但短标签必须顶上，否则开关就点不到了。
        expect(html).toContain("agent-composer-send-hint-compact");
        expect(html).toContain("⏎ 发送");
    });

    test("运行中改成插话提示，不再显示发送键开关", () => {
        const html = renderToStaticMarkup(
            <AgentChatComposer theme={canvasThemes.dark} prompt="" disabled={false} sending={false} running placeholder="插话" onPromptChange={() => {}} onSubmit={() => {}} onAddFiles={() => {}} onRemoveAttachment={() => {}} onStop={async () => {}} />,
        );
        expect(html).toContain("运行中：发送即插话，下一步生效");
        expect(html).not.toContain("agent-composer-send-hint-toggle");
        expect(html).not.toContain("agent-composer-send-hint-compact");
    });
});

describe("Agent 运行状态渲染", () => {
    test("状态行按当前动作显示，而不是永远一句话", () => {
        expect(renderToStaticMarkup(<AgentWorkingMessage theme={canvasThemes.dark} label={agentWorkingLabel([{ id: "a", role: "assistant", text: "", reasoning: true }])} />)).toContain("正在推理");
        expect(renderToStaticMarkup(<AgentWorkingMessage theme={canvasThemes.dark} label="等待你确认这次写入" />)).toContain("等待你确认这次写入");
    });
});
