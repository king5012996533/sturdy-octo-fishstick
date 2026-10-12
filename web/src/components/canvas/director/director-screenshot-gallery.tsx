import { Image } from "antd";
import { Send } from "lucide-react";
import { useEffect, useState } from "react";

import { resolveImageUrl } from "@/services/image-storage";
import type { DirectorScreenshot } from "@/types/director";

/** 截图只是导演台里的中间产物；这里给每张图一条回到画布的路，避免截图落地后无处可用。 */
export function DirectorScreenshotGallery({ screenshots, title = "相机截图", compact = false, onSendToCanvas }: { screenshots: DirectorScreenshot[]; title?: string; compact?: boolean; onSendToCanvas?: (screenshot: DirectorScreenshot) => void | Promise<void> }) {
    const [sendingId, setSendingId] = useState<string | null>(null);
    const send = async (screenshot: DirectorScreenshot) => {
        if (!onSendToCanvas || sendingId) return;
        setSendingId(screenshot.id);
        try {
            await onSendToCanvas(screenshot);
        } finally {
            setSendingId(null);
        }
    };
    return <section aria-label="相机截图" className={compact ? "space-y-2" : "space-y-2 border-t pt-3"} style={compact ? undefined : { borderColor: "var(--director-sequencer-border)" }}>
        <h3 className="text-sm font-medium">{title}</h3>
        {screenshots.length ? <div className="grid grid-cols-2 gap-2">{screenshots.map((screenshot) => <ScreenshotCard key={screenshot.id} screenshot={screenshot} compact={compact} sending={sendingId === screenshot.id} onSendToCanvas={onSendToCanvas ? () => void send(screenshot) : undefined} />)}</div> : <p className="text-xs opacity-55">暂无截图</p>}
    </section>;
}

function ScreenshotCard({ screenshot, compact, sending, onSendToCanvas }: { screenshot: DirectorScreenshot; compact: boolean; sending: boolean; onSendToCanvas?: () => void }) {
    const [url, setUrl] = useState(screenshot.url);
    useEffect(() => {
        let active = true;
        void resolveImageUrl(screenshot.storageKey, screenshot.url, { cacheMiss: true }).then((resolved) => { if (active) setUrl(resolved); }).catch(() => {});
        return () => { active = false; };
    }, [screenshot.storageKey, screenshot.url]);
    return <div className={compact ? "min-w-0 max-w-28 overflow-hidden" : "min-w-0 overflow-hidden rounded-lg border"} style={compact ? undefined : { borderColor: "var(--director-sequencer-border)" }}>
        <Image src={url} alt={screenshot.name} width="100%" className={compact ? "aspect-square rounded-lg object-cover" : "aspect-video object-cover"} preview={{ mask: "查看" }} />
        <span className={`block truncate text-xs ${compact ? "py-1 opacity-60" : "px-2 pt-2"}`} title={screenshot.name}>{screenshot.name}</span>
        {onSendToCanvas ? <button type="button" data-director-screenshot-send={screenshot.id} aria-label={`发送截图 ${screenshot.name} 到画布`} disabled={sending} className={`flex w-full items-center justify-center gap-1 rounded-md border text-[11px] leading-none transition enabled:hover:bg-white/10 disabled:opacity-50 ${compact ? "px-1.5 py-1" : "my-2 px-2 py-2"}`} style={{ borderColor: "var(--director-sequencer-border)" }} onClick={onSendToCanvas}><Send className="size-3 shrink-0" aria-hidden />{sending ? "正在发送" : "发送到画布"}</button> : null}
    </div>;
}
