import { describe, expect, test } from "bun:test";

import { CanvasNodeType } from "@/types/canvas";
import { NODE_DEFAULT_SIZE } from "@/constant/canvas";
import { MEDIA_NODE_MIN_SIZE, VIDEO_NODE_MAX_SIZE } from "@/lib/canvas/canvas-node-size";

describe("画布媒体卡片尺寸", () => {
    test("新建图片和视频卡片使用适合扫描的默认尺寸", () => {
        expect(NODE_DEFAULT_SIZE[CanvasNodeType.Image]).toMatchObject({ width: 560, height: 315 });
        expect(NODE_DEFAULT_SIZE[CanvasNodeType.Video]).toMatchObject({ width: 560, height: 315 });
    });

    test("媒体节点仍保留可读的最小尺寸并限制视频最大尺寸", () => {
        expect(MEDIA_NODE_MIN_SIZE).toEqual({ width: 360, height: 202 });
        expect(VIDEO_NODE_MAX_SIZE).toEqual({ width: 560, height: 405 });
    });
});
