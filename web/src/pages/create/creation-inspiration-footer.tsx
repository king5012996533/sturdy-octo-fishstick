import { libtvSampleSource } from "./creation-inspirations-libtv";

/**
 * 广场页脚：左边一句栏目口号，右边是条目数的索引条 —— 数字 + 细线 + 单位。
 * 这里曾经排成"01 — 58"的进度条，但广场早已改成一次全出，没有"已展示/总数"这回事；
 * 左端那个写死的"01"更是个纯装饰，看着就像占位符。所以只留总数。
 *
 * 素材来源说明保留在索引条左侧的折叠块里：它是版权口径的凭证（封面与提示词来自哪里、
 * 上线前要替换成什么），不能为了版面干净直接删掉；折叠起来就既不抢视线，又随时可查。
 *
 * 说明里只有文字，没有外链。这里曾经挂过一个指向上游作品页的链接，那等于在自家广场的
 * 页脚上给上游引流；版权口径写明来源就够了，不需要一个可点的出口。
 */

/** 把条数补成两位数：位宽固定，切换筛选时整条不会跟着计数左右晃。 */
function padCount(value: number) {
    return String(Math.max(value, 0)).padStart(2, "0");
}

export function CreationInspirationFooter({ count, unit }: { count: number; unit: string }) {
    return (
        <footer className="creation-inspiration-footer">
            <span className="creation-inspiration-motto">Create your next scene</span>
            <div className="creation-inspiration-index">
                <details className="creation-inspiration-sources-fold">
                    <summary>模板与封面来源</summary>
                    <p>{libtvSampleSource.notice}</p>
                </details>
                {/* 数字与单位是排版元素，读屏读"58 个创意"没有意义，所以整条按一张图标注。 */}
                <span className="creation-inspiration-pagebar" role="img" aria-label={`共 ${count} ${unit}`}>
                    <span aria-hidden="true">{padCount(count)}</span>
                    <i aria-hidden="true" />
                    <span aria-hidden="true">{unit}</span>
                </span>
            </div>
        </footer>
    );
}
