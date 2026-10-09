import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const source = readFileSync(resolve(import.meta.dir, "../src/components/canvas/canvas-workspace-overlays.tsx"), "utf8");
const panelEffect = source.slice(
    source.indexOf("export function CanvasNodePanelOverlay"),
    source.indexOf("export function CanvasConnectionCreateMenu"),
);

describe("节点浮层不再每个视口节拍重排", () => {
    // 它会读 container.clientWidth，而组件每 64ms 就重渲染一次；在渲染期读布局
    // 等于每拍一次强制同步重排。实测这是平移期间最大的单点开销。
    test("挂载期位置只算一次，不在渲染期读 clientWidth", () => {
        expect(panelEffect).toContain("const [initialPosition] = useState(() => getNodePanelPosition(");
        const renderBody = panelEffect.slice(0, panelEffect.indexOf("useLayoutEffect"));
        expect(renderBody).not.toContain("const initialPosition = getNodePanelPosition(");
    });

    // 视口在依赖里会让订阅每个节拍都被拆掉重建；逐帧更新本来就由命令式订阅负责。
    test("命令式视口订阅的 effect 不再依赖 viewport", () => {
        const depsAt = panelEffect.indexOf("panelWidthScale]);");
        expect(depsAt).toBeGreaterThan(-1);
        const deps = panelEffect.slice(panelEffect.lastIndexOf("}, [", depsAt), depsAt + 1);
        expect(deps).not.toContain("viewport]");
        expect(deps).not.toContain(", viewport,");
    });

    test("用 ref 取最新视口，避免闭包读到过期的值", () => {
        expect(panelEffect).toContain("const latestViewportRef = useRef(viewport);");
        expect(panelEffect).toContain("latestViewportRef.current = viewport;");
        expect(panelEffect).toContain("let liveViewport = latestViewportRef.current;");
    });
});
