import { userChannelConfigVisible } from "@/lib/user-channel-ui";
import { useUserStore } from "@/stores/use-user-store";

export type SettingsSection = "channels" | "models" | "preferences" | "prompts" | "storage";

/**
 * 用户能不能自己配渠道。托管实例里恒为 false：设置页只剩一页说明，
 * 任何"配置未就绪"的引导跳过去都是死胡同。
 */
export function canOpenChannelSettings() {
    return userChannelConfigVisible(useUserStore.getState().features.customChannelsEnabled);
}

export function settingsPath(section: SettingsSection = "channels", continueCreation = false) {
    const params = new URLSearchParams({ section });
    if (continueCreation) params.set("continue", "1");
    return `/settings?${params.toString()}`;
}

/**
 * 画布深层组件没有路由上下文出口时统一跳转到正式设置页，避免重新引入全局配置弹窗。
 *
 * 托管实例由平台持有模型与计费，前台没有任何可配置项，跳过去只会落到一页说明上，
 * 因此这里直接拦下、不做任何跳转：用户在画布里比在一个说明页上更有用。
 * 可见反馈由各入口自己表达（例如"该能力没有可用模型"时把提交按钮与模型选择器置灰），
 * 而不是靠全局 toast——本项目已经全局关掉了 antd 的 message/notification。
 */
export function navigateToSettings(options?: { section?: SettingsSection; continueCreation?: boolean }) {
    const to = settingsPath(options?.section, options?.continueCreation);
    if (!canOpenChannelSettings()) return;
    const event = new CustomEvent<{ to: string }>("workspace:navigate", { detail: { to }, cancelable: true });
    if (window.dispatchEvent(event)) window.location.assign(to);
}
