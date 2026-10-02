import { describe, expect, test } from "bun:test";

import { cloudMediaUrl, isEphemeralMediaUrl, withCloudMediaContent, withCloudMediaContentOnProject } from "@/lib/canvas/canvas-cloud-media";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function node(metadata: CanvasNodeData["metadata"], type = CanvasNodeType.Image): CanvasNodeData {
    return { id: "n1", type, title: "图片", position: { x: 0, y: 0 }, width: 320, height: 240, metadata };
}

describe("isEphemeralMediaUrl", () => {
    test("blob 与 data 地址跨会话不可复现", () => {
        expect(isEphemeralMediaUrl("blob:https://kinotv.example/1")).toBe(true);
        expect(isEphemeralMediaUrl("data:image/png;base64,AAAA")).toBe(true);
        expect(isEphemeralMediaUrl("")).toBe(true);
        expect(isEphemeralMediaUrl(undefined)).toBe(true);
    });

    test("云端地址和外链都视为持久", () => {
        expect(isEphemeralMediaUrl("/api/resources/abc/file")).toBe(false);
        expect(isEphemeralMediaUrl("https://cdn.example/a.png")).toBe(false);
    });
});

describe("cloudMediaUrl", () => {
    test("只有 resource: 前缀能推导出本站资源地址", () => {
        expect(cloudMediaUrl("resource:abc")).toContain("/resources/abc/file");
        expect(cloudMediaUrl("image:scope:key")).toBe("");
        expect(cloudMediaUrl(undefined)).toBe("");
    });
});

describe("withCloudMediaContent", () => {
    // 这是「云端没有保留」的直接病灶：节点早就有了云端资源，展示地址却还写着 blob:。
    test("云端资源 + blob 展示地址时收敛为云端地址", () => {
        const rewritten = withCloudMediaContent(node({ content: "blob:https://kinotv.example/1", storageKey: "resource:abc" }));
        expect(rewritten.metadata?.content).toBe(cloudMediaUrl("resource:abc"));
        expect(rewritten.metadata?.storageKey).toBe("resource:abc");
    });

    test("空 content 同样补上云端地址", () => {
        expect(withCloudMediaContent(node({ content: "", storageKey: "resource:abc" })).metadata?.content).toContain("/resources/abc/file");
    });

    test("外链保持原样，不把别人的地址改成本站资源", () => {
        const external = node({ content: "https://cdn.example/a.png", storageKey: "resource:abc" });
        expect(withCloudMediaContent(external)).toBe(external);
    });

    test("本地键不动——它还等着补传任务去处理", () => {
        const local = node({ content: "blob:https://kinotv.example/1", storageKey: "image:scope:key" });
        expect(withCloudMediaContent(local)).toBe(local);
    });

    test("文本节点不受影响", () => {
        const text = node({ content: "blob:x", storageKey: "resource:abc" }, CanvasNodeType.Text);
        expect(withCloudMediaContent(text)).toBe(text);
    });
});

describe("withCloudMediaContentOnProject", () => {
    test("没有任何节点需要改写时返回原对象，避免无谓的保存触发", () => {
        const project = { nodes: [node({ content: "https://cdn.example/a.png", storageKey: "resource:abc" })] };
        expect(withCloudMediaContentOnProject(project)).toBe(project);
    });

    test("有节点需要改写时只替换该节点", () => {
        const target = node({ content: "blob:x", storageKey: "resource:abc" });
        const untouched = node({ content: "https://cdn.example/a.png", storageKey: "resource:def" });
        const project = { nodes: [target, untouched] };
        const rewritten = withCloudMediaContentOnProject(project);
        expect(rewritten).not.toBe(project);
        expect(rewritten.nodes[0]).not.toBe(target);
        expect(rewritten.nodes[1]).toBe(untouched);
    });
});
