import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import type { TaskChargeEstimate } from "../src/hooks/use-task-charge-quote";
import { CreationCreditEstimate } from "../src/pages/create/creation-credit-estimate";
import type { TaskChargeQuote } from "../src/services/api/credit";

/**
 * 生成按钮旁的消耗提示。
 *
 * 文本会话的"起步价"与"最低余额水位"是两件事：余额不到水位时用户根本发不出去，
 * 提示必须说清要留多少，只把起步价染红会让人以为"充 1 积分就能继续"。
 */
function quote(overrides: Partial<TaskChargeQuote>): TaskChargeQuote {
    return {
        credits: 1,
        unit: "REQUEST",
        quantity: 1,
        surchargeCredits: 0,
        sellUnitPrice: 1,
        multiplierBp: 10000,
        multiplierSource: "DEFAULT",
        priced: true,
        minimumBalance: 0,
        ...overrides,
    };
}

function estimate(overrides: Partial<TaskChargeEstimate>): TaskChargeEstimate {
    return { loading: false, quote: null, balance: null, sufficient: true, error: "", unsupported: false, ...overrides };
}

describe("CreationCreditEstimate", () => {
    test("文本：余额不到水位时指出需要多少，而不是只显示起步价", () => {
        const html = renderToStaticMarkup(
            <CreationCreditEstimate estimate={estimate({ quote: quote({ minimumBalance: 40 }), balance: 12, sufficient: false })} />,
        );
        expect(html).toContain("余额需 ≥ 40 积分");
        expect(html).toContain("当前 12");
        // 起步价此时不是重点：它会让用户以为这就是总价。
        expect(html).not.toContain("预计预扣");
    });

    test("文本：余额够时仍显示起步价，并在算式里说明收尾按真实用量结算", () => {
        const html = renderToStaticMarkup(
            <CreationCreditEstimate estimate={estimate({ quote: quote({ minimumBalance: 40 }), balance: 100, sufficient: true })} />,
        );
        expect(html).toContain("预计预扣");
        expect(html).toContain("按实际 token 用量结算");
    });

    test("图片按张：水位为 0 时不出现余额要求", () => {
        const html = renderToStaticMarkup(
            <CreationCreditEstimate estimate={estimate({ quote: quote({ unit: "IMAGE", credits: 60, quantity: 2, sellUnitPrice: 30 }), balance: 500, sufficient: true })} />,
        );
        expect(html).toContain("预计预扣");
        expect(html).not.toContain("余额需");
    });
});
