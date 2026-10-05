import { Button, Input, Modal, Select, Switch, Tag } from "antd";
import { Globe, Image as ImageIcon, Palette, RefreshCw, RotateCcw, Save, Trash2, Upload } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { commitPublicAppearance, useAppearanceStore } from "@/stores/use-appearance-store";

import {
    getAdminAppearance,
    resetAdminAppearance,
    updateAdminAppearance,
    uploadAdminAppearanceAsset,
    type AdminAppearanceAssetSlot,
    type AdminAppearanceInput,
    type AdminAppearanceSetting,
} from "./api";

type Draft = Omit<AdminAppearanceInput, "skinThemes"> & { skinThemes: AdminAppearanceSetting["skinThemes"] };

function draftOf(setting: AdminAppearanceSetting): Draft {
    const { public: _public, configured: _configured, updatedBy: _updatedBy, createdAt: _createdAt, updatedAt: _updatedAt, schemaVersion: _schemaVersion, ...rest } = setting;
    return rest;
}

/** 五个上传槽位：浅色 Logo、深色 Logo、品牌视频、视频封面与充值收款码。 */
const assetSlots: Array<{ slot: AdminAppearanceAssetSlot; label: string; hint: string; accept: string; kind: "image" | "video"; field: keyof Draft }> = [
    { slot: "logo", label: "浅色模式 Logo", hint: "PNG / JPEG / WebP，5MB 以内，建议方形透明底", accept: "image/png,image/jpeg,image/webp", kind: "image", field: "logoResourceId" },
    { slot: "logo-dark", label: "深色模式 Logo", hint: "留空则沿用浅色 Logo", accept: "image/png,image/jpeg,image/webp", kind: "image", field: "darkLogoResourceId" },
    { slot: "poster", label: "视频封面", hint: "PNG / JPEG / WebP，10MB 以内", accept: "image/png,image/jpeg,image/webp", kind: "image", field: "authVideoPosterResourceId" },
    { slot: "video", label: "登录页品牌视频", hint: "MP4 / WebM，256MB 以内", accept: "video/mp4,video/webm", kind: "video", field: "authVideoResourceId" },
    { slot: "payment-qr", label: "充值收款二维码", hint: "PNG / JPEG / WebP，5MB 以内。配置后展示在用户端积分中心的充值区，供支付渠道接通前扫码付款、运营手工补单", accept: "image/png,image/jpeg,image/webp", kind: "image", field: "paymentQrResourceId" },
];

/** Go 的 time.Time 零值会序列化成 0001-01-01，展示成"1/1/1"比留空更让人困惑。 */
function formatUpdatedAt(value?: string) {
    if (!value) return "—";
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime()) || parsed.getFullYear() < 2000) return "—";
    return parsed.toLocaleString();
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
    return (
        <label className="admin-settings-field">
            <span className="admin-settings-field-label">{label}</span>
            {children}
            {hint ? <span className="admin-settings-field-hint">{hint}</span> : null}
        </label>
    );
}

function AssetPreview({ kind, url }: { kind: "image" | "video"; url: string }) {
    if (!url) return <span className="admin-asset-placeholder">未配置</span>;
    if (kind === "video") return <video className="admin-asset-preview" src={url} muted playsInline preload="metadata" />;
    return <img className="admin-asset-preview" src={url} alt="" />;
}

/**
 * 站点设置（品牌与外观）。
 *
 * 这里改的是"平台长什么样"而不是"我怎么用"：品牌名、Logo、登录页文案、皮肤与备案号
 * 全部由服务端存储并投影给匿名前台，因此保存后必须立刻回写本地 appearance store，
 * 否则运营会在后台看到旧品牌、误以为没生效。
 */
export function SettingsPane() {
    const [setting, setSetting] = useState<AdminAppearanceSetting | null>(null);
    const [draft, setDraft] = useState<Draft | null>(null);
    const [dirty, setDirty] = useState(false);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [resetting, setResetting] = useState(false);
    const [uploadingSlot, setUploadingSlot] = useState("");
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [resetOpen, setResetOpen] = useState(false);
    const fileInputs = useRef<Record<string, HTMLInputElement | null>>({});
    const storeBrandName = useAppearanceStore((state) => state.appearance.brandName);

    const load = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const payload = await getAdminAppearance();
            setSetting(payload);
            // 未保存的草稿不能被刷新冲掉：运营可能刚粘完一段文案。
            setDraft((current) => (dirty && current ? current : draftOf(payload)));
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "加载站点设置失败");
        } finally {
            setLoading(false);
        }
    }, [dirty]);

    useEffect(() => {
        void load();
    }, [load]);

    const update = (patch: Partial<Draft>) => {
        setDraft((current) => (current ? { ...current, ...patch } : current));
        setDirty(true);
        setNotice("");
    };

    const upload = async (slot: AdminAppearanceAssetSlot, field: keyof Draft, file: File | undefined) => {
        if (!file) return;
        setUploadingSlot(slot);
        setError("");
        setNotice("");
        try {
            const resource = await uploadAdminAppearanceAsset(slot, file);
            update({ [field]: resource.id } as Partial<Draft>);
            setNotice("资源已上传，点击「保存设置」后才会对前台生效。");
        } catch (uploadError) {
            setError(uploadError instanceof Error ? uploadError.message : "资源上传失败");
        } finally {
            setUploadingSlot("");
            const input = fileInputs.current[slot];
            if (input) input.value = "";
        }
    };

    const save = async () => {
        if (!draft) return;
        setSaving(true);
        setError("");
        setNotice("");
        try {
            const payload = await updateAdminAppearance(draft);
            setSetting(payload);
            setDraft(draftOf(payload));
            setDirty(false);
            commitPublicAppearance(payload.public);
            setNotice("站点设置已保存，前台立即生效。");
        } catch (saveError) {
            setError(saveError instanceof Error ? saveError.message : "保存站点设置失败");
        } finally {
            setSaving(false);
        }
    };

    const reset = async () => {
        setResetting(true);
        setError("");
        setNotice("");
        try {
            const payload = await resetAdminAppearance();
            setSetting(payload);
            setDraft(draftOf(payload));
            setDirty(false);
            commitPublicAppearance(payload.public);
            setResetOpen(false);
            setNotice("已恢复为内置默认品牌标识。");
        } catch (resetError) {
            setError(resetError instanceof Error ? resetError.message : "恢复默认失败");
        } finally {
            setResetting(false);
        }
    };

    if (loading && !draft) {
        return (
            <div className="flex flex-col gap-4">
                <div className="admin-section-head">
                    <div>
                        <h2 className="admin-section-title">站点设置</h2>
                        <p className="admin-section-desc">品牌名称、Logo、登录页文案、皮肤与备案信息。</p>
                    </div>
                </div>
                <div className="admin-card admin-empty">正在加载站点设置…</div>
            </div>
        );
    }

    if (!draft) {
        return (
            <div className="flex flex-col gap-4">
                <div className="admin-section-head">
                    <div>
                        <h2 className="admin-section-title">站点设置</h2>
                        <p className="admin-section-desc">品牌名称、Logo、登录页文案、皮肤与备案信息。</p>
                    </div>
                    <Button icon={<RefreshCw className="size-3.5" />} onClick={() => void load()}>重试</Button>
                </div>
                {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}
            </div>
        );
    }

    const publicAppearance = setting?.public;
    const skinOptions = draft.skinThemes.map((skin) => ({
        value: skin.id,
        label: <span className="flex items-center gap-2">{skin.name}{skin.locked ? <Tag>内置</Tag> : null}</span>,
    }));

    return (
        <div className="flex flex-col gap-4">
            <div className="admin-section-head">
                <div>
                    <h2 className="admin-section-title">站点设置</h2>
                    <p className="admin-section-desc">品牌名称、Logo、登录页文案、皮肤与备案信息。保存后匿名前台立即生效，已登录用户下次刷新可见。</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button icon={<RefreshCw className="size-3.5" />} loading={loading} onClick={() => void load()}>刷新</Button>
                    <Button icon={<RotateCcw className="size-3.5" />} onClick={() => setResetOpen(true)}>恢复默认</Button>
                </div>
            </div>

            {error ? <div className="admin-notice is-error"><span>{error}</span></div> : null}
            {notice ? <div className="admin-notice is-ok"><span>{notice}</span></div> : null}
            {!setting?.configured ? (
                <div className="admin-notice">
                    <Globe className="size-3.5 shrink-0" />
                    <span>当前使用的是内置默认品牌（{storeBrandName}），尚未发布过自定义站点设置。</span>
                </div>
            ) : null}

            <div className="admin-settings-grid">
                <section className="admin-card admin-settings-section">
                    <h3 className="admin-settings-title"><Palette className="size-3.5" />品牌与文案</h3>
                    <Field label="品牌名称" hint="展示在侧栏、标签页与邮件里，1-40 个字符。">
                        <Input maxLength={40} value={draft.brandName} onChange={(event) => update({ brandName: event.target.value })} />
                    </Field>
                    <Field label="英文标识" hint="小写字母、数字与连字符，不能以连字符开头或结尾。">
                        <Input maxLength={48} value={draft.brandSlug} onChange={(event) => update({ brandSlug: event.target.value })} />
                    </Field>
                    <Field label="登录页主标题" hint="支持换行；留空会回落到内置标语。">
                        <Input.TextArea rows={2} maxLength={80} value={draft.authHeroTitle} onChange={(event) => update({ authHeroTitle: event.target.value })} />
                    </Field>
                    <Field label="登录页说明文案" hint="最多 160 字。">
                        <Input.TextArea rows={2} maxLength={160} value={draft.authHeroDescription} onChange={(event) => update({ authHeroDescription: event.target.value })} />
                    </Field>
                    <Field label="皮肤主题" hint="主题令牌由系统内置，后台只切换当前使用的主题。">
                        <Select value={draft.skinId} options={skinOptions} onChange={(value) => update({ skinId: value })} />
                    </Field>
                </section>

                <section className="admin-card admin-settings-section">
                    <h3 className="admin-settings-title"><ImageIcon className="size-3.5" />品牌与收款资源</h3>
                    {assetSlots.map((item) => {
                        const resourceId = String(draft[item.field] ?? "");
                        const previewURL = setting?.public
                            ? item.slot === "logo"
                                ? setting.public.logoUrl
                                : item.slot === "logo-dark"
                                    ? setting.public.darkLogoUrl
                                    : item.slot === "poster"
                                        ? setting.public.authVideoPosterUrl
                                        : item.slot === "payment-qr"
                                            ? setting.public.paymentQrUrl
                                            : setting.public.authVideoUrl
                            : "";
                        return (
                            <div className="admin-asset-row" key={item.slot}>
                                <div className="admin-asset-thumb">
                                    <AssetPreview kind={item.kind} url={resourceId ? previewURL : ""} />
                                </div>
                                <div className="admin-asset-meta">
                                    <span className="admin-settings-field-label">{item.label}</span>
                                    <span className="admin-settings-field-hint">{item.hint}</span>
                                    <span className="admin-asset-state">
                                        {uploadingSlot === item.slot ? "上传中…" : resourceId ? <Tag color="green">已选择自定义资源</Tag> : <Tag>使用默认</Tag>}
                                    </span>
                                </div>
                                <div className="admin-asset-actions">
                                    <input
                                        ref={(node) => { fileInputs.current[item.slot] = node; }}
                                        type="file"
                                        accept={item.accept}
                                        hidden
                                        onChange={(event) => void upload(item.slot, item.field, event.target.files?.[0])}
                                    />
                                    <Button
                                        size="small"
                                        icon={<Upload className="size-3.5" />}
                                        loading={uploadingSlot === item.slot}
                                        disabled={Boolean(uploadingSlot) && uploadingSlot !== item.slot}
                                        onClick={() => fileInputs.current[item.slot]?.click()}
                                    >
                                        上传
                                    </Button>
                                    <Button size="small" type="text" icon={<Trash2 className="size-3.5" />} disabled={!resourceId} onClick={() => update({ [item.field]: "" } as Partial<Draft>)}>
                                        清除
                                    </Button>
                                </div>
                            </div>
                        );
                    })}
                    <div className="admin-settings-inline">
                        <span>Logo 加框</span>
                        <Switch checked={draft.logoFrameEnabled} onChange={(checked) => update({ logoFrameEnabled: checked })} />
                    </div>
                    <div className="admin-settings-inline">
                        <span>登录页视频自动播放</span>
                        <Switch checked={draft.authVideoAutoplay} onChange={(checked) => update({ authVideoAutoplay: checked })} />
                    </div>
                </section>

                <section className="admin-card admin-settings-section">
                    <h3 className="admin-settings-title"><Globe className="size-3.5" />SEO 与页脚</h3>
                    <Field label="SEO 标题" hint="留空则回落到品牌名称，最多 70 字。">
                        <Input maxLength={70} value={draft.seoTitle} onChange={(event) => update({ seoTitle: event.target.value })} />
                    </Field>
                    <Field label="SEO 描述" hint="最多 200 字。">
                        <Input.TextArea rows={2} maxLength={200} value={draft.seoDescription} onChange={(event) => update({ seoDescription: event.target.value })} />
                    </Field>
                    <Field label="SEO 关键词" hint="英文逗号分隔，最多 300 字。">
                        <Input maxLength={300} value={draft.seoKeywords} onChange={(event) => update({ seoKeywords: event.target.value })} />
                    </Field>
                    <Field label="页脚版权" hint="留空则按品牌名称自动生成当前年份版权。">
                        <Input maxLength={160} value={draft.footerCopyright} onChange={(event) => update({ footerCopyright: event.target.value })} />
                    </Field>
                    <div className="admin-settings-inline">
                        <span>展示 ICP 备案号</span>
                        <Switch checked={draft.icpFilingEnabled} onChange={(checked) => update({ icpFilingEnabled: checked })} />
                    </div>
                    <Field label="ICP 备案号" hint="开启展示前必填。">
                        <Input maxLength={64} disabled={!draft.icpFilingEnabled} value={draft.icpFilingNumber} onChange={(event) => update({ icpFilingNumber: event.target.value })} />
                    </Field>
                </section>

                <section className="admin-card admin-settings-section">
                    <h3 className="admin-settings-title">版本信息</h3>
                    <div className="admin-settings-facts">
                        <span>投影版本<b>{publicAppearance?.revision ?? "builtin"}</b></span>
                        <span>最近更新<b>{formatUpdatedAt(setting?.updatedAt)}</b></span>
                        <span>更新人<b>{setting?.updatedBy || "—"}</b></span>
                        <span>当前皮肤<b>{publicAppearance?.activeSkin?.name ?? draft.skinId}</b></span>
                    </div>
                    <div className="admin-settings-inline">
                        <span>Logo 自定义<b>{publicAppearance?.logoConfigured ? "是" : "否"}</b></span>
                        <span>品牌视频自定义<b>{publicAppearance?.authVideoConfigured ? "是" : "否"}</b></span>
                    </div>
                </section>
            </div>

            <div className="admin-settings-actions">
                <Button type="primary" icon={<Save className="size-3.5" />} loading={saving} disabled={!dirty} onClick={() => void save()}>
                    保存设置
                </Button>
                <span className="admin-ink-faint" style={{ fontSize: "var(--fs-caption)" }}>
                    {dirty ? "有未保存的修改" : "与线上一致"}
                </span>
            </div>

            <Modal
                open={resetOpen}
                title="恢复为默认品牌？"
                okText="确认恢复"
                cancelText="取消"
                confirmLoading={resetting}
                onOk={() => void reset()}
                onCancel={() => setResetOpen(false)}
            >
                <p style={{ fontSize: "var(--fs-caption)", color: "var(--admin-ink-dim)", lineHeight: 1.8 }}>
                    品牌名称、Logo、登录页文案、皮肤与备案信息都会回到 KinoTV 内置默认值：
                    登录页会重新显示内置视频与默认标识。此操作不影响账号与画布数据。
                </p>
            </Modal>
        </div>
    );
}
