import { Modal, Tabs, Typography } from "antd";

import type { HostedAuthAgreements } from "./api";

/**
 * 协议正文查看器。
 *
 * 正文来自服务端：条款更新不需要重新发布前端，也保证「用户看到的」与「服务端记录的
 * 版本」是同一份内容。
 */
export function HostedAuthAgreementDialog({ open, agreements, initialType, onClose }: { open: boolean; agreements: HostedAuthAgreements | null; initialType: string | null; onClose: () => void }) {
    const activeKey = initialType ?? "TERMS";
    return (
        <Modal open={open} onCancel={onClose} footer={null} width={680} title="用户协议与隐私政策" data-testid="hosted-auth-agreements">
            {agreements ? (
                <Tabs
                    key={activeKey}
                    defaultActiveKey={activeKey}
                    items={agreements.documents.map((document) => ({
                        key: document.type,
                        label: document.title,
                        children: (
                            <div className="max-h-[60vh] overflow-y-auto pr-2">
                                <Typography.Paragraph className="whitespace-pre-wrap text-sm leading-6">{document.body}</Typography.Paragraph>
                            </div>
                        ),
                    }))}
                />
            ) : (
                <Typography.Paragraph type="secondary">协议加载中…</Typography.Paragraph>
            )}
        </Modal>
    );
}
