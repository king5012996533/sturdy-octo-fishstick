import { useCallback, useState } from "react";

/** 认证门面里提示条的语气：失败、没填完、办成了、说明一句。 */
export type AuthNoticeTone = "error" | "warning" | "success" | "info";

type AuthNoticeState = { tone: AuthNoticeTone; text: string } | null;

/**
 * 认证门面自己的提示通道。
 *
 * 全局 CSS 关掉了 antd 的浮层提示（工作区的反馈走画布内状态），登录页没有画布，
 * 「验证码发出去了没有」「登录为什么进不去」只能在这张卡片里说。提示条留在卡片内，
 * 跟着场景一起被看见，也不会和别的页面抢右上角。
 */
export function useAuthNotice() {
    const [notice, setNotice] = useState<AuthNoticeState>(null);
    const showNotice = useCallback((tone: AuthNoticeTone, text: string) => setNotice({ tone, text }), []);
    const clearNotice = useCallback(() => setNotice(null), []);
    return { notice, showNotice, clearNotice };
}

export function AuthNotice({ notice }: { notice: AuthNoticeState }) {
    if (!notice) return null;
    return (
        <p className={`auth-notice is-${notice.tone}`} role={notice.tone === "error" ? "alert" : "status"} data-testid="hosted-auth-notice">
            {notice.text}
        </p>
    );
}
