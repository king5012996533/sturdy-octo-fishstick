/**
 * 后台的媒体预览：素材管理与生成产物对账共用一份渲染口径。
 *
 * 地址是后端现场签发的只读链接（12 小时到期），前端拿不到就别硬凑——签不出来时给
 * 明确空态，后台排查时"没有预览"和"链接打不开"必须能一眼区分。
 */
export function MediaPreview({ kind, src }: { kind: string; src?: string }) {
    if (!src) {
        return <span className="admin-asset-placeholder">无预览</span>;
    }
    if (kind === "video") {
        return <video className="admin-resource-video" src={src} muted playsInline preload="metadata" controls />;
    }
    if (kind === "audio") {
        return <audio className="admin-resource-audio" src={src} controls preload="metadata" />;
    }
    return <img className="admin-resource-thumb" src={src} alt="" loading="lazy" />;
}
