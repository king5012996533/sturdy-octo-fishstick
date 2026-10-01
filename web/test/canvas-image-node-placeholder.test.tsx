import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { renderToStaticMarkup } from "react-dom/server";

import { CanvasImageNodePlaceholder } from "../src/components/canvas/canvas-image-node-placeholder";
import { canvasThemes } from "../src/lib/canvas-theme";

const contentSource = readFileSync(new URL("../src/components/canvas/canvas-node-content.tsx", import.meta.url), "utf8");
const hookSource = readFileSync(new URL("../src/components/canvas/use-canvas-node-resource-url.ts", import.meta.url), "utf8");

describe("图片节点占位态", () => {
    test("空节点说明自己是空白，而不是只留一个像破图的图标", () => {
        const markup = renderToStaticMarkup(<CanvasImageNodePlaceholder theme={canvasThemes.dark} state="empty" />);
        expect(markup).toContain('data-canvas-image-state="empty"');
        expect(markup).toContain("空白图片节点");
        expect(markup).toContain("工具栏可生成或上传");
    });

    test("多视角角色节点用角色名替换默认说明", () => {
        const markup = renderToStaticMarkup(<CanvasImageNodePlaceholder theme={canvasThemes.dark} state="empty" hint="女明星 · 多视角参考待生成" />);
        expect(markup).toContain("女明星 · 多视角参考待生成");
        expect(markup).not.toContain("工具栏可生成或上传");
    });

    test("加载中给转圈状态，不显示重试", () => {
        const markup = renderToStaticMarkup(<CanvasImageNodePlaceholder theme={canvasThemes.dark} state="loading" onRetry={() => {}} />);
        expect(markup).toContain('data-canvas-image-state="loading"');
        expect(markup).toContain('aria-label="图片加载中"');
        expect(markup).not.toContain("data-canvas-image-retry");
    });

    test("加载失败给出重试入口", () => {
        const markup = renderToStaticMarkup(<CanvasImageNodePlaceholder theme={canvasThemes.dark} state="failed" onRetry={() => {}} />);
        expect(markup).toContain('data-canvas-image-state="failed"');
        expect(markup).toContain("图片加载失败");
        expect(markup).toContain("data-canvas-image-retry");
    });
});

describe("图片节点资源解析契约", () => {
    test("节点内容组件把三态交给占位组件，并把解码失败回报给 hook", () => {
        expect(contentSource).toContain("useCanvasNodeResourceUrl(node, nearViewport)");
        expect(contentSource).toContain('state={failed ? "failed" : loading || hasMediaReference ? "loading" : "empty"}');
        expect(contentSource).toContain("onError={reportImageError}");
        expect(contentSource).toContain("<CanvasImageNodePlaceholder");
    });

    test("只有尝试过下载仍为空才算失败，未进入视口的缓存 miss 不算", () => {
        expect(hookSource).toContain("setFailed(eager);");
        expect(hookSource).toContain("const retry = useCallback(() => setRetryNonce((nonce) => nonce + 1), []);");
        expect(hookSource).toContain("setUrl(\"\");\n        setFailed(true);");
    });
});
