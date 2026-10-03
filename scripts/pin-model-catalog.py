#!/usr/bin/env python3
"""把平台货架收敛到 KinoTV 当前售卖的模型。

这份清单是产品决策，不是默认值：平台只卖列在这里的图片与视频模型，其余一律下架。
脚本做成"可重放"的原因也在此——货架会变，而每次变更都必须能从仓库里重算一遍，
而不是靠谁记得当初在后台点了哪些开关。

货架由**两处**共同决定，缺一处模型都不会出现在前台：

1. 渠道模型的 `enabled` 开关（`/admin/channels/{id}/models`）；
2. 平台模型配置里各渠道的模型清单（`/workspace/model-config` 的 `channels[].models`）。

第 2 处是前台真正读的那份：`modelProfiles`、`imageModels`/`videoModels` 都由它派生。
只改第 1 处会出现"后台看着已上架、前台一个都看不到"的假象——这也是这个脚本必须
一起收敛两处的原因。

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
    # OpenAI 图片族走同一份质量标准（见 seed-image-model-prices.py）：2.5 系比 2.0 多出
    # 上游 xhigh/max 两档，档位枚举、能力合同与价目都已按五档对齐；2.0 仍只有三档。
    "image": {
        "openai/gpt-image-2",
        "openai/gpt-image-2.5-sunburst",
        "openai/gpt-image-2.5-flare",
        "google/imagen-4",
        "google/imagen-4-fast",
    },
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


def reconcile_list(current: list[str], target: list[str]) -> list[str]:
    """保留顺序的对齐：先留两边都有的（按当前顺序），再按目标顺序补上新增的。"""
    target_set = set(target)
    kept = [item for item in current if item in target_set]
    return kept + [item for item in target if item not in set(kept)]


def reconcile_shelf_list(current: list[str], target: list[str], managed_channels: set[str]) -> list[str]:
    """能力清单的对齐，但只对受治理的渠道执行"下架"。

    清单里可能有别处的条目（自带渠道、用户自己的渠道）。那些渠道不在这个脚本的
    治理范围内：它们不该被静默删掉，也不该因为读不到能力画像就被漏掉。
    """
    target_set = set(target)
    kept: list[str] = []
    for item in current:
        if item in target_set:
            kept.append(item)
            continue
        owner = item.split("::", 1)[0] if "::" in item else ""
        if owner in managed_channels:
            continue
        kept.append(item)
    return kept + [item for item in target if item not in set(kept)]


def managed_profile(model: dict) -> dict:
    """把渠道模型整理成平台配置里 modelProfiles 的形状。

    平台配置只认 `model` 这个键名（渠道模型接口里叫 `modelKey`），而且只保留这几项：
    多写的字段——`id`、`variants`、时间戳——会跟着配置一起被下发到前台。字段名写错
    不会报错，只会让前台按"没有画像"回落到默认能力合同，等于模型悄悄降级。
    """
    return {
        "model": model.get("modelKey", ""),
        "displayName": model.get("displayName", ""),
        "icon": model.get("icon", ""),
        "capability": model.get("capability", ""),
        "protocol": model.get("protocol", ""),
        "capabilityConfig": model.get("capabilityConfig"),
    }


def shelf_plan(config: dict, shelf: dict[str, list[dict]]) -> tuple[dict, list[str]]:
    """算出平台模型配置的目标状态。

    shelf 是"渠道 id → 该渠道货架上的模型（已按目标启用状态过滤、保持后台顺序）"。
    返回 (新配置, 变更说明)。只动渠道的 models/modelProfiles 与四个能力清单，
    其余键（选中默认模型、提示词、agent 设置等）原样保留。
    """
    changed: list[str] = []
    channels = config.get("channels")
    if not isinstance(channels, list):
        return config, changed
    capability_by_model: dict[str, str] = {}
    managed_channels = set(shelf)
    for channel in channels:
        if not isinstance(channel, dict):
            continue
        channel_id = str(channel.get("id") or "")
        wanted = shelf.get(channel_id)
        if wanted is None:
            # 不在系统渠道清单里（自带 beefapi、用户渠道）：不归这个脚本管。
            continue
        wanted_keys = [item.get("model", "") for item in wanted]
        current = [str(item) for item in (channel.get("models") or [])]
        next_models = reconcile_list(current, wanted_keys)
        if next_models != current:
            added = [item for item in next_models if item not in set(current)]
            removed = [item for item in current if item not in set(next_models)]
            if added:
                changed.append(f"{channel_id} 上架 {', '.join(added)}")
            if removed:
                changed.append(f"{channel_id} 下架 {', '.join(removed)}")
            channel["models"] = next_models
        # modelProfiles 跟着 models 走：它就是这份清单的能力画像，少一条等于模型没有面板。
        wanted_by_key = {item.get("model", ""): item for item in wanted}
        current_profiles = [item for item in (channel.get("modelProfiles") or []) if isinstance(item, dict)]
        current_by_key = {str(item.get("model") or ""): item for item in current_profiles}
        next_profiles = [
            current_by_key.get(key) if current_by_key.get(key) == wanted_by_key.get(key) else wanted_by_key[key]
            for key in next_models
            if key in wanted_by_key
        ]
        if next_profiles != current_profiles:
            channel["modelProfiles"] = next_profiles
            changed.append(f"{channel_id} 同步 {len(next_profiles)} 条模型画像")
        for item in wanted:
            capability_by_model[f"{channel_id}::{item.get('model', '')}"] = str(item.get("capability") or "")

    qualified: dict[str, list[str]] = {}
    for capability in ("image", "video", "text", "audio"):
        key = f"{capability}Models"
        if key not in config:
            continue
        qualified[key] = [
            f"{channel.get('id')}::{model}"
            for channel in channels
            if isinstance(channel, dict)
            for model in (channel.get("models") or [])
            if capability_by_model.get(f"{channel.get('id')}::{model}") == capability
        ]
    qualified["models"] = [
        f"{channel.get('id')}::{model}"
        for channel in channels
        if isinstance(channel, dict)
        for model in (channel.get("models") or [])
    ]
    for key, target in qualified.items():
        if key not in config:
            continue
        current = [str(item) for item in (config.get(key) or [])]
        next_values = reconcile_shelf_list(current, target, managed_channels)
        if next_values != current:
            changed.append(f"{key} 清单变更（{len(current)} → {len(next_values)} 项）")
            config[key] = next_values
    return config, changed


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
    # 渠道 id → 目标货架模型（按后台顺序）。未启用的模型不进这份清单，对应"下架"。
    shelf: dict[str, list[dict]] = {}

    for channel in channels:
        channel_id = channel.get("id") or ""
        if not channel_id:
            continue
        models = request("GET", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
        on_shelf: list[dict] = []
        for model in models:
            capability = model.get("capability") or ""
            model_key = model.get("modelKey") or ""
            if capability not in CATALOG:
                # 不在治理范围内的能力（text/audio）保持原样进清单。
                if model.get("enabled"):
                    on_shelf.append(managed_profile(model))
                continue
            wanted = model_key in CATALOG[capability]
            if wanted:
                seen[capability].add(model_key)
            if bool(model.get("enabled")) != wanted:
                changes.append((channel_id, model, wanted))
            if wanted:
                on_shelf.append(managed_profile(model))
        shelf[channel_id] = on_shelf

    exit_code = 0
    for capability, wanted in CATALOG.items():
        missing = sorted(wanted - seen[capability])
        if missing:
            # 白名单里的模型一个都没找到，说明货架与预期不符（改过标识、渠道被删、
            # 或导入没跑）。这不该静默：下架是幂等的，少一个能卖的模型才是事故。
            print(f"警告：{capability} 货架里应有的模型未出现在任何渠道：{', '.join(missing)}", file=sys.stderr)
            exit_code = 1

    # 平台模型配置的渠道清单：把上面算出的目标货架同步进去，前台才看得到。
    payload = request("GET", args.base_url, "/workspace/model-config", cookie)
    config = payload.get("config")
    revision = payload.get("revision")
    shelf_changes: list[str] = []
    if isinstance(config, dict):
        config, shelf_changes = shelf_plan(json.loads(json.dumps(config)), shelf)
    else:
        print("警告：读不到平台模型配置，本次只改渠道开关，前台清单不会变化。", file=sys.stderr)
        exit_code = 1

    if not changes and not shelf_changes:
        print("货架已经是目标状态，无需变更。")
        return exit_code

    action = "写入" if args.apply else "预览"
    print(f"平台模型配置（{action}）：")
    for line in shelf_changes or ["无需变更"]:
        print(f"  {line}")
    print(f"\n渠道开关（{action}）：")
    for channel_id, model, wanted in changes or []:
        print(f"  [{'上架' if wanted else '下架'}] {channel_id} · {model.get('capability')} · {model.get('modelKey')} ({model.get('displayName')})")
    if not changes:
        print("  无需变更")

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

    if shelf_changes:
        body = {"config": config}
        if revision is not None:
            # 平台配置是进程级单例，带上 revision 才能挡住并发覆盖；没有 revision
            # 说明服务端不支持版本控制，那就退回整份覆盖，并在输出里说清楚。
            body["expectedRevision"] = revision
        request("PUT", args.base_url, "/workspace/model-config", cookie, body)
        print(f"  已同步平台模型配置（revision {revision}）")

    print(f"\n完成：{len(changes)} 项渠道开关、{len(shelf_changes)} 项清单变更已生效。")
    return exit_code


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ApiError as error:
        print(f"失败：{error}", file=sys.stderr)
        sys.exit(1)
