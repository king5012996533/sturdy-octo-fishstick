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

还有第三种缺口：渠道只建在库里、平台配置的 `channels` 里根本没有这一条。这时第 2 处
连"改"的机会都没有，模型同样进不了前台。脚本因此会先把缺失的系统渠道补进配置，
再收敛模型清单——新建渠道后的第一次 pin 就能把这条路走完。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/pin-model-catalog.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/pin-model-catalog.py --apply

Cookie 从浏览器开发者工具的任意 /api/admin/* 请求里复制（`Cookie:` 请求头整行）。
脚本不接收账号密码：这套系统只有验证码登录，把口令塞进命令行等于多一个泄露面。

在售模型不会被静默下架：名单里没有、但渠道已启用且已定价的模型会让脚本直接拒绝执行，
要真下架得显式加 `--allow-unlisted-disable`（见 `unlisted_sellable`）。

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
#
# 标识必须按渠道写实：同一个模型在不同渠道的上游标识可能不同，写错了不会被发现，
# 只会让脚本把在卖的模型当成"超纲"下架。当前有两组这样的成对标识：
#   - fal 主账号（CHANNEL_000012）用上游原名，带斜杠：openai/gpt-image-2.5/sunburst；
#   - Replicate 备用（CHANNEL_000003）用连字符：openai/gpt-image-2.5-sunburst。
# 两组都在卖（fal 优先、Replicate 兜底），因此两组都得在名单里。
CATALOG: dict[str, set[str]] = {
    # OpenAI 图片族走同一份质量标准（见 seed-image-model-prices.py）：2.5 系比 2.0 多出
    # 上游 xhigh/max 两档，档位枚举、能力合同与价目都已按五档对齐；2.0 仍只有三档。
    "image": {
        "openai/gpt-image-2",
        # fal 主账号（带斜杠的上游标识）
        "openai/gpt-image-2.5/sunburst",
        "openai/gpt-image-2.5/flare",
        # Replicate 备用（连字符标识，与上面两条是同一个逻辑模型的两条通路）
        "openai/gpt-image-2.5-sunburst",
        "openai/gpt-image-2.5-flare",
        # 已退役、不要再加回来：google/imagen-4 与 imagen-4-fast（Replicate）。这两条
        # 是早期接入的备选，跑下来不如 fal 的两个 2.5 系，2026-10 起不再对外出售。
        # 火山方舟 Seedream 5.0：2026-10 起只卖 pro / flash 两个型号，Lite 已下架。
        "doubao-seedream-5-0-pro-260628",
        "doubao-seedream-5-0-flash-260915",
        # 腾讯 TokenHub 混元生图 3.5：中文人物质感到位，补齐国内直连的一条生图线路。
        "hy-image-v3.5-preview",
        # AI 超分（fal Recraft Crisp）：能力上是图片，但它是画布工具的固定端点，
        # 不是给用户在下拉里挑的模型（capabilityConfig.toolOnly=true）。仍必须列在这里：
        # 它要按张计价，而货架是"能不能卖"的唯一开关，漏了就等于按钮点不动。
        "fal-ai/recraft/upscale/crisp",
    },
    "video": {
        # 已退役、不要再加回来：Aigen 的 seedance-2.0 / seedance-2.5（CHANNEL_000007）。
        # 2026-10 起不再对外出售，渠道模型保持停用。
        # 秘塔 MiniMax-H3：同一模型的两个分辨率档各占一条货架记录，
        # 漏一个就会被这个脚本当成"超纲"下架，前台少一档。
        "MiniMax-H3",
        "MiniMax-H3-2K",
        # 纵横科技 TTP-grok：按分辨率分档定价，两档价挂在同一条货架记录下
        # （价目表的视频档位就是分辨率本身），因此这里只有一条。
        "grok-imagine-video/v1.5",
    },
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


UNIT_LABELS = {"TOKEN_1M": "百万 token", "TOKEN_1K": "千 token", "IMAGE": "张", "SECOND": "秒", "REQUEST": "次"}


def describe_price(rows: list[dict]) -> str:
    """给一条在售模型配一句人话，说明它为什么不该被这个脚本顺手关掉。"""
    unit = UNIT_LABELS.get(str(rows[0].get("unit") or ""), "次")
    sells = [row["sellUnitPrice"] for row in rows if isinstance(row.get("sellUnitPrice"), int)]
    upstreams = [row["upstreamUnitPrice"] for row in rows if isinstance(row.get("upstreamUnitPrice"), int)]
    if sells:
        return f"在售，售价最高 {max(sells)} 分/{unit}"
    if upstreams:
        return f"已定价（上游最高 {max(upstreams)} 分/{unit}，售价按倍率解析）"
    return "已配置价目"


def unlisted_sellable(changes: list[tuple[str, dict, bool]], prices: list[dict]) -> list[tuple[str, dict, str]]:
    """即将下架、但正在卖的模型。

    判据是"渠道里已启用 + 有启用的价目"：两者同时成立说明它正在产生收入，而这个脚本
    判断它在不在货架上靠的是 CATALOG 那份手写名单——名单一旦落后于实际货架，下架就是
    静默的：不报错、前台却不卖了。这条路径上已经发生过一次（fal 渠道用的是带斜杠的
    上游标识，名单里只有 Replicate 的连字符写法），所以改成硬拦截，不再靠人眼。
    """
    priced: dict[str, list[dict]] = {}
    for price in prices:
        model_key = str(price.get("modelKey") or "")
        if model_key and price.get("enabled") is not False:
            priced.setdefault(model_key, []).append(price)
    hits: list[tuple[str, dict, str]] = []
    for channel_id, model, wanted in changes:
        if wanted:
            continue
        rows = priced.get(f"{channel_id}::{model.get('modelKey') or ''}")
        if rows:
            hits.append((channel_id, model, describe_price(rows)))
    return hits


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


def is_tool_only(profile: dict) -> bool:
    """这份画像是不是画布工具专用的固定端点（不让用户在下拉里选）。

    与前台同一判据（`use-config-store.ts` 的 `filterModelsByCapability`）：能力清单是
    "用户能挑的模型"快照，工具专用模型进去只会让人选中一次必然失败的生成。
    """
    capability_config = profile.get("capabilityConfig") or {}
    if not isinstance(capability_config, dict):
        return False
    image = capability_config.get("image") or {}
    return bool(isinstance(image, dict) and image.get("toolOnly"))


def managed_profile(model: dict) -> dict:
    """把渠道模型整理成平台配置里 modelProfiles 的形状。

    平台配置只认 `model` 这个键名（渠道模型接口里叫 `modelKey`），而且只保留这几项：
    多写的字段——`id`、`variants`、时间戳——会跟着配置一起被下发到前台。字段名写错
    不会报错，只会让前台按"没有画像"回落到默认能力合同，等于模型悄悄降级。
    """
    capability_config = model.get("capabilityConfig")
    profile = {
        "model": model.get("modelKey", ""),
        "displayName": model.get("displayName", ""),
        "icon": model.get("icon", ""),
        "capability": model.get("capability", ""),
        "protocol": model.get("protocol", ""),
    }
    # 没有能力配置时不要写出这个空键。前台与后台写入画像时都不带它，只有这里带上，
    # 结果是每次预览都报一条"同步 N 条模型画像"的假变更——真正该看见的差异被淹在里面。
    if capability_config is not None:
        profile["capabilityConfig"] = capability_config
    return profile


def register_missing_channels(config: dict, shelf: dict[str, list[dict]], meta: dict[str, dict]) -> list[str]:
    """把后台已有、平台配置里却缺的系统渠道补一条。

    渠道只在库里建好是不够的：前台读的是平台配置里的 `channels`，少一条就等于
    "后台看着已上架、定价也能查到，创作端却选不到这个模型"。这条缺陷不报错，
    只会让运营以为模型没生效，所以由这个脚本补齐，而不是靠谁记得手工同步。
    """
    channels = config.get("channels")
    if not isinstance(channels, list):
        return []
    known = {str(item.get("id") or "") for item in channels if isinstance(item, dict)}
    added: list[str] = []
    for channel_id, profiles in shelf.items():
        if not channel_id or channel_id in known or not profiles:
            continue
        channel_meta = meta.get(channel_id) or {}
        channels.append(
            {
                "id": channel_id,
                "name": str(channel_meta.get("name") or channel_id),
                "scope": "system",
                "enabled": True,
                "pinned": False,
                "sortOrder": int(channel_meta.get("sortOrder") or 0),
                "apiFormat": str(channel_meta.get("apiFormat") or "openai"),
                # 系统渠道在浏览器侧只持有 /api/ai/system/<id> 这条相对地址：
                # 上游地址与平台密钥都留在服务端，前端拿不到也改不了。
                "baseUrl": f"/api/ai/system/{channel_id}",
                "apiKey": "system",
                "secretKey": "",
                "headers": [],
                "hasApiKey": True,
                "models": [profile["model"] for profile in profiles],
                "modelProfiles": profiles,
            }
        )
        added.append(f"{channel_id} 补进平台模型配置（{len(profiles)} 个模型）")
    return added


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
    tool_only_entries: set[str] = set()
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
            qualified_name = f"{channel_id}::{item.get('model', '')}"
            capability_by_model[qualified_name] = str(item.get("capability") or "")
            if is_tool_only(item):
                tool_only_entries.add(qualified_name)

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
            and f"{channel.get('id')}::{model}" not in tool_only_entries
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
    parser.add_argument(
        "--allow-unlisted-disable",
        action="store_true",
        help="放行「在售模型不在白名单时下架」的情况（默认拒绝，见 unlisted_sellable）",
    )
    args = parser.parse_args()

    cookie = args.cookie.strip()
    if not cookie:
        print("缺少管理员会话：请设置 KINO_ADMIN_COOKIE（浏览器里任意 /api/admin/* 请求的 Cookie 头）", file=sys.stderr)
        return 2

    channels = request("GET", args.base_url, "/admin/channels?pageSize=200", cookie).get("channels") or []
    seen = {capability: set() for capability in MANAGED_CAPABILITIES}
    changes: list[tuple[str, dict, bool]] = []
    # 渠道 id → 目标货架模型（按后台顺序）。未启用的模型不进这份清单，对应"下架"。
    shelf: dict[str, list[dict]] = {}
    # 建渠道时写得进库、写不进平台配置的字段就靠这里取，补配置时要用。
    channel_meta: dict[str, dict] = {}

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
        channel_meta[channel_id] = {
            "name": channel.get("name") or "",
            "apiFormat": channel.get("apiFormat") or "",
            "sortOrder": channel.get("sortOrder") or 0,
        }

    unlisted: list[tuple[str, dict, str]] = []
    if any(not wanted for _, _, wanted in changes):
        prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
        unlisted = unlisted_sellable(changes, prices)

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
        config = json.loads(json.dumps(config))
        shelf_changes.extend(register_missing_channels(config, shelf, channel_meta))
        config, converged = shelf_plan(config, shelf)
        shelf_changes.extend(converged)
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
    unlisted_reasons = {(channel_id, model.get("modelKey")): note for channel_id, model, note in unlisted}
    for channel_id, model, wanted in changes or []:
        note = unlisted_reasons.get((channel_id, model.get("modelKey")))
        suffix = f" — ⚠ {note}，不在白名单里" if note else ""
        print(f"  [{'上架' if wanted else '下架'}] {channel_id} · {model.get('capability')} · {model.get('modelKey')} ({model.get('displayName')}){suffix}")
    if not changes:
        print("  无需变更")

    if unlisted and not args.allow_unlisted_disable:
        print("\n拒绝执行：下面这些模型正在卖，却不在 CATALOG 白名单里。", file=sys.stderr)
        for channel_id, model, note in unlisted:
            print(f"  {channel_id} · {model.get('modelKey')}（{note}）", file=sys.stderr)
        print(
            "这条路径只会出现在白名单落后于实际货架时——下架是静默的，前台少卖一个模型不会报错。\n"
            "要么把它补进 CATALOG（通常是要卖），要么先在后台下架它，"
            "确认这次就是来下架的再加 --allow-unlisted-disable。",
            file=sys.stderr,
        )
        return 1

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
