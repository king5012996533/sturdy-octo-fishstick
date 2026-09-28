import { App, Button, InputNumber } from "antd";
import { RotateCcw, Save } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { getAdminRuntimePolicy, resetAdminRuntimePolicy, updateAdminRuntimePolicy, type AdminRuntimePolicySetting } from "./api";

type PolicyGroupKey = "resource" | "task" | "request";

/**
 * 字段名 → 中文标签。
 *
 * 这里只做展示映射，不做字段清单：数值字段以后端返回的键为准逐个渲染，
 * 后端新增一项就会自动出现在面板上，避免"加了配置项但后台漏了入口"。
 */
const fieldLabels: Record<string, string> = {
    resourceUploadMB: "单次上传上限",
    generatedFileMB: "单文件生成上限",
    dailyUploadMB: "每日上传总量",
    storedFileGB: "存储配额",
    structuredDataMB: "结构化数据额度",
    taskDataGB: "任务数据额度",
    assetCount: "素材条数上限",
    canvasCount: "画布数量上限",
    taskCount: "任务数量上限",
    apiCallLogCount: "调用日志保留条数",
    recycleBinRetentionDays: "回收站保留天数",
    workerConcurrency: "任务并发",
    channelConcurrency: "渠道并发",
    activeTaskLimit: "单用户并行任务",
    imageTimeoutMinutes: "图片超时",
    textTimeoutMinutes: "文本超时",
    audioTimeoutMinutes: "音频超时",
    videoTimeoutMinutes: "视频超时",
    storyboardTimeoutMinutes: "分镜超时",
    defaultTimeoutMinutes: "默认超时",
    taskCreatePerMinute: "任务创建频控",
    resourceUploadPerMinute: "资源上传频控",
    resourceImportPerMinute: "资源导入频控",
    assetWritePerMinute: "素材写入频控",
    canvasWritePerMinute: "画布写入频控",
    systemRelayPerMinute: "平台转发频控",
    customRelayPerMinute: "自建转发频控",
    customRelayConcurrency: "自建转发并发",
    customRelayRequestMB: "自建转发请求上限",
    customRelayResponseMB: "自建转发响应上限",
    customRelayTimeoutMinutes: "自建转发超时",
    systemRelayRequestMB: "平台转发请求上限",
    systemRelayResponseMB: "平台转发响应上限",
    systemRelayTimeoutMinutes: "平台转发超时",
    channelCircuitFailureCount: "熔断失败阈值",
    channelCircuitOpenSeconds: "熔断开路时长",
};

const groupMeta: Record<PolicyGroupKey, { title: string; description: string; unit: Record<string, string> }> = {
    resource: {
        title: "资源额度",
        description: "存储、数量与保留期的硬上限；超过后写入被拒绝。",
        unit: { resourceUploadMB: "MB", generatedFileMB: "MB", dailyUploadMB: "MB", storedFileGB: "GB", structuredDataMB: "MB", taskDataGB: "GB", recycleBinRetentionDays: "天" },
    },
    task: {
        title: "任务与超时",
        description: "并发与各类任务的超时上限，单位分钟。",
        unit: {
            imageTimeoutMinutes: "分钟",
            textTimeoutMinutes: "分钟",
            audioTimeoutMinutes: "分钟",
            videoTimeoutMinutes: "分钟",
            storyboardTimeoutMinutes: "分钟",
            defaultTimeoutMinutes: "分钟",
        },
    },
    request: {
        title: "频控与熔断",
        description: "按分钟计数的请求频控，以及上游故障时的熔断阈值。",
        unit: {
            taskCreatePerMinute: "/分钟",
            resourceUploadPerMinute: "/分钟",
            resourceImportPerMinute: "/分钟",
            assetWritePerMinute: "/分钟",
            canvasWritePerMinute: "/分钟",
            systemRelayPerMinute: "/分钟",
            customRelayPerMinute: "/分钟",
            customRelayRequestMB: "MB",
            customRelayResponseMB: "MB",
            customRelayTimeoutMinutes: "分钟",
            systemRelayRequestMB: "MB",
            systemRelayResponseMB: "MB",
            systemRelayTimeoutMinutes: "分钟",
            channelCircuitOpenSeconds: "秒",
        },
    },
};

const groupOrder: PolicyGroupKey[] = ["resource", "task", "request"];

/** 运行时策略。0 或空值由后端按字段上下限校验，前端只做范围兜底。 */
export function PolicyPane() {
    const { message } = App.useApp();
    const [policy, setPolicy] = useState<AdminRuntimePolicySetting | null>(null);
    const [saved, setSaved] = useState<AdminRuntimePolicySetting | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);

    const load = useCallback(async () => {
        setLoading(true);
        try {
            const payload = await getAdminRuntimePolicy();
            setPolicy(payload);
            setSaved(payload);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "加载运行时策略失败");
        } finally {
            setLoading(false);
        }
    }, [message]);

    useEffect(() => {
        void load();
    }, [load]);

    const dirty = useMemo(() => JSON.stringify(policy) !== JSON.stringify(saved), [policy, saved]);

    const updateField = (group: PolicyGroupKey, field: string, value: number | null) => {
        setPolicy((current) => (current ? { ...current, [group]: { ...current[group], [field]: value ?? 0 } } : current));
    };

    const save = async () => {
        if (!policy) return;
        setSaving(true);
        try {
            const payload = await updateAdminRuntimePolicy(policy);
            setPolicy(payload);
            setSaved(payload);
            message.success("运行时策略已保存");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "保存运行时策略失败");
        } finally {
            setSaving(false);
        }
    };

    const reset = async () => {
        setSaving(true);
        try {
            const payload = await resetAdminRuntimePolicy();
            setPolicy(payload);
            setSaved(payload);
            message.success("已恢复默认策略");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "重置运行时策略失败");
        } finally {
            setSaving(false);
        }
    };

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">运行时策略</h2>
                    <p className="admin-section-desc">平台级配额与限流。保存后对新请求立即生效，进行中的任务按旧值结算。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button icon={<RotateCcw className="size-3.5" />} loading={saving} onClick={() => void reset()}>
                        恢复默认
                    </Button>
                    <Button type="primary" icon={<Save className="size-3.5" />} loading={saving} disabled={!dirty} onClick={() => void save()}>
                        保存
                    </Button>
                </div>
            </div>

            {groupOrder.map((group) => {
                const values = policy?.[group] ?? {};
                const meta = groupMeta[group];
                return (
                    <div key={group} className="admin-card">
                        <div className="admin-card-head">
                            <span className="flex min-w-0 flex-col">
                                <b style={{ fontSize: "var(--fs-body)" }}>{meta.title}</b>
                                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>{meta.description}</span>
                            </span>
                        </div>
                        <div className="admin-card-pad grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
                            {Object.keys(values).length ? (
                                Object.entries(values).map(([field, value]) => (
                                    <label key={field} className="flex flex-col gap-1">
                                        <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-dim)" }}>
                                            {fieldLabels[field] ?? field}
                                            {meta.unit[field] ? <span style={{ color: "var(--admin-ink-faint)" }}> · {meta.unit[field]}</span> : null}
                                        </span>
                                        <InputNumber
                                            min={0}
                                            className="w-full"
                                            value={value}
                                            disabled={loading || saving}
                                            onChange={(next) => updateField(group, field, next)}
                                        />
                                    </label>
                                ))
                            ) : (
                                <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>正在读取…</span>
                            )}
                        </div>
                    </div>
                );
            })}

            <div className="admin-inline-note">
                <span>平台转发超时与体积上限直接决定流式响应能否跑完：视频类模型建议预留 10 分钟以上，响应上限至少 64 MB。</span>
            </div>
        </div>
    );
}
