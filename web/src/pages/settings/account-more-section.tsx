import { ChevronDown } from "lucide-react";
import { useState, type ReactNode } from "react";

/**
 * 「更多设置」：登录设备、身份绑定、我的投稿、用量与协议、注销账号都收在这里。
 *
 * 这一页的第一屏只回答三个问题（还剩多少积分、我叫什么、密码是什么），其余能力不是
 * 不重要，而是低频且破坏性的居多：摊开在首屏只会让"看一眼余额"变成一次浏览任务。
 *
 * 收起时不渲染子树：五个子卡片各自的请求也不该在用户没打开时发出去。
 */
export function AccountMoreSection({ children }: { children: ReactNode }) {
    const [open, setOpen] = useState(false);

    return (
        <div className="account-more">
            <button type="button" className="account-more-toggle" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
                <span>更多设置</span>
                <ChevronDown className="size-4" aria-hidden />
            </button>
            {open ? <div className="account-more-body">{children}</div> : null}
        </div>
    );
}
