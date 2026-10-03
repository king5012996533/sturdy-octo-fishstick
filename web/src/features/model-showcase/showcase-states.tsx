import type { ReactNode } from "react";

/** 广场三种非正常状态的统一呈现：骨架、失败、空结果。三个页面共用同一份，避免各写一套。 */

export function ShowcaseSkeletonGrid({ count = 6 }: { count?: number }) {
    return (
        <div className="showcase-grid" aria-hidden>
            {Array.from({ length: count }, (_, index) => (
                <div key={index} className="showcase-skeleton" />
            ))}
        </div>
    );
}

export function ShowcaseErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
    return (
        <div className="showcase-state" role="alert">
            <strong>没能加载出来</strong>
            <p>{message}</p>
            <button type="button" className="showcase-retry" onClick={onRetry}>
                重试
            </button>
        </div>
    );
}

export function ShowcaseEmptyState({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
    return (
        <div className="showcase-state">
            <strong>{title}</strong>
            <p>{description}</p>
            {action}
        </div>
    );
}
