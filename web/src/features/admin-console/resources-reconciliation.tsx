import { Button, Modal, Table, Tag, type TableProps } from "antd";
import { useState } from "react";

import { formatCount, formatDateTime } from "@/lib/format-usage";

import { backfillAdminResourceProvenance, type AdminResourceReconciliation, type AdminTaskCharge } from "./api-resources";

const chargeStateMeta: Record<string, { label: string; color: string }> = {
    untracked: { label: "未关联任务", color: "default" },
    prebilling: { label: "计费前", color: "default" },
    charged: { label: "已扣费", color: "green" },
    uncharged: { label: "未扣费", color: "red" },
};

export function chargeStateLabel(state: string) {
    return chargeStateMeta[state]?.label ?? (state || "—");
}

export function chargeStateColor(state: string) {
    return chargeStateMeta[state]?.color ?? "default";
}

/**
 * 对账异常面板与历史回填入口。
 *
 * 两个方向分开列：产物无扣费是平台在替上游垫钱，扣费无产物是用户付了钱没拿到东西，
 * 处置动作完全不同，混在一张表里只会让人不知道该找谁。
 */
export function ResourcesReconciliation({
    open,
    reconciliation,
    onClose,
    onBackfilled,
}: {
    open: boolean;
    reconciliation: AdminResourceReconciliation | null;
    onClose: () => void;
    onBackfilled: () => void;
}) {
    const [backfillNotice, setBackfillNotice] = useState("");
    const [backfillError, setBackfillError] = useState("");
    const [backfilling, setBackfilling] = useState(false);
    const [confirmApply, setConfirmApply] = useState(false);

    const runBackfill = async (dryRun: boolean) => {
        setBackfilling(true);
        setBackfillError("");
        setBackfillNotice("");
        try {
            const payload = await backfillAdminResourceProvenance(dryRun);
            const result = payload.result;
            const prefix = dryRun ? "演练结果" : "回填完成";
            setBackfillNotice(
                `${prefix}：扫过 ${formatCount(result.tasksScanned)} 个任务，待关联 ${formatCount(result.resourcesMissing)} 条，可关联 ${formatCount(result.linked)} 条，仍对不上 ${formatCount(result.unmatched)} 条。`,
            );
            if (dryRun) {
                setConfirmApply(result.linked > 0);
            } else {
                setConfirmApply(false);
                onBackfilled();
            }
        } catch (error) {
            setBackfillError(error instanceof Error ? error.message : "回填失败");
        } finally {
            setBackfilling(false);
        }
    };

    const taskColumns: TableProps<AdminTaskCharge>["columns"] = [
        {
            title: "任务",
            key: "task",
            render: (_, row) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-sub admin-canvas-id">{row.taskId}</span>
                    <span className="admin-user-sub">{row.taskType || "—"} · {row.taskStatus || "—"}</span>
                </div>
            ),
        },
        {
            title: "账号",
            key: "owner",
            width: 190,
            render: (_, row) => (
                <div className="flex min-w-0 flex-col gap-1">
                    <span className="admin-user-name">{row.userName || row.userId}</span>
                    <span className="admin-user-sub">{row.userId}</span>
                </div>
            ),
        },
        { title: "净扣费", key: "net", width: 96, render: (_, row) => <Tag color="red">{formatCount(row.net)} 积分</Tag> },
        { title: "扣费时间", key: "chargedAt", width: 168, render: (_, row) => <span className="admin-user-sub">{formatDateTime(row.chargedAt)}</span> },
        {
            title: "任务报错",
            key: "error",
            render: (_, row) => <span className="admin-user-sub admin-canvas-reason">{row.taskError || "—"}</span>,
        },
    ];

    const uncharged = reconciliation?.unchargedResources ?? [];

    return (
        <Modal open={open} width={980} footer={null} title="对账异常与历史回填" onCancel={onClose}>
            <div className="flex flex-col gap-4">
                <div className="admin-notice">
                    <span>
                        计费起点：{reconciliation?.billingStart ? formatDateTime(reconciliation.billingStart) : "账号库里还没有任何任务扣费，暂不做漏单判定"}。
                        回填只补空的 task_id，可以重复执行。
                    </span>
                    <span className="flex items-center gap-2">
                        <Button size="small" loading={backfilling} onClick={() => void runBackfill(true)}>
                            演练回填
                        </Button>
                        {confirmApply ? (
                            <Button size="small" type="primary" loading={backfilling} onClick={() => void runBackfill(false)}>
                                确认写入
                            </Button>
                        ) : null}
                    </span>
                </div>

                {backfillError ? <div className="admin-notice is-error"><span>{backfillError}</span></div> : null}
                {backfillNotice ? <div className="admin-notice is-ok"><span>{backfillNotice}</span></div> : null}

                <div>
                    <h3 className="admin-section-title">产物无扣费（计费上线后） · {formatCount(reconciliation?.uncharged ?? 0)}</h3>
                    <p className="admin-section-desc">
                        上游已经产出、平台却没有对应扣费。列表最多展示 50 条，超出部分按时间范围再筛。
                    </p>
                    {uncharged.length === 0 ? (
                        <div className="admin-card admin-empty">没有漏扣费的产物。</div>
                    ) : (
                        <div className="admin-card">
                            <Table
                                rowKey="id"
                                size="small"
                                dataSource={uncharged}
                                pagination={false}
                                scroll={{ y: 240 }}
                                columns={[
                                    { title: "产物 ID", dataIndex: "id", key: "id", render: (value: string) => <span className="admin-user-sub admin-canvas-id">{value}</span> },
                                    { title: "账号", key: "owner", width: 170, render: (_, row) => <span className="admin-user-sub">{row.userName || row.userId}</span> },
                                    { title: "任务", dataIndex: "taskId", key: "taskId", width: 300, render: (value: string) => <span className="admin-user-sub admin-canvas-id">{value}</span> },
                                    { title: "创建时间", dataIndex: "createdAt", key: "createdAt", width: 168, render: (value: string) => <span className="admin-user-sub">{formatDateTime(value)}</span> },
                                ]}
                            />
                        </div>
                    )}
                </div>

                <div>
                    <h3 className="admin-section-title">扣费无产物 · {formatCount(reconciliation?.chargedWithoutResource ?? 0)}</h3>
                    <p className="admin-section-desc">
                        扣了费、任务也跑过，却一条产物都没有的图片 / 视频 / 音频任务。文本任务不在其中——它本来就不产出资源。
                    </p>
                    {reconciliation?.chargedTasks?.length ? (
                        <div className="admin-card">
                            <Table<AdminTaskCharge>
                                rowKey="taskId"
                                size="small"
                                dataSource={reconciliation.chargedTasks}
                                columns={taskColumns}
                                pagination={false}
                                scroll={{ x: 860 }}
                            />
                        </div>
                    ) : (
                        <div className="admin-card admin-empty">没有扣费却没有产物的任务。</div>
                    )}
                </div>
            </div>
        </Modal>
    );
}
