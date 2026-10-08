import { useEffect, useMemo, useRef, useState } from "react";
import { ImageSettingsPanel } from "@/components/image-settings-panel";
import { ModelPicker } from "@/components/model-picker";
import type { CanvasTheme } from "@/lib/canvas-theme";
import { agentApprovalModel, agentApprovalModelSelection, createAgentApprovalSettingsDraft, type AgentImageApproval } from "@/lib/canvas/agent-media-approval";
import { modelCapabilityConfigFor, normalizeImageValue } from "@/lib/model-capabilities";
import { resolveCompatibleModel, type ModelRequirements } from "@/lib/model-selection";
import type { AgentMediaSettings } from "@/services/api/agent";
import { PUBLIC_MODEL_CATALOG_ID, useEffectiveConfig } from "@/stores/use-config-store";

export function CanvasAgentImageApprovalSettings({ initial, value, onChange, theme, disabled }: {
    initial: AgentImageApproval;
    value?: AgentMediaSettings;
    onChange: (value: AgentMediaSettings) => void;
    theme: CanvasTheme;
    disabled: boolean;
}) {
    const effective = useEffectiveConfig();
    const config = useMemo(() => ({ ...effective, channels: effective.channels.filter((channel) => channel.scope === "system" || channel.id === PUBLIC_MODEL_CATALOG_ID) }), [effective]);
    const settings = value || initial;
    const model = agentApprovalModel(config, settings);
    const [rejection, setRejection] = useState("");
    // 规格改动可能在一个事件里提交多次（改比例会先提交尺寸、再提交画质）。
    // 基准必须同 tick 同步推进，否则后一次提交会拿渲染快照把前一次覆盖掉。
    const draftRef = useRef<ReturnType<typeof createAgentApprovalSettingsDraft> | null>(null);
    if (!draftRef.current) draftRef.current = createAgentApprovalSettingsDraft(settings);
    useEffect(() => {
        draftRef.current?.sync(settings);
    }, [settings]);
    const requirements: ModelRequirements = {
        capability: "image",
        input: { textCount: 1, imageCount: initial.referenceNodeIds.length, videoCount: 0, audioCount: 0, characterCount: 0 },
        imageSize: settings.size,
        options: { size: settings.size, ...(settings.quality ? { quality: settings.quality } : {}), count: 1 },
    };
    // 只有平台模型能走 Agent 生成：非平台渠道在这里会被拒，必须给用户原因而不是静默失败。
    const selectionFor = (nextModel: string) => {
        try {
            return agentApprovalModelSelection(config, nextModel);
        } catch {
            return null;
        }
    };
    const changeModel = (next: string) => {
        if (disabled) return;
        const profile = modelCapabilityConfigFor(config, next).image;
        if (!profile) {
            setRejection("该模型暂不支持图片生成，请换一个模型");
            return;
        }
        const selection = selectionFor(next);
        if (!selection) {
            setRejection("本次生成只能使用平台模型，请换一个模型");
            return;
        }
        const normalized = normalizeImageValue(profile, draftRef.current!.current());
        setRejection("");
        onChange(draftRef.current!.commit({ ...selection, size: normalized.size, quality: profile.quality.supported ? normalized.quality : "" }));
    };
    const changeOption = (key: "quality" | "size" | "transparentBackground" | "count", next: string) => {
        if (disabled || (key !== "size" && key !== "quality")) return;
        const base = draftRef.current!.current();
        const candidate = { ...base, [key]: next };
        const selected = resolveCompatibleModel(config, agentApprovalModel(config, base) || model, {
            ...requirements,
            imageSize: candidate.size,
            options: { size: candidate.size, ...(candidate.quality ? { quality: candidate.quality } : {}), count: 1 },
        });
        if (!selected) {
            setRejection("当前模型不支持该规格，换个规格或模型再试");
            return;
        }
        const selection = selectionFor(selected);
        if (!selection) {
            setRejection("本次生成只能使用平台模型，请换一个模型");
            return;
        }
        setRejection("");
        // 只提交本次改动的键：尺寸与画质是两次提交，带上整份快照会互相覆盖。
        onChange(draftRef.current!.commit({ ...selection, [key]: next }));
    };
    return <fieldset disabled={disabled} className="min-w-0 space-y-3 border-0 p-0" data-canvas-no-zoom data-canvas-wheel-scroll aria-label="图片生成设置">
        <div className="space-y-1.5">
            <div className="text-xs" style={{ color: theme.node.muted }}>生成模型</div>
            <ModelPicker config={config} capability="image" value={model} onChange={changeModel} requirements={requirements} variant="creation" fullWidth placeholder="选择生成模型" popoverClassName="agent-model-picker-popover" />
        </div>
        {model ? <ImageSettingsPanel config={{ ...config, model, imageModel: model, size: settings.size, quality: settings.quality, count: "1" }} onConfigChange={changeOption} theme={theme} showTitle={false} showCount={false} showTransparent={false} className="min-w-0 space-y-3" /> : <p className="text-xs" style={{ color: theme.node.muted }}>当前模型不在可选目录中，可重新选择；提交时将重新校验模型与规格。</p>}
        <p className="text-xs" style={{ color: theme.node.text }}>本次规格：{settings.size}{settings.quality ? ` · ${settings.quality}` : ""}</p>
        {rejection ? <p role="status" className="text-xs" style={{ color: theme.node.muted }}>{rejection}</p> : null}
        <p className="text-xs" style={{ color: theme.node.muted }}>修改仅用于本次生成，费用按最终模型和规格计算。</p>
    </fieldset>;
}
