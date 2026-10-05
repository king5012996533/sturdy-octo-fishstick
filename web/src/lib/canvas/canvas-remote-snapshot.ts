/**
 * 后台快照回写的准入判定。
 *
 * 页面有两条后台同步路径：每 4 秒轮询一次画布接口，以及 canvas.updated 的 SSE 推送。
 * 两条路径拿到云端快照后都会直接 setNodes / setConnections 覆盖编辑器，但云端那份
 * 必然落后于内存里的编辑——节点位置要到指针抬起才提交，保存本身又是异步的。于是用户
 * 拖到一半、刚选完比例、正在输入时被覆盖一次，画面就被拉回旧状态：卡片回弹、尺寸来回
 * 跳、跟着节点走的输入框一起闪。机器越慢时间窗口越宽，所以 Windows 上尤其明显。
 *
 * 把判定收成纯函数，两条路径共用同一道闸，也便于单测。
 */
export type CanvasRemoteSnapshotGate = {
    /** 正在拖动节点（指针按下到抬起之间）。 */
    nodeDragging: boolean;
    /** 视口手势进行中（平移 / 缩放 / 滑行）。 */
    viewportInteracting: boolean;
    /** 本地还有排队或进行中的写入没落盘。 */
    hasPendingLocalWrite: boolean;
};

/**
 * 只有编辑器空闲、且本地没有未落盘编辑时才允许套用后台快照。
 *
 * 被挡下不是丢弃：轮询每 4 秒会再来一次，跨标签页 / 跨设备的同步只是推迟到空闲后生效。
 */
export function canApplyRemoteCanvasSnapshot(gate: CanvasRemoteSnapshotGate) {
    return !gate.nodeDragging && !gate.viewportInteracting && !gate.hasPendingLocalWrite;
}
