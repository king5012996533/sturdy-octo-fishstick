import { Button } from "antd";
import { ShieldAlert } from "lucide-react";
import { Link } from "react-router";

import type { OwnCanvasModeration } from "@/services/api/canvas-moderation";

/**
 * 画布被平台下架时的落地页。
 *
 * 不用 403/404 的裸提示顶替：用户需要知道"是哪一块、为什么、还能做什么"，
 * 而平台下架本来就该给出理由。
 */
export function CanvasModerationBlocked({ notice }: { notice: OwnCanvasModeration }) {
    const removed = notice.status === "REMOVED";
    return (
        <main className="flex h-full items-center justify-center px-6">
            <div className="flex w-full max-w-[420px] flex-col items-center gap-3 rounded-[var(--panel-radius-wide-tight,12px)] border border-white/10 bg-white/[0.03] px-6 py-8 text-center backdrop-blur-xl">
                <span className="grid size-10 place-items-center rounded-full border border-amber-400/30 bg-amber-400/10 text-amber-300" aria-hidden>
                    <ShieldAlert className="size-5" />
                </span>
                <p className="text-base font-medium" role="alert">这块画布已被平台{removed ? "移除" : "下架"}</p>
                <p className="text-sm opacity-80">{notice.reason ? `处置理由：${notice.reason}` : "平台未附具体理由，如需申诉请联系客服。"}</p>
                <p className="text-xs opacity-55">
                    {removed ? "内容已从平台移除，无法继续编辑；如需找回请联系客服复核。" : "画布内容与素材仍在，复核通过后即可继续编辑。"}
                </p>
                <Link to="/canvas" className="mt-1">
                    <Button type="primary">返回画布库</Button>
                </Link>
            </div>
        </main>
    );
}
