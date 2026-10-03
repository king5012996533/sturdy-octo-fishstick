import { libtvSampleSource } from "./creation-inspirations-libtv";

/**
 * 广场页脚：左边一句栏目口号，右边是"当前进度"的索引条 —— 左端是已展示张数、右端是总数。
 * 左端原先写死成"01"（纯装饰），展开更多时整条不动，看起来就像占位符；换成真实计数后
 * 它才是真的进度条。
 *
 * 素材来源说明保留在索引条左侧的折叠块里：它是版权口径的凭证（封面与提示词来自哪里、
 * 上线前要替换成什么），不能为了版面干净直接删掉；折叠起来就既不抢视线，又随时可查。
 *
 * 说明里只有文字，没有外链。这里曾经挂过一个指向上游作品页的链接，那等于在自家广场的
 * 页脚上给上游引流；版权口径写明来源就够了，不需要一个可点的出口。
 */

/** 把条数补成两位数：索引条按"两个等宽数字夹一条细线"排版，位宽固定才不会随计数左右晃。 */
function padCount(value: number) {
    return String(Math.max(value, 0)).padStart(2, "0");
}

export function CreationInspirationFooter({ shown, total, unit }: { shown: number; total?: number; unit: string }) {
    const ceiling = total ?? shown;
    return (
        <footer className="creation-inspiration-footer">
            <span className="creation-inspiration-motto">Create your next scene</span>
            <div className="creation-inspiration-index">
                <details className="creation-inspiration-sources-fold">
                    <summary>模板与封面来源</summary>
                    <p>{libtvSampleSource.notice}</p>
                </details>
                {/* 数字本身是排版元素，读屏读"13 — 58"没有意义，所以整条按一张图标注。 */}
                <span className="creation-inspiration-pagebar" role="img" aria-label={`已展示 ${shown} / ${ceiling} ${unit}`}>
                    <span aria-hidden="true">{padCount(shown)}</span>
                    <i aria-hidden="true" />
                    <span aria-hidden="true">{padCount(ceiling)}</span>
                </span>
            </div>
        </footer>
    );
}
