#!/usr/bin/env python3
"""把平台货架收敛到 KinoTV 当前售卖的模型。

这份清单是产品决策，不是默认值：平台只卖列在这里的图片与视频模型，其余一律下架。
脚本做成"可重放"的原因也在此——货架会变，而每次变更都必须能从仓库里重算一遍，
而不是靠谁记得当初在后台点了哪些开关。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/pin-model-catalog.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/pin-model-catalog.py --apply

Cookie 从浏览器开发者工具的任意 /api/admin/* 请求里复制（`Cookie:` 请求头整行）。
脚本不接收账号密码：这套系统只有验证码登录，把口令塞进命令行等于多一个泄露面。

只改 `enabled` 一个字段。模型对象是全量覆盖语义（PUT），因此其余字段——尤其是
`variants`（分档上游 SKU，例如 Seedance 2.5 的 720p/30s）与 `capabilityConfig`
（运营为某个模型调过的参数面板）——必须原样往返。漏传 variants 会把档位重置成
一条默认记录，档位是能卖的东西，丢了不会报错，只会让那个模型突然少了几档。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

# 货架：能力 → 允许上架的模型标识。不在这里的图片/视频模型一律下架。
# text 与 audio 刻意不在治理范围内：Agent 的脑子（DeepSeek）与将来的音频模型
# 走的是另一套取舍，把它们一起收进来会让这个脚本在执行时需要理解更多上下文。
CATALOG: dict[str, set[str]] = {
    "image": {"openai/gpt-image-2"},
    "video": {"seedance-2.0", "seedance-2.5"},
}
MANAGED_CAPABILITIES = tuple(CATALOG)


class ApiError(RuntimeError):
    pass


def request(method: str, base_url: str, path: str, cookie: str, payload: dict | None = None) -> dict:
    body = None if payload is None else json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(base_url.rstrip("/") + path, data=body, method=method)
    req.add_header("Cookie", cookie)
    req.add_header("Accept", "application/json")
    if body is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            envelope = json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as error:
        detail = error.read().decode("utf-8", errors="replace")[:300]
        raise ApiError(f"{method} {path} → HTTP {error.code}: {detail}") from error
    except urllib.error.URLError as error:
        raise ApiError(f"{method} {path} → 无法连接：{error.reason}") from error
    if envelope.get("code") != 0:
        raise ApiError(f"{method} {path} → {envelope.get('msg') or envelope}")
    return envelope.get("data") or {}


def put_body(model: dict, enabled: bool) -> dict:
    """构造全量 PUT 请求体：改 enabled，其余字段逐字回传。"""
    return {
        "modelKey": model.get("modelKey", ""),
        "providerModelKey": model.get("providerModelKey", ""),
        "displayName": model.get("displayName", ""),
        "icon": model.get("icon", ""),
        "capability": model.get("capability", ""),
        "protocol": model.get("protocol", ""),
        "enabled": enabled,
        "capabilityConfig": model.get("capabilityConfig"),
        # variants 必须回传：服务端在为空时会把档位重置成一条 "任意分辨率/任意时长"
        # 的默认记录，那会让 Seedance 的 720p/30s 这类档位凭空消失。
        "variants": [
            {
                "selector": variant.get("selector") or {},
                "resolution": variant.get("resolution", ""),
                "videoSeconds": variant.get("videoSeconds", 0),
                "providerModelKey": variant.get("providerModelKey", ""),
                "enabled": variant.get("enabled", True),
            }
            for variant in (model.get("variants") or [])
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="收敛平台模型货架（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    args = parser.parse_args()

    cookie = args.cookie.strip()
    if not cookie:
        print("缺少管理员会话：请设置 KINO_ADMIN_COOKIE（浏览器里任意 /api/admin/* 请求的 Cookie 头）", file=sys.stderr)
        return 2

    channels = request("GET", args.base_url, "/admin/channels", cookie).get("channels") or []
    seen = {capability: set() for capability in MANAGED_CAPABILITIES}
    changes: list[tuple[str, dict, bool]] = []

    for channel in channels:
        channel_id = channel.get("id") or ""
        if not channel_id:
            continue
        models = request("GET", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
        for model in models:
            capability = model.get("capability") or ""
            if capability not in CATALOG:
                continue
            model_key = model.get("modelKey") or ""
            wanted = model_key in CATALOG[capability]
            if wanted:
                seen[capability].add(model_key)
            if bool(model.get("enabled")) == wanted:
                continue
            changes.append((channel_id, model, wanted))

    exit_code = 0
    for capability, wanted in CATALOG.items():
        missing = sorted(wanted - seen[capability])
        if missing:
            # 白名单里的模型一个都没找到，说明货架与预期不符（改过标识、渠道被删、
            # 或导入没跑）。这不该静默：下架是幂等的，少一个能卖的模型才是事故。
            print(f"警告：{capability} 货架里应有的模型未出现在任何渠道：{', '.join(missing)}", file=sys.stderr)
            exit_code = 1

    if not changes:
        print("货架已经是目标状态，无需变更。")
        return exit_code

    action = "写入" if args.apply else "预览"
    print(f"共 {len(changes)} 项变更（{action}）：")
    for channel_id, model, wanted in changes:
        label = "上架" if wanted else "下架"
        print(f"  [{label}] {channel_id} · {model.get('capability')} · {model.get('modelKey')} ({model.get('displayName')})")
    if not args.apply:
        print("\n这是预览。确认无误后加 --apply 执行。")
        return exit_code

    for channel_id, model, wanted in changes:
        model_id = model.get("id") or ""
        request(
            "PUT",
            args.base_url,
            f"/admin/channels/{urllib.parse.quote(channel_id)}/models/{urllib.parse.quote(model_id)}",
            cookie,
            put_body(model, wanted),
        )
        print(f"  已{'上架' if wanted else '下架'} {model.get('modelKey')}")
    print(f"\n完成：{len(changes)} 项变更已生效。")
    return exit_code


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ApiError as error:
        print(f"失败：{error}", file=sys.stderr)
        sys.exit(1)
