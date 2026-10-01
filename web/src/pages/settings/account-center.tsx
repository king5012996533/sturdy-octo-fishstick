import { AccountBindingsCard } from "./account-bindings-card";
import { AccountCreditsCard } from "./account-credits-card";
import { AccountDeletionCard } from "./account-deletion-card";
import { AccountDevicesCard } from "./account-devices-card";
import { AccountMoreSection } from "./account-more-section";
import { AccountPasswordCard } from "./account-password-card";
import { AccountProfileCard } from "./account-profile-card";
import { AccountUsageCard } from "./account-usage-card";
import { MyCreationPostsCard } from "./my-creation-posts-card";

import "./account-center.css";

/**
 * 用户中心（托管形态的 /settings）。
 *
 * 托管实例上用户没有可配置的模型：上游凭证、渠道与计费都由平台持有。因此这一页不摆
 * 任何配置表单，只回答三件事——我还有多少积分、我叫什么、我的密码是什么。
 * 这三张卡各占一个文件、各发各的请求、各自失败各自重试：积分接口抖动不该让用户连
 * 昵称都改不了。
 *
 * 登录设备、身份绑定、我的投稿、用量与协议、注销账号收进「更多设置」：它们低频，
 * 且多数是破坏性动作，摊在第一屏只会把"看一眼余额"变成一次浏览任务。
 */
export function AccountCenter() {
    return (
        <div className="account-page">
            <AccountCreditsCard />
            <AccountProfileCard />
            <AccountPasswordCard />
            <AccountMoreSection>
                <AccountDevicesCard />
                <AccountBindingsCard />
                <MyCreationPostsCard />
                <AccountUsageCard />
                <AccountDeletionCard />
            </AccountMoreSection>
        </div>
    );
}
