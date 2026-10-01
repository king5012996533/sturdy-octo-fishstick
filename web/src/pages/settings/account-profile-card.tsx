import { App, Button, Input } from "antd";
import { useCallback, useEffect, useState } from "react";

import { getAccountProfile, updateAccountProfile, type AccountProfile } from "@/services/api/account-profile";
import { ApiError } from "@/services/api/request";

/**
 * 「资料」：昵称与头像。
 *
 * 身份条就是编辑区——头像和昵称在页面顶部直接可改，改完点保存。把"我长什么样"和
 * "改成什么样"拆成上下两处，是上一版最刺眼的问题：同一份信息在一屏里出现两遍，
 * 读者要先分辨哪个是读数、哪个是表单。
 *
 * 头像地址收在「更换头像」后面：它是一个 URL，不该和昵称并排摆在第一眼的位置。
 * 放在这里而不是弹窗，是因为预览必须实时——用户要看到自己贴的地址能不能显示出图。
 */
export function AccountProfileCard() {
    const { message } = App.useApp();
    const [profile, setProfile] = useState<AccountProfile | null>(null);
    const [name, setName] = useState("");
    const [avatarUrl, setAvatarUrl] = useState("");
    const [editingAvatar, setEditingAvatar] = useState(false);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        try {
            const payload = await getAccountProfile();
            setProfile(payload);
            setName(payload.name);
            setAvatarUrl(payload.avatarUrl);
            setError("");
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : "读取个人资料失败");
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
    }, [load]);

    const save = async () => {
        setSaving(true);
        try {
            const updated = await updateAccountProfile({ name, avatarUrl });
            setProfile(updated);
            setName(updated.name);
            setAvatarUrl(updated.avatarUrl);
            setError("");
            message.success("资料已保存");
        } catch (saveError) {
            // 服务端的校验文案原样透出：本地再翻译一层，就会在规则改动后说错原因。
            message.error(saveError instanceof ApiError ? saveError.message : "保存失败，请稍后重试");
        } finally {
            setSaving(false);
        }
    };

    const dirty = profile !== null && (name.trim() !== profile.name || avatarUrl.trim() !== profile.avatarUrl);
    const shownAvatar = avatarUrl.trim();
    const initial = (name.trim() || profile?.email || profile?.phone || "K").slice(0, 1).toUpperCase();
    const contact = profile?.email || profile?.phone || "";

    return (
        <section className="account-card" aria-labelledby="account-profile-title">
            <div className="account-card-head">
                <h2 id="account-profile-title" className="account-card-title">资料</h2>
                {contact ? <span className="account-card-aside">{contact}</span> : null}
            </div>

            <div className="account-identity-row">
                {shownAvatar ? (
                    // 头像地址可能指向任何 https 资源，加载失败时浏览器显示破图；不预加载探活，
                    // 探活会让每次打开这一页都多发一条请求。
                    <span className="account-avatar-lg"><img src={shownAvatar} alt="" referrerPolicy="no-referrer" /></span>
                ) : (
                    <span className="account-avatar-lg" aria-hidden>{initial}</span>
                )}
                <label className="account-field is-flush min-w-0 flex-1">
                    <span className="account-field-label">昵称</span>
                    <Input
                        value={name}
                        maxLength={30}
                        placeholder={loading ? "正在读取…" : "给自己起个名字"}
                        onChange={(event) => setName(event.target.value)}
                    />
                </label>
            </div>

            {editingAvatar ? (
                <div className="account-field">
                    <span className="account-field-label">头像地址（留空则用昵称首字母）</span>
                    <Input
                        value={avatarUrl}
                        placeholder="https://…"
                        allowClear
                        onChange={(event) => setAvatarUrl(event.target.value)}
                    />
                </div>
            ) : null}

            {error ? <p className="account-error">{error}</p> : null}

            <div className="account-card-foot">
                {editingAvatar ? (
                    <span className="account-card-aside">头像地址留空即用昵称首字母</span>
                ) : (
                    <Button className="account-quiet-button" type="link" size="small" onClick={() => setEditingAvatar(true)}>
                        更换头像
                    </Button>
                )}
                <div className="account-card-foot-actions">
                    {dirty ? <span className="account-card-aside">有未保存的改动</span> : null}
                    <Button type="primary" loading={saving} disabled={!dirty} onClick={() => void save()}>
                        保存
                    </Button>
                </div>
            </div>
        </section>
    );
}
