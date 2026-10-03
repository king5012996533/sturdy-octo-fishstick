import { useCallback, useEffect, useState } from "react";

import { getShowcaseModel, listShowcaseModels, type ShowcaseModel } from "./api";

/**
 * 广场两个页面的取数钩子。
 *
 * 用 AbortController 而不是"忽略过期响应"的标志位：用户从列表点进详情、又快速点回来时，
 * 旧请求会真的被取消，不会和新的响应抢着写同一个 state。
 * 刷新时保留已有数据（只把 loading 打开），避免重试按钮点下去整页闪成骨架。
 */

export type ShowcaseListState = {
    models: ShowcaseModel[] | null;
    loading: boolean;
    error: string;
    reload: () => void;
};

export function useShowcaseModels(): ShowcaseListState {
    const [models, setModels] = useState<ShowcaseModel[] | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [revision, setRevision] = useState(0);

    useEffect(() => {
        const controller = new AbortController();
        setLoading(true);
        setError("");
        listShowcaseModels(controller.signal)
            .then((next) => {
                if (!controller.signal.aborted) setModels(next);
            })
            .catch((cause) => {
                if (!controller.signal.aborted) setError(errorText(cause, "模型列表加载失败，请稍后重试。"));
            })
            .finally(() => {
                if (!controller.signal.aborted) setLoading(false);
            });
        return () => controller.abort();
    }, [revision]);

    const reload = useCallback(() => setRevision((value) => value + 1), []);
    return { models, loading, error, reload };
}

export type ShowcaseDetailState = {
    model: ShowcaseModel | null;
    loading: boolean;
    error: string;
    reload: () => void;
};

export function useShowcaseModel(slug: string): ShowcaseDetailState {
    const [model, setModel] = useState<ShowcaseModel | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [revision, setRevision] = useState(0);

    useEffect(() => {
        const controller = new AbortController();
        setLoading(true);
        setError("");
        getShowcaseModel(slug, controller.signal)
            .then((next) => {
                if (!controller.signal.aborted) setModel(next);
            })
            .catch((cause) => {
                if (!controller.signal.aborted) setError(errorText(cause, "模型详情加载失败，请稍后重试。"));
            })
            .finally(() => {
                if (!controller.signal.aborted) setLoading(false);
            });
        return () => controller.abort();
    }, [slug, revision]);

    const reload = useCallback(() => setRevision((value) => value + 1), []);
    return { model, loading, error, reload };
}

/** 后端业务错误已经带可读文案，直接透出；网络层的失败才用兜底句。 */
function errorText(error: unknown, fallback: string): string {
    return error instanceof Error && error.message.trim() ? error.message : fallback;
}
