import { priceLabel, tierLabel, unitLabel } from "./presentation";
import type { ShowcasePrice } from "./api";

/**
 * 价格表：档位 / 单价 / 计价单位。
 *
 * 单价直接渲染后端给的整数积分，不在前端做元与分的换算——全站口径就是"积分"，
 * 换算一次就会与账单页出现两套数字。未定价的档位显示"暂不可用"，绝不显示 0。
 */
export function ShowcasePriceTable({ prices }: { prices: ShowcasePrice[] }) {
    if (!prices.length) {
        return <p className="showcase-lead">该模型暂未开放价格档位。</p>;
    }
    return (
        <table className="showcase-table">
            <thead>
                <tr>
                    <th scope="col">档位</th>
                    <th scope="col">计价单位</th>
                    <th scope="col">单价</th>
                </tr>
            </thead>
            <tbody>
                {prices.map((price) => {
                    const available = price.priced && price.sellUnitPrice !== null;
                    return (
                        <tr key={`${price.priceTier}-${price.unit}`}>
                            <td>{tierLabel(price.priceTier)}</td>
                            <td>{unitLabel(price.unit)}</td>
                            <td className={available ? undefined : "is-unavailable"}>{priceLabel(price)}</td>
                        </tr>
                    );
                })}
            </tbody>
        </table>
    );
}
