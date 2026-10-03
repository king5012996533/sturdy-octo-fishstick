import { useEffect, useRef, useState, type ImgHTMLAttributes } from "react";

/**
 * 广场封面：滚到眼前才挂 src。
 *
 * 广场改成一次全出之后，58 张封面如果只靠浏览器的 loading="lazy"，Chrome 会在快速
 * 网络下把预载阈值放到几千像素、一口气全下完（实测首屏 58 张全拉，合计约 6.7MB）。
 * 这里自己盯视口，只留 320px 的提前量，列表再长也只下看得到的那几张。
 *
 * 封面来自对方 CDN 的外链，没有 storageKey，所以不复用 CachedResourceImage ——
 * 那个组件的视口门控只对带资源 ID 的图生效。
 */
export function CreationInspirationCover({ eager = false, ...props }: ImgHTMLAttributes<HTMLImageElement> & { eager?: boolean }) {
    const ref = useRef<HTMLImageElement>(null);
    const [nearViewport, setNearViewport] = useState(eager);

    useEffect(() => {
        if (nearViewport) return;
        const image = ref.current;
        // 没有 IntersectionObserver（老浏览器、SSR 后的首帧）就直接放行：宁可多下一张图，
        // 也不能让广场变成一片空盒子。
        if (!image || typeof IntersectionObserver === "undefined") {
            setNearViewport(true);
            return;
        }
        const observer = new IntersectionObserver((entries) => {
            if (entries.some((entry) => entry.isIntersecting)) {
                setNearViewport(true);
                observer.disconnect();
            }
        }, { rootMargin: "320px" });
        observer.observe(image);
        return () => observer.disconnect();
    }, [nearViewport]);

    return <img {...props} ref={ref} src={nearViewport ? props.src : undefined} />;
}
