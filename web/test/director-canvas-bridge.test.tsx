import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const workbench = readFileSync(resolve(import.meta.dir, "../src/components/canvas/director/canvas-director-workbench.tsx"), "utf8");
const project = readFileSync(resolve(import.meta.dir, "../src/pages/canvas/project.tsx"), "utf8");
const nodePanel = readFileSync(resolve(import.meta.dir, "../src/components/canvas/director/canvas-director-node-panel.tsx"), "utf8");
const cameraTabs = readFileSync(resolve(import.meta.dir, "../src/components/canvas/director/director-camera-screenshot-tabs.tsx"), "utf8");

/**
 * 导演台曾经像个孤岛：画布图片埋在「AI 识图导入 → 3D 素材」三层下，
 * 唯一显眼的出口（场景描述右边的箭头）还是常驻 disabled 的占位。
 * 这组测试钉住两端接口，别再退化回去。
 */
describe("导演台与画布的接口", () => {
    test("画布图片是导演台里的一级入口，并按关系排序标注", () => {
        expect(workbench).toContain('navigationTab === "canvas"');
        expect(workbench).toContain('connectedImageNodeIds');
        expect(workbench).toContain('badge={connected ? "已连线" : inScene ? "场中" : undefined}');
        expect(workbench).toContain("data-director-row-badge");
        // 排序与归类交给 lib 里的纯函数，行为由 director-canvas-images.test.ts 覆盖。
        expect(workbench).toContain("buildDirectorCanvasImageRows(imageNodes, connectedImageNodeIds || [], directorBillboardSourceNodeIds(draft?.objects || []))");
    });

    test("导演台不再删画布节点，✕ 只移除场景里的立牌", () => {
        expect(workbench).not.toContain("onDeleteImageNode");
        expect(workbench).toContain("removeBillboardForCanvasNode");
        expect(workbench).toContain("onDelete={inScene ? () => removeBillboardForCanvasNode(node.id) : undefined}");
        expect(project).not.toContain("onDeleteImageNode");
    });

    test("画布图片列表不再静默截断到 20 张", () => {
        expect(workbench).not.toContain("imageNodes.slice(0, 20)");
        expect(workbench).toContain("imageNodes.map((node) => <SceneRow");
    });

    test("画布把连到导演节点的关系传给导演台", () => {
        expect(project).toContain("connectedImageNodeIds={connections.filter((connection) => connection.toNodeId === directorNodeId");
    });

    test("场景描述右侧的箭头可用，它就是送回画布的动作", () => {
        expect(workbench).not.toContain("submitUnavailable={true}");
        expect(workbench).toContain("onSubmit={() => void applyToCanvas()}");
    });

    test("导演节点空态说明输入端这条关系", () => {
        expect(nodePanel).toContain("把画布上的图片连到本节点输入端");
    });
});

describe("导演台截图回画布", () => {
    test("截图右键只有一个出口：每条通道都接上发送回调", () => {
        // 属性页（ShotInspector 内嵌）和截图页（机位分组）都要能发送，否则换个模式就没入口。
        expect(workbench).toContain("<DirectorScreenshotGallery screenshots={shot.screenshots || []} onSendToCanvas={onSendScreenshotToCanvas} />");
        expect(workbench).toContain("onSendScreenshotToCanvas={onSendScreenshotToCanvas ? (screenshot) => void sendScreenshotToCanvas(screenshot) : undefined}");
        expect(cameraTabs).toContain("onSendToCanvas={onSendScreenshotToCanvas}");
    });

    test("截图节点沿用已有资源，并以 append 方式连回镜头节点", () => {
        expect(project).toContain("createDirectorScreenshotCanvasNode(screenshot, getCanvasCenter())");
        expect(project).toContain('connectDirectorReferenceNodes([...nodesRef.current, node], connectionsRef.current, [node.id], directorNodeId || "", () => nanoid(), "append")');
        // 截图已经上传过，不能在这里再传一次。
        expect(project).not.toContain("uploadImage(screenshot");
    });

    test("画布上送出去的截图仍然可以被撤回场景立牌，不会误删画布节点", () => {
        expect(workbench).toContain("const sendScreenshotToCanvas = async (screenshot: DirectorScreenshot) => {");
        expect(workbench).toContain('message.success("截图已发送到画布")');
    });

    test("视口工具条提供一键「截图并发送到画布」，没有画布出口时按钮不出现", () => {
        expect(workbench).toContain("onCaptureAndSend={onSendScreenshotToCanvas ? () => void captureScreenshot({ sendToCanvas: true }) : undefined}");
        expect(workbench).toContain("const captureScreenshot = async ({ sendToCanvas = false }");
        // 发送失败不能把已经存下来的截图说成失败。
        expect(workbench).toContain("截图已保存，但发送到画布失败");
    });
});
