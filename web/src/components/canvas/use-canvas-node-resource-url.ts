import { useCallback, useEffect, useState, type RefObject } from "react";

import { buildLibTVImagePreviewUrl, buildLibTVVideoSourceUrl } from "@/lib/canvas/libtv-import";
import { resourceIdFromStorageKey } from "@/services/api/resources";
import { resolveImageUrl } from "@/services/image-storage";
import { cacheResourceObjectUrl, getCachedResourceObjectUrl, peekCachedResourceObjectUrl } from "@/services/resource-blob-cache";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

export type CanvasNodeResourceUrl = {
    /** 可直接挂到 <img> 或播放器上的地址；空字符串表示当前拿不到。 */
    url: string;
    loading: boolean;
    /** 解析失败（下载失败、本地缓存读不到）。必须和"节点本来就没有媒体"分开呈现。 */
    failed: boolean;
    retry: () => void;
    /** 交给 <img onError>：解码失败时清掉地址，重试才会重新解析（旧 Blob URL 可能已被撤销）。 */
    reportImageError: () => void;
};

/**
 * 把节点元数据解析成本地可用的媒体地址。
 *
 * 远程资源接口受登录/桌面令牌保护，原生 `<img>` 无法附带令牌，所以先经
 * resource-blob-cache 走鉴权下载再挂 Blob URL。图片按视口懒加载，未进入
 * 视口时不下载；调用方据此区分"待加载"和"失败"。
 */
export function useCanvasNodeResourceUrl(node: CanvasNodeData, eager: boolean): CanvasNodeResourceUrl {
    const storageKey = node.metadata?.storageKey || "";
    const rawContent = node.metadata?.content || "";
    const content = node.type === CanvasNodeType.Video && node.metadata?.importSource?.provider === "libtv"
        ? buildLibTVVideoSourceUrl(rawContent)
        : rawContent;
    // `previewContent` is intentionally passive-only.  When a media node is
    // activated, VideoPlayer/Audio must receive the playable asset, never the
    // LibTV OSS snapshot URL stored for the thumbnail.
    const fallback = node.type === CanvasNodeType.Video || node.type === CanvasNodeType.Audio
        ? content
        : node.metadata?.previewContent
            || (node.type === CanvasNodeType.Image && node.metadata?.importSource?.provider === "libtv" ? buildLibTVImagePreviewUrl(content) : content);
    const resourceId = resourceIdFromStorageKey(storageKey);
    const isRemoteResource = Boolean(resourceId);
    const synchronousUrl = eager && isRemoteResource && node.type === CanvasNodeType.Image ? peekCachedResourceObjectUrl(storageKey) : "";
    // Inline data URLs are already local, but decoding thousands of them is
    // still expensive. Images must wait for the same viewport gate as remote
    // resources; otherwise DOM virtualization does not reduce image work.
    const isLazyVisual = node.type === CanvasNodeType.Image;
    const initialUrl = synchronousUrl || (isRemoteResource || isLazyVisual ? "" : fallback);
    const [url, setUrl] = useState(() => initialUrl);
    const [loading, setLoading] = useState(() => !initialUrl && isRemoteResource && eager);
    const [failed, setFailed] = useState(false);
    const [retryNonce, setRetryNonce] = useState(0);

    useEffect(() => {
        let cancelled = false;
        setFailed(false);

        if (!isRemoteResource && isLazyVisual && storageKey) {
            if (!eager) {
                setUrl("");
                setLoading(false);
                return () => { cancelled = true; };
            }
            setLoading(true);
            void resolveImageUrl(storageKey, fallback)
                .then((resolved) => {
                    if (cancelled) return;
                    setUrl(resolved);
                    setFailed(!resolved);
                })
                .catch(() => {
                    if (cancelled) return;
                    setUrl("");
                    setFailed(true);
                })
                .finally(() => {
                    if (!cancelled) setLoading(false);
                });
            return () => { cancelled = true; };
        }

        if (!isRemoteResource) {
            setUrl(isLazyVisual && !eager ? "" : fallback);
            setLoading(false);
            return () => { cancelled = true; };
        }

        const cachedSync = peekCachedResourceObjectUrl(storageKey);
        if (cachedSync) {
            setUrl(cachedSync);
            setLoading(false);
            return () => { cancelled = true; };
        }
        if (!url) {
            setLoading(eager);
        }
        // 只有进入视口或被激活的节点才下载远程媒体；缓存层会复用已有 Blob URL 和 in-flight 请求。
        const resolve = eager ? cacheResourceObjectUrl(storageKey) : getCachedResourceObjectUrl(storageKey);
        void resolve.then((cached) => {
            if (cancelled) return;
            if (cached) {
                setUrl(cached);
                return;
            }
            // 未进入视口的缓存 miss 是正常状态；已经尝试过下载仍为空才算失败。
            setFailed(eager);
            // Never hand a protected resource URL to a native <img> after a
            // cache miss. It cannot attach the auth header and would render
            // as a broken image after relogin.
        }).catch(() => {
            if (cancelled) return;
            setUrl(synchronousUrl);
            setFailed(eager);
        }).finally(() => {
            if (!cancelled) setLoading(false);
        });
        return () => { cancelled = true; };
    }, [eager, fallback, isLazyVisual, isRemoteResource, retryNonce, storageKey]);

    const retry = useCallback(() => setRetryNonce((nonce) => nonce + 1), []);
    const reportImageError = useCallback(() => {
        setUrl("");
        setFailed(true);
    }, []);

    return { url, loading, failed, retry, reportImageError };
}

/** 用 IntersectionObserver 判断节点是否进入视口；一旦进入就不再回退，避免缩放时反复卸载资源。 */
export function useCanvasNodeNearViewport(ref: RefObject<Element | null>) {
    const [nearViewport, setNearViewport] = useState(false);
    useEffect(() => {
        const element = ref.current;
        if (!element || typeof IntersectionObserver === "undefined") {
            setNearViewport(true);
            return;
        }
        const observer = new IntersectionObserver((entries) => {
            if (entries.some((entry) => entry.isIntersecting)) {
                setNearViewport(true);
                observer.disconnect();
            }
        }, { rootMargin: "600px" });
        observer.observe(element);
        return () => observer.disconnect();
    }, [ref]);
    return nearViewport;
}
