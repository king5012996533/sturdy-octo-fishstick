import { App, Button, Input } from "antd";
import { useCallback, useEffect, useState } from "react";

import { getAccountProfile, updateAccountProfile, type AccountProfile } from "@/services/api/account-profile";
import { ApiError } from "@/services/api/request";

import { AccountAvatarRemoveButton, AccountAvatarUploader } from "./account-avatar-uploader";

/**
 * 「资料」：昵称与头像。
 *
 * 身份条就是编辑区——头像和昵称在页面顶部直接可改，改完点保存。把"我长什么样"和
 * "改成什么样"拆成上下两处，是上一版最刺眼的问题：同一份信息在一屏里出现两遍，
 * 读者要先分辨哪个是读数、哪个是表单。
 *
 * 头像从"填地址"改成"传图片"之后，它就不该再占表单的一行了：地址是服务端签发的，
 * 用户既看不到也不需要理解它。点那张图换一张，是这一页唯一需要存在的说明。
 */
export function AccountProfileCard() {
    const { message } = App.useApp();
    const [profile, setProfile] = useState<AccountProfile | null>(null);
    const [name, setName] = useState("");
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");

    const load = useCallback(async () => {
        setLoading(true);
        try {
            const payload = await getAccountProfile();
            setProfile(payload);
            setName(payload.name);
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

    /** 头像上传即生效，因此它不参与"有未保存的改动"：它是已经落库的，不是草稿。 */
    const applyAvatar = useCallback((updated: AccountProfile) => {
        setProfile(updated);
    }, []);

    const save = async () => {
        if (!profile) return;
        setSaving(true);
        try {
            // 头像原样回传：接口一直接受它，而这里回传的正是服务端刚写下的值，
            // 重复写一次是空操作，却省掉一个"改昵称顺手清空头像"的分支。
            const updated = await updateAccountProfile({ name, avatarUrl: profile.avatarUrl });
            setProfile(updated);
            setName(updated.name);
            setError("");
            message.success("资料已保存");
        } catch (saveError) {
            // 服务端的校验文案原样透出：本地再翻译一层，就会在规则改动后说错原因。
            message.error(saveError instanceof ApiError ? saveError.message : "保存失败，请稍后重试");
        } finally {
            setSaving(false);
        }
    };

    const dirty = profile !== null && name.trim() !== profile.name;
    const contact = profile?.email || profile?.phone || "";
    const initial = (name.trim() || profile?.email || profile?.phone || "K").slice(0, 1).toUpperCase();

    return (
        <section className="account-card" aria-labelledby="account-profile-title">
            <div className="account-card-head">
                <h2 id="account-profile-title" className="account-card-title">资料</h2>
                {contact ? <span className="account-card-aside">{contact}</span> : null}
            </div>

            <div className="account-identity-row">
                <AccountAvatarUploader avatarUrl={profile?.avatarUrl ?? ""} fallback={initial} onChange={applyAvatar} />
                <label className="account-field is-flush min-w-0 flex-1">
                    <span className="account-field-label">昵称</span>
                    <Input
                        value={name}
                        maxLength={30}
                        placeholder={loading ? "正在读取…" : "给自己起个名字"}
                        disabled={loading}
                        onChange={(event) => setName(event.target.value)}
                    />
                </label>
            </div>

            {error ? <p className="account-error">{error}</p> : null}

            <div className="account-card-foot">
                {profile?.avatarUrl ? (
                    <AccountAvatarRemoveButton disabled={loading || saving} onChange={applyAvatar} />
                ) : (
                    <span className="account-card-aside">{loading ? "正在读取头像…" : "点头像即可上传一张图片"}</span>
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
