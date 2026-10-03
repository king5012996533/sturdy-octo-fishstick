/**
 * 积分单价的中文读法。
 *
 * 广场价格表与创作台的消耗提示都渲染同一批价目行，两处各写一份 switch，很快就会出现
 * 「广场写 30 积分/张、按钮写 30 积分/次」这类对不上的读数。单位枚举与文案只维护在这里。
 */
export function creditUnitRateLabel(unit: string, sellUnitPrice: number): string {
    const price = sellUnitPrice.toLocaleString("zh-CN");
    switch (unit) {
        case "IMAGE":
            return `${price} 积分/张`;
        case "SECOND":
            return `${price} 积分/秒`;
        case "TOKEN_1M":
            return `${price} 积分/百万 token`;
        case "TOKEN_1K":
            return `${price} 积分/千 token`;
        default:
            return `${price} 积分/次`;
    }
}
