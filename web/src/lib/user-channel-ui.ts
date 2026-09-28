/**
 * 用户端是否可能出现「自建渠道」界面。
 *
 * 托管产物（SaaS）由平台持有上游凭证与计费，前台不再提供任何渠道/密钥配置面：
 * 这里不是"用功能开关把它藏起来"，而是构建期就不存在——入口不渲染，页面对应的
 * 代码路径也拿不到。功能开关（`customChannelsEnabled`）在托管实例上只剩接口与
 * 计费语义，本地/桌面版则照常按它决定是否显示配置入口。
 */
export function userChannelConfigVisible(customChannelsEnabled: boolean) {
    if (__BEEFTV_HOSTED_AUTH__) return false;
    return customChannelsEnabled;
}
