import { App, Button, Switch } from "antd";
import { RotateCcw, Save } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { useUserStore } from "@/stores/use-user-store";

import { getAdminFeatures, updateAdminFeatures, type AdminFeatureAvailability } from "./api";

type FeatureKey = "shortDramaEnabled" | "taskCenterEnabled" | "customChannelsEnabled" | "frontendModelsEnabled" | "pluginCenterEnabled" | "systemPluginsVisibleToUsers" | "timelineTranscriptionEnabled";

const featureGroups: Array<{ title: string; description: string; items: Array<{ key: FeatureKey; label: string; note: string }> }> = [
    {
        title: "前台形态",
        description: "这两项决定用户端是「平台托管模型」还是「用户自配模型」，也决定计费是否成立。",
        items: [
            { key: "customChannelsEnabled", label: "允许用户自建渠道（接口语义）", note: "SaaS 产物里前台没有自建渠道界面，这项只影响接口与计费语义；本地/桌面版仍按它显示配置入口。" },
            { key: "frontendModelsEnabled", label: "允许前台自带模型", note: "打开后用户可用自己的密钥直连，平台转发端点会整体关闭（403），计费链路同时失效。" },
        ],
    },
    {
        title: "模块开放",
        description: "面向用户的创作模块开关，关闭后入口与接口一并下线。",
        items: [
            { key: "shortDramaEnabled", label: "短剧创作", note: "短剧相关的项目与任务能力。" },
            { key: "taskCenterEnabled", label: "任务中心", note: "任务列表与执行记录。" },
            { key: "pluginCenterEnabled", label: "插件中心", note: "用户可见的插件管理与安装。" },
            { key: "systemPluginsVisibleToUsers", label: "系统插件对用户可见", note: "平台预置插件是否出现在用户端列表。" },
            { key: "timelineTranscriptionEnabled", label: "时间线转写", note: "音视频时间线的语音转写能力。" },
        ],
    },
];

const defaultFeatures: AdminFeatureAvailability = {
    shortDramaEnabled: true,
    taskCenterEnabled: true,
    customChannelsEnabled: true,
    frontendModelsEnabled: false,
    pluginCenterEnabled: true,
    systemPluginsVisibleToUsers: true,
    timelineTranscriptionEnabled: true,
};

/**
 * 功能开放。
 *
 * 前端模型与自建渠道是"前台形态"的两把开关，而不是可随意组合的功能位：
 * 打开前台模型就等于把执行凭证交回用户手里，平台转发与计费随之失效，
 * 因此这两项在同一次保存里提交，并给出后果说明。
 */
export function FeaturesPane() {
    const { message } = App.useApp();
    const currentUser = useUserStore((state) => state.user);
    const [features, setFeatures] = useState<AdminFeatureAvailability>(defaultFeatures);
    const [saved, setSaved] = useState<AdminFeatureAvailability | null>(null);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);

    const load = useCallback(async () => {
        setLoading(true);
        try {
            const payload = await getAdminFeatures();
            setFeatures({ ...defaultFeatures, ...payload });
            setSaved(payload);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "加载功能开放配置失败");
        } finally {
            setLoading(false);
        }
    }, [message]);

    useEffect(() => {
        void load();
    }, [load]);

    const dirty = useMemo(() => {
        if (!saved) return false;
        return (Object.keys(defaultFeatures) as FeatureKey[]).some((key) => features[key] !== saved[key]);
    }, [features, saved]);

    const save = async () => {
        setSaving(true);
        try {
            const payload = await updateAdminFeatures(features);
            setFeatures({ ...defaultFeatures, ...payload });
            setSaved(payload);
            message.success("功能开放已保存");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "保存功能开放失败");
        } finally {
            setSaving(false);
        }
    };

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">功能开放</h2>
                    <p className="admin-section-desc">改动立即对全部用户生效，无需重启服务。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button icon={<RotateCcw className="size-3.5" />} onClick={() => void load()} disabled={loading || saving}>
                        重新读取
                    </Button>
                    <Button type="primary" icon={<Save className="size-3.5" />} loading={saving} disabled={!dirty} onClick={() => void save()}>
                        保存
                    </Button>
                </div>
            </div>

            {saved && !saved.configured ? (
                <div className="admin-inline-note">
                    <span>当前还没有落库的配置，界面显示的是默认值。保存一次即由管理后台接管。</span>
                </div>
            ) : null}

            {featureGroups.map((group) => (
                <div key={group.title} className="admin-card">
                    <div className="admin-card-head">
                        <span className="flex min-w-0 flex-col">
                            <b style={{ fontSize: "var(--fs-body)" }}>{group.title}</b>
                            <span style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>{group.description}</span>
                        </span>
                    </div>
                    <div className="admin-card-pad flex flex-col gap-3">
                        {group.items.map((item) => (
                            <div key={item.key} className="flex items-start justify-between gap-4">
                                <span className="flex min-w-0 flex-col gap-0.5">
                                    <b style={{ fontSize: "var(--fs-caption)", fontWeight: 500 }}>{item.label}</b>
                                    <span style={{ fontSize: "var(--fs-label)", lineHeight: 1.7, color: "var(--admin-ink-faint)" }}>{item.note}</span>
                                </span>
                                <Switch
                                    checked={features[item.key]}
                                    disabled={loading || saving}
                                    aria-label={item.label}
                                    onChange={(checked) => setFeatures((current) => ({ ...current, [item.key]: checked }))}
                                />
                            </div>
                        ))}
                    </div>
                </div>
            ))}

            {saved?.updatedAt ? (
                <p style={{ fontSize: "var(--fs-label)", color: "var(--admin-ink-faint)" }}>
                    最近更新：{new Date(saved.updatedAt).toLocaleString("zh-CN")}
                    {saved.updatedBy ? ` · ${saved.updatedBy === currentUser?.id ? currentUser.displayName || currentUser.username : saved.updatedBy}` : ""}
                </p>
            ) : null}
        </div>
    );
}
