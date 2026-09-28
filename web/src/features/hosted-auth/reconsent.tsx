import { Alert, App, Button, Checkbox, Tabs, Typography } from "antd";
import { useEffect, useState } from "react";

import { acceptHostedAuthAgreements, fetchHostedAuthAgreements, logoutHostedAuth, type HostedAuthAgreements } from "./api";

/**
 * 强制重签页。
 *
 * 条款更新后，签过旧版本的账号在进入工作台之前必须在这里补一次同意：留痕的价值
 * 在于"用户确实看过这一版"，默认放行等于把记录写成假的。不同意就只剩退出登录。
 */
export function HostedAuthReconsent({ currentVersion, onAccepted }: { currentVersion: string; onAccepted: () => void }) {
    const { message } = App.useApp();
    const [agreements, setAgreements] = useState<HostedAuthAgreements | null>(null);
    const [agreed, setAgreed] = useState(false);
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        let cancelled = false;
        void fetchHostedAuthAgreements()
            .then((payload) => {
                if (!cancelled) setAgreements(payload);
            })
            .catch((error) => {
                if (!cancelled) message.error(error instanceof Error ? error.message : "协议加载失败，请刷新重试");
            });
        return () => {
            cancelled = true;
        };
    }, [message]);

    const accept = async () => {
        setSubmitting(true);
        try {
            await acceptHostedAuthAgreements(currentVersion);
            onAccepted();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "提交失败，请稍后重试");
        } finally {
            setSubmitting(false);
        }
    };

    const logout = async () => {
        try {
            await logoutHostedAuth();
        } finally {
            window.location.reload();
        }
    };

    return (
        <main className="flex min-h-screen items-center justify-center px-6 py-10">
            <section className="flex w-full max-w-[680px] flex-col gap-4 rounded-[14px] border border-white/10 bg-white/[0.03] p-6 backdrop-blur-xl">
                <header className="flex flex-col gap-1">
                    <h1 className="text-lg font-semibold">用户协议已更新</h1>
                    <p className="text-sm opacity-70">新版本 {currentVersion}，请阅读并同意后继续使用。不同意可选择退出登录。</p>
                </header>

                <Alert type="info" showIcon message="本次同意会记录版本号、时间与来源，作为你的签署凭据。" />

                <div className="max-h-[46vh] overflow-y-auto rounded-[10px] border border-white/10 bg-black/20 px-4">
                    {agreements ? (
                        <Tabs
                            items={agreements.documents.map((document) => ({
                                key: document.type,
                                label: document.title,
                                children: (
                                    <Typography.Paragraph className="whitespace-pre-wrap text-sm leading-6">{document.body}</Typography.Paragraph>
                                ),
                            }))}
                        />
                    ) : (
                        <p className="py-6 text-sm opacity-60">协议加载中…</p>
                    )}
                </div>

                <Checkbox checked={agreed} onChange={(event) => setAgreed(event.target.checked)}>
                    我已阅读并同意上述协议
                </Checkbox>

                <div className="flex items-center gap-3">
                    <Button type="primary" disabled={!agreed || !agreements} loading={submitting} onClick={() => void accept()}>
                        同意并继续
                    </Button>
                    <Button type="text" onClick={() => void logout()}>
                        退出登录
                    </Button>
                </div>
            </section>
        </main>
    );
}
