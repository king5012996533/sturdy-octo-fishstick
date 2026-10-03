import { describe, expect, test } from "bun:test";
import {
    CREATION_INSPIRATION_PAGE_SIZE,
    CREATION_INSPIRATION_PAGE_STEP,
    creationInspirationProgress,
    creationInspirationRevealIndex,
    revealCreationInspirationCard,
} from "../src/pages/create/creation-inspiration-more";

/**
 * 「查看更多」曾经只有一句 setLimit(+12)：新卡片落在首屏之外，按钮和页脚都不动，点完
 * 与没点一样。这些用例钉住"点下去必须看得见变化"的三处口径：剩余张数、页脚计数、
 * 以及把第一张新卡卷回视野。
 */

type FakeCard = { scrollIntoView: (options: ScrollIntoViewOptions) => void };

function fakeGrid(cardCount: number) {
    const calls: Array<{ index: number; options: ScrollIntoViewOptions }> = [];
    const cards: FakeCard[] = Array.from({ length: cardCount }, (_, index) => ({
        scrollIntoView: (options) => calls.push({ index, options }),
    }));
    return { grid: { querySelectorAll: () => cards } as unknown as HTMLElement, calls };
}

describe("广场查看更多", () => {
    test("首屏 13 张、每次 12 张：四列 bento 铺满整行", () => {
        expect(CREATION_INSPIRATION_PAGE_SIZE).toBe(13);
        expect(CREATION_INSPIRATION_PAGE_STEP).toBe(12);
    });

    test("进度按 limit 截断到总数，剩余张数就是按钮上的文案", () => {
        expect(creationInspirationProgress(CREATION_INSPIRATION_PAGE_SIZE, 58)).toEqual({ shown: 13, total: 58, remaining: 45 });
        expect(creationInspirationProgress(25, 58)).toEqual({ shown: 25, total: 58, remaining: 33 });
    });

    test("展开到超过总数时按总数封顶：剩余为 0，按钮随之消失", () => {
        expect(creationInspirationProgress(99, 58)).toEqual({ shown: 58, total: 58, remaining: 0 });
        expect(creationInspirationRevealIndex(99, 58)).toBeNull();
    });

    test("总数还没加载出来（0 条）时不预留空位", () => {
        expect(creationInspirationProgress(CREATION_INSPIRATION_PAGE_SIZE, 0)).toEqual({ shown: 0, total: 0, remaining: 0 });
        expect(creationInspirationRevealIndex(13, 0)).toBeNull();
    });

    test("展开后滚动的是第一张新卡，且只滚最小距离", () => {
        const { grid, calls } = fakeGrid(58);
        revealCreationInspirationCard(grid, creationInspirationRevealIndex(CREATION_INSPIRATION_PAGE_SIZE, 58));
        expect(calls).toHaveLength(1);
        expect(calls[0]?.index).toBe(13);
        expect(calls[0]?.options.block).toBe("nearest");
    });

    test("没有新卡、没有容器、序号越界时都不滚动：不能把用户已经看到的位置挪走", () => {
        const { grid, calls } = fakeGrid(13);
        revealCreationInspirationCard(grid, null);
        revealCreationInspirationCard(null, 13);
        revealCreationInspirationCard(grid, 13);
        revealCreationInspirationCard(grid, -1);
        expect(calls).toHaveLength(0);
    });
});
