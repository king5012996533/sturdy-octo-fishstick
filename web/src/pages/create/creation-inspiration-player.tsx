import type Hls from "hls.js";
import { Sparkles } from "lucide-react";
import { useEffect, useState } from "react";

import { AppModal } from "@/components/ui/product/app-modal";

import type { CreationInspiration } from "./creation-inspirations";

/**
 * 灵感作品的播放入口。
 *
 * 为什么只做"打开才加载"这一件事：成片中位 292MB、最大 1.5GB，整个作品池 80 条合计
 * 32GB —— 平台存不下也不该存，所以成片永远留在上游，由浏览器直连、按需拉分片。这个
 * 组件因此必须守住两条：广场列表里不挂任何 <video>，弹层关闭即销毁；hls.js 也只在真
 * 要播 HLS 时才动态加载，首页不为它多付一次体积。播放流量走的是上游 CDN，本站只提供
 * 一个地址字符串。
 */

/**
 * 刻意不放"原站作品"外链：弹层是站内的终点，往外送一条链接等于把看过这条作品的人交给
 * 上游。署名（作者名）留在标题下方，那是版权归属，与跳转是两件事。
 */

/** 上游那三档是 1080p(12.6Mbps) / 720p(2.8Mbps) / 480p(1.2Mbps)，选档交给 ABR。 */
function attachHls(video: HTMLVideoElement, source: string, onFatal: () => void) {
    let disposed = false;
    let instance: Hls | null = null;
    void import("hls.js")
        .then(({ default: HlsPlayer }) => {
            if (disposed) return;
            // 上游不是每个作品都转过码；库里存的也可能是原片，那种情况走 <video src>。
            if (!HlsPlayer.isSupported()) {
                video.src = source;
                return;
            }
            instance = new HlsPlayer({
                // startLevel = -1 交给 ABR，capLevelToPlayerSize 让"弹层多大就取多清晰"，
                // 否则上游把 1080p 排在第一档，打开瞬间就是一条 12.6Mbps 的流。
                startLevel: -1,
                capLevelToPlayerSize: true,
            });
            instance.on(HlsPlayer.Events.ERROR, (_event, data) => {
                if (data.fatal) onFatal();
            });
            instance.loadSource(source);
            instance.attachMedia(video);
        })
        // 库没加载出来就退回原生 <video src>：原片本来就不需要它，Safari 也能自行播 HLS。
        .catch(() => {
            if (!disposed) video.src = source;
        });
    return () => {
        disposed = true;
        instance?.destroy();
    };
}

export function CreationInspirationPlayer({ item, onUse, onClose }: {
    item: CreationInspiration;
    onUse: () => void;
    onClose: () => void;
}) {
    // 用回调 ref 而不是 useRef：弹层内容在 portal 里挂载，元素可能被重建（挂载 → 隐藏
    // 再显示），useRef 会指向旧节点，而 effect 的依赖没变、不会重跑 —— 表现就是"播放器
    // 打开了，但 <video> 上一片空白"。把元素本身放进依赖，才能对真正在 DOM 里的那个生效。
    const [video, setVideo] = useState<HTMLVideoElement | null>(null);
    const [unavailable, setUnavailable] = useState(false);
    const source = item.videoUrl || "";

    useEffect(() => {
        if (!video || !source) return undefined;
        setUnavailable(false);

        const startPlayback = () => {
            // 用户是点卡片进来的，带着手势，正常情况下能带声播。被浏览器拦下时静音重试
            // 一次：停在首帧会让人以为"点了没反应"，而面板上明明有播放键。
            void video.play().catch(() => {
                video.muted = true;
                void video.play().catch(() => undefined);
            });
        };

        // 是否走 hls.js 只看 MSE 支持，不看 canPlayType：Chrome 对
        // application/vnd.apple.mpegurl 会回 "maybe"，据此直接喂 m3u8 就是黑屏，
        // 而它其实并不会播 HLS。
        if (!source.includes(".m3u8")) {
            video.src = source;
            video.addEventListener("loadedmetadata", startPlayback, { once: true });
            startPlayback();
            return () => video.removeEventListener("loadedmetadata", startPlayback);
        }
        const dispose = attachHls(video, source, () => setUnavailable(true));
        video.addEventListener("loadedmetadata", startPlayback, { once: true });
        return () => {
            video.removeEventListener("loadedmetadata", startPlayback);
            dispose();
        };
    }, [video, source]);

    return (
        <AppModal
            flush
            open
            title={null}
            footer={null}
            centered
            width="min(1160px, calc(100vw - 32px))"
            onCancel={onClose}
            className="creation-inspiration-player-modal"
        >
            <div className="creation-inspiration-player">
                {source && !unavailable ? (
                    <video ref={setVideo} controls playsInline poster={item.image} className="creation-inspiration-player-video" />
                ) : (
                    <div className="creation-inspiration-player-fallback">
                        <strong>这个作品暂时播不了</strong>
                        <span>上游成片取不到，封面与提示词仍然可用。</span>
                    </div>
                )}
                <div className="creation-inspiration-player-bar">
                    <div className="creation-inspiration-player-meta">
                        <strong>{item.title}</strong>
                        <span>{[item.category, item.author].filter(Boolean).join(" · ")}</span>
                    </div>
                    <div className="creation-inspiration-player-actions">
                        <button type="button" className="creation-inspiration-player-use" onClick={onUse}>
                            <Sparkles aria-hidden="true" />
                            使用这个创意
                        </button>
                    </div>
                </div>
            </div>
        </AppModal>
    );
}
