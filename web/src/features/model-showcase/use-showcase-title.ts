import { useEffect } from "react";

/**
 * 公开页要有自己的浏览器标题：访客可能直接把链接分享出去，标题里写着产品名的
 * "模型广场"才说明这页是什么。离开页面时还原，避免把标题留在工作区上。
 */
export function useShowcaseTitle(title: string) {
    useEffect(() => {
        const previous = document.title;
        document.title = title;
        return () => {
            document.title = previous;
        };
    }, [title]);
}
