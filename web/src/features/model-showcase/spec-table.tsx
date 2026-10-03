import { specRows } from "./presentation";
import type { ShowcaseModel } from "./api";

/** 参数表：只渲染已发布的能力合同里确实存在的行，缺值的行整行不出现。 */
export function ShowcaseSpecTable({ model }: { model: ShowcaseModel }) {
    const rows = specRows(model.spec, model.capability);
    if (!rows.length) {
        return <p className="showcase-lead">该模型暂无额外可选参数。</p>;
    }
    return (
        <table className="showcase-table showcase-spec-table">
            <tbody>
                {rows.map((row) => (
                    <tr key={row.label}>
                        <td className="showcase-spec-label">{row.label}</td>
                        <td>{row.value}</td>
                    </tr>
                ))}
            </tbody>
        </table>
    );
}
