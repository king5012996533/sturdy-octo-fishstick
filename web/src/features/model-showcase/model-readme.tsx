import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

/**
 * 自述文件正文渲染。
 *
 * 用 Markdown 而不是富文本：正文的来源是上游页面（抓取后落成 Markdown 存库），
 * 改成富文本等于给运营一套需要学一遍的编辑器，而这里要的只是"能读"。
 *
 * 两条硬约束：
 *
 *   - 不渲染原始 HTML（react-markdown 默认就不渲染，这里显式不打开 rehype-raw）：
 *     正文最终来自外部页面，允许内联 HTML 等于把注入面开到页面上。
 *   - 不渲染链接，只留文字：模型页是入口不是中转站，把访客送去上游页面等于替对方
 *     做导流，顺带把我们的上游渠道摊开给人看。
 */
export function ModelReadme({ markdown }: { markdown: string }) {
    return (
        <div className="doc-prose">
            <Markdown
                remarkPlugins={[remarkGfm]}
                components={{
                    // 链接降级成普通文字：保留可读性，不给出跳转。
                    a: ({ children }) => <span className="doc-link-text">{children}</span>,
                    // 图片一律不渲染：正文里的图指向上游 CDN，会变成一片死图或代理请求。
                    img: () => null,
                }}
            >
                {markdown}
            </Markdown>
        </div>
    );
}
