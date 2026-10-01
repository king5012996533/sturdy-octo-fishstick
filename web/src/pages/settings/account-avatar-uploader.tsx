import { useRef, useState } from "react";
import { App } from "antd";

import { clearAccountAvatar, uploadAccountAvatar, type AccountProfile } from "@/services/api/account-profile";
import { ApiError } from "@/services/api/request";

/**
 * 头像：点头像换一张，没头像时显示昵称首字母。
 *
 * 上传独立成一个组件，是因为它有自己的三条状态——选文件前的校验、上传中的禁用、
 * 上传后的回填——把它们摊进资料卡会和"改昵称"的脏检查、保存按钮纠缠在一起。
 *
 * 头像本身就画成按钮而不是在旁边再放一个"更换头像"：用户点哪张图就改哪张图，
 * 是这个动作最不需要解释的形态。
 */

/** 与服务端 avatar.MaxBytes 对齐。 */
export const AVATAR_MAX_BYTES = 2 * 1024 * 1024;
/** 与服务端 avatar.allowedMIMETypes 对齐。 */
export const AVATAR_ACCEPTED_TYPES = ["image/png", "image/jpeg", "image/webp"];

/**
 * 只做即时提示，最终以服务端为准。
 *
 * file.type 可能为空（某些系统不按扩展名上报），这时放行交给服务端嗅探判断，
 * 而不是在这里猜——猜错的表现是"明明能用的图传不上去"，比多一次往返更糟。
 */
export function validateAvatarFile(file: File): string {
    if (file.size > AVATAR_MAX_BYTES) return "头像不能超过 2MB";
    if (file.type && !AVATAR_ACCEPTED_TYPES.includes(file.type.toLowerCase())) return "头像仅支持 PNG、JPG 或 WebP 图片";
    return "";
}

export function AccountAvatarUploader({ avatarUrl, fallback, onChange }: {
    avatarUrl: string;
    /** 没有头像时显示的占位字符，通常是昵称首字母。 */
    fallback: string;
    onChange: (profile: AccountProfile) => void;
}) {
    const { message } = App.useApp();
    const inputRef = useRef<HTMLInputElement>(null);
    const [busy, setBusy] = useState(false);

    const pickFile = () => inputRef.current?.click();

    const handleFile = async (file: File | undefined) => {
        if (!file) return;
        const complaint = validateAvatarFile(file);
        if (complaint) {
            message.error(complaint);
            return;
        }
        setBusy(true);
        try {
            onChange(await uploadAccountAvatar(file));
            message.success("头像已更新");
        } catch (error) {
            message.error(error instanceof ApiError ? error.message : "头像上传失败，请稍后重试");
        } finally {
            setBusy(false);
            // 清空 value：选同一个文件第二次不会触发 change，用户会以为点了没反应。
            if (inputRef.current) inputRef.current.value = "";
        }
    };

    const shown = avatarUrl.trim();

    return (
        <>
            <button
                type="button"
                className={`account-avatar-lg account-avatar-button${busy ? " is-busy" : ""}`}
                onClick={pickFile}
                disabled={busy}
                aria-label={shown ? "更换头像" : "上传头像"}
                title={shown ? "更换头像" : "上传头像"}
                data-testid="account-avatar-trigger"
            >
                {shown ? <img src={shown} alt="" referrerPolicy="no-referrer" /> : <span aria-hidden>{fallback}</span>}
            </button>
            <input
                ref={inputRef}
                type="file"
                className="account-avatar-input"
                accept={AVATAR_ACCEPTED_TYPES.join(",")}
                onChange={(event) => void handleFile(event.target.files?.[0])}
                data-testid="account-avatar-input"
            />
        </>
    );
}

/**
 * 移除头像。
 *
 * 与上传分成两个组件，是因为两个动作在不同位置：上传的入口是那张图本身，移除是页脚里
 * 一个次要动作。合成一个组件就得把页脚的排版搬进来，而页脚还要放保存按钮。
 */
export function AccountAvatarRemoveButton({ disabled, onChange }: {
    disabled: boolean;
    onChange: (profile: AccountProfile) => void;
}) {
    const { message } = App.useApp();
    const [busy, setBusy] = useState(false);
    return (
        <button
            type="button"
            className="account-quiet-button account-link-button"
            disabled={disabled || busy}
            data-testid="account-avatar-remove"
            onClick={() => {
                void (async () => {
                    setBusy(true);
                    try {
                        onChange(await clearAccountAvatar());
                        message.success("头像已移除");
                    } catch (error) {
                        message.error(error instanceof ApiError ? error.message : "移除头像失败，请稍后重试");
                    } finally {
                        setBusy(false);
                    }
                })();
            }}
        >
            {busy ? "移除中…" : "移除头像"}
        </button>
    );
}
