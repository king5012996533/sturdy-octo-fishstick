import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { DirectorScreenshotGallery } from "../src/components/canvas/director/director-screenshot-gallery";
import type { DirectorScreenshot } from "../src/types/director";

describe("导演台截图历史展示", () => {
    test("当前镜头有截图时显示可辨识的图片与名称", () => {
        const html = renderToStaticMarkup(createElement(DirectorScreenshotGallery, { screenshots: [{ id: "one", name: "机位3-shot-01", storageKey: "resource:image-1", url: "/api/resources/image-1", width: 1920, height: 1080, createdAt: "2026-09-29T00:00:00.000Z" }] }));
        expect(html).toContain("相机截图");
        expect(html).toContain("机位3-shot-01");
        expect(html).toContain('alt="机位3-shot-01"');
    });

    test("摄影机截图页使用方形缩略图且不叠加卡片边框", () => {
        const html = renderToStaticMarkup(createElement(DirectorScreenshotGallery, { title: "机位3截图", compact: true, screenshots: [{ id: "one", name: "机位3-shot-01", storageKey: "resource:image-1", url: "/api/resources/image-1", width: 1920, height: 1080, createdAt: "2026-09-29T00:00:00.000Z" }] }));
        expect(html).toContain("机位3截图");
        expect(html).toContain("aspect-square");
        expect(html).not.toContain("rounded-lg border");
    });
});

const sendable: DirectorScreenshot = { id: "shot-1", name: "主摄影机-1", url: "/assets/a.png", storageKey: "resource:a", width: 1280, height: 720, createdAt: "2026-10-12T00:00:00.000Z" };

describe("导演台截图发送到画布", () => {
    test("没给发送回调时只展示截图，不出现发送入口", () => {
        const html = renderToStaticMarkup(createElement(DirectorScreenshotGallery, { screenshots: [sendable] }));
        expect(html).toContain("主摄影机-1");
        expect(html).not.toContain("发送到画布");
    });

    test("给了发送回调后每张截图都有独立的发送到画布按钮", () => {
        const html = renderToStaticMarkup(createElement(DirectorScreenshotGallery, { screenshots: [sendable], onSendToCanvas: () => {} }));
        expect(html).toContain('data-director-screenshot-send="shot-1"');
        expect(html).toContain('aria-label="发送截图 主摄影机-1 到画布"');
        expect(html).toContain("发送到画布");
    });

    test("紧凑的截图页也保留发送入口，换个模式不会少一条路", () => {
        const html = renderToStaticMarkup(createElement(DirectorScreenshotGallery, { screenshots: [sendable], compact: true, onSendToCanvas: () => {} }));
        expect(html).toContain('aria-label="发送截图 主摄影机-1 到画布"');
    });
});
