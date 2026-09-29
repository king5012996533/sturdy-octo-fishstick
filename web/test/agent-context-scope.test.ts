import { expect, test } from "bun:test";
import { resolveAgentContextScope } from "@/lib/canvas/agent-context-scope";
import { canvasNodeMentionToken } from "@/lib/canvas/canvas-resource-references";

test("引用画布节点时自动补上画布范围", () => {
    const scope = resolveAgentContextScope([], `你能不能看到这个图是什么 ${canvasNodeMentionToken("image-1")}`);
    expect(scope).toContain("canvas");
});

test("已经勾选画布范围时保持原样，不改动用户设置", () => {
    const scope = resolveAgentContextScope(["canvas", "skills"], canvasNodeMentionToken("image-1"));
    expect(scope).toEqual(["canvas", "skills"]);
});

test("纯聊天不隐式扩大上下文范围", () => {
    expect(resolveAgentContextScope([], "你好，帮我写个片名")).toEqual([]);
    expect(resolveAgentContextScope([], "@skill:storyboard 帮我看下分镜")).toEqual([]);
});

test("没有画布范围时引用了节点，范围里补的是画布而不是别的键", () => {
    const scope = resolveAgentContextScope(["skills"], canvasNodeMentionToken("image-1"));
    expect(scope).toEqual(["skills", "canvas"]);
});
