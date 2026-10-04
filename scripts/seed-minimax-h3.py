#!/usr/bin/env python3
"""上架秘塔 MiniMax H3 视频模型，并按分辨率分档定价。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
    METASO_H3_API_KEY='<秘塔 H3 API Key>' \\
        python3 scripts/seed-minimax-h3.py

    KINO_ADMIN_COOKIE='...' METASO_H3_API_KEY='...' \\
    KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-minimax-h3.py --apply

密钥只从环境变量读，不落库到脚本、不写进仓库。

## 为什么走 MiniMax 原生口，不走 OpenAI 兼容口

秘塔同时开了两个口：`https://metaso.cn/api/openai`（OpenAI/Sora 兼容）和
`https://metaso.cn/api/minimax`（MiniMax 原生）。兼容口只支持「单图 + 提示词」、
时长只认 4/8/12；原生口支持 4–15 秒、首尾帧、多图、参考视频/音频与比例，而且
正好对上仓库里已有的 `minimax-video` 协议插件，前端和后端都不用改。

## 为什么要拆成两张模型卡

上游按分辨率计价（768P 0.09 元/秒、2K 0.15 元/秒），但计费侧的视频只有一档价
（见 auth/pricing_model.go：视频档位留空）。同一张卡填不出两个价，所以按分辨率拆成
两个渠道模型，每个模型的 `capabilityConfig.video.resolutions` 固定一档——服务端会用
`applyFixedVideoResolution` 把画质钉死在那一档，用户选不出别的分辨率，也就不会出现
「按 768P 的价跑了 2K 的活」。

## 毛利

| 档位 | 售价 | 上游成本 | 每秒 |
| --- | --- | --- | --- |
| 768P | ¥0.15/秒 | ¥0.09/秒 | +¥0.06 |
| 2K | ¥0.25/秒 | ¥0.15/秒 | +¥0.10 |

两档毛利率都是 40%。这是拉新价，不是成本加成价：同规格上游官方价约 0.45/0.75 元/秒，
秘塔已经打到 2 折，我们再让一层，换的是「愿意试」而不是「愿意付」。

## 时长与素材上限的出处

duration 4–15 是上游接口实测的硬边界（3 和 16 都返回「duration 必须为 4 到 15 的整数」）。
图片 5 张、参考视频关闭、音频 3 段：上限不是上游的能力上限，是按上游的素材计费倒推出来的
（见 MAX_IMAGES 上面的说明）。素材必须是公网可达 URL——插件声明了 requiresPublicMediaUrls，
宿主会把素材换成短时效签名地址，签名有效期 4 小时，够一条视频取完。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from decimal import Decimal

CHANNEL_NAME = "秘塔 MiniMax-H3"
CHANNEL_BASE_URL = "https://metaso.cn/api/minimax"

# 上游模型标识：两张卡打的是同一个上游模型，只是把画质钉在不同档。
UPSTREAM_MODEL = "MiniMax-H3"

# 上游实测并发约 5 条，超了直接 429（concurrent video task limit reached）。
# 平台侧留一格余量钉在 4，避免多个用户同时提交时把 429 暴露成「生成失败」。
CONCURRENCY_LIMIT = 4

# 上游素材计费（秘塔价目页 + 实测用量回执）：
#
#   · 图片：前 5 张免费，第 6 张起 0.05 元/张；
#   · 参考视频：按输入时长 × 输出档位的每秒单价计费。实测一条「输入 5 秒 + 输出 4 秒」
#     的任务，用量回执是 input_seconds=6 / output_seconds=4，上游按下 10 秒收钱；
#   · 音频：免费，用量里只记 input_audio_seconds，不计入 total_seconds。
#
# 这两笔超量成本都有对应的扣费口径，素材上限因此按上游放开：
#
#   · 参考视频输入秒数：按同档位每秒单价一起收，见
#     backend/internal/app/task_credit_reference_video.go；
#   · 图片第 6 张起：按 15 分/张加收（上游 5 分/张），见
#     backend/internal/app/task_credit_reference_image.go。
#
# 上限取 9 而不是更多：插件侧硬校验就是「图片最多 9 张」，再放开也会被上游打回。
MAX_IMAGES = 9
MAX_VIDEOS = 3
MAX_AUDIOS = 3

# 售价：分 / 秒。15 分 = ¥0.15/秒，25 分 = ¥0.25/秒。
MODELS: list[dict] = [
    {
        "modelKey": "MiniMax-H3",
        "displayName": "MiniMax H3 768P",
        "resolution": "768P",
        "sellFenPerSecond": 15,
        "upstreamPerSecond": "0.09",
    },
    {
        "modelKey": "MiniMax-H3-2K",
        "displayName": "MiniMax H3 2K",
        "resolution": "2K",
        "sellFenPerSecond": 25,
        "upstreamPerSecond": "0.15",
    },
]

CAPABILITY = "VIDEO"
# 必须是 SECOND：视频按用户选定的秒数相乘，秒数在提交时一定存在。
UNIT = "SECOND"
PROTOCOL = "minimax-video"
VENDOR_CODE = "minimax-video"

# 前台可选时长与画幅。与 minimax-video 插件的默认档一致，改口径改这里再重跑。
DURATIONS = list(range(4, 16))
RATIOS = ["adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16"]


def capability_config(resolution: str) -> dict:
    """把画质钉死在单一档位，其余沿用 minimax-video 的默认能力。"""
    return {
        "version": 1,
        "video": {
            "references": {
                "promptMaxChars": 8000,
                "minImages": 0,
                "maxImages": MAX_IMAGES,
                "maxImageBytes": 30 * 1024 * 1024,
                "maxVideos": MAX_VIDEOS,
                "maxVideoBytes": 50 * 1024 * 1024,
                "maxVideoDurationSeconds": 15,
                "maxAudios": MAX_AUDIOS,
                "maxAudioBytes": 15 * 1024 * 1024,
                "maxAudioDurationSeconds": 15,
            },
            "duration": {"selection": "enum", "values": DURATIONS, "default": 5},
            "ratios": RATIOS,
            "defaultRatio": "16:9",
            "resolutions": [resolution],
            "defaultResolution": resolution,
            "generateAudio": {"supported": False, "default": False},
            "watermark": {"supported": True, "default": False},
            "operations": ["text_to_video", "image_to_video", "reference_to_video"],
            "defaultOperation": "text_to_video",
        },
    }


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


def price_key(model_key: str, channel_id: str) -> str:
    """定价表里的模型标识带渠道前缀，与前台结算时用的 key 一致。"""
    return f"{channel_id}::{model_key}"


def note_for(model: dict) -> str:
    return (
        f"上游 秘塔 MiniMax-H3 {model['resolution']} ¥{model['upstreamPerSecond']}/秒；"
        f"按秒售价 {Decimal(model['sellFenPerSecond']) / 100} 元/秒"
    )


def print_economics() -> None:
    print(f"渠道「{CHANNEL_NAME}」{CHANNEL_BASE_URL}（协议 {PROTOCOL}，最大并发 {CONCURRENCY_LIMIT}）")
    for model in MODELS:
        margin = 1 - Decimal(model["upstreamPerSecond"]) / (Decimal(model["sellFenPerSecond"]) / 100)
        print(
            f"  {model['displayName']:<18} {model['resolution']:<5} "
            f"{Decimal(model['sellFenPerSecond']) / 100:>5.2f} 元/秒 "
            f"· 上游 {model['upstreamPerSecond']} 元/秒 · 毛利 {margin * 100:.0f}%"
        )
    print(
        f"  时长 {'/'.join(str(value) for value in DURATIONS)} 秒；画幅 {', '.join(RATIOS)}；"
        f"素材上限 图片 {MAX_IMAGES} / 视频 {MAX_VIDEOS} / 音频 {MAX_AUDIOS}"
    )


def model_payload(model: dict) -> dict:
    return {
        "modelKey": model["modelKey"],
        "providerModelKey": UPSTREAM_MODEL,
        "displayName": model["displayName"],
        "capability": CAPABILITY.lower(),
        "protocol": PROTOCOL,
        "enabled": True,
        "capabilityConfig": capability_config(model["resolution"]),
    }


def model_drift(existing: dict, payload: dict) -> bool:
    """能力配置也要比：只比"模型在不在"，改了图片上限这类参数重跑脚本会被当成无变更。"""
    for field in ("providerModelKey", "displayName", "capability", "protocol"):
        if str(existing.get(field) or "") != str(payload[field]):
            return True
    if existing.get("enabled") is not True:
        return True
    return existing.get("capabilityConfig") != payload["capabilityConfig"]


def find_channel(base_url: str, cookie: str) -> dict | None:
    page = request("GET", base_url, "/admin/channels?pageSize=200", cookie)
    for channel in page.get("channels") or []:
        if str(channel.get("name") or "").strip() == CHANNEL_NAME:
            return channel
    return None


def main() -> int:
    parser = argparse.ArgumentParser(description="上架秘塔 MiniMax H3 并按分辨率分档定价（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    args = parser.parse_args()

    cookie = args.cookie.strip()
    if not cookie:
        print("缺少管理员会话：请设置 KINO_ADMIN_COOKIE（浏览器里任意 /api/admin/* 请求的 Cookie 头）", file=sys.stderr)
        return 2

    print_economics()
    print()

    api_key = os.environ.get("METASO_H3_API_KEY", "").strip()
    channel = find_channel(args.base_url, cookie)
    if channel is None and not api_key:
        print("渠道不存在，创建它需要 METASO_H3_API_KEY。", file=sys.stderr)
        return 2

    plan: list[str] = []
    if channel is None:
        plan.append(f"创建渠道「{CHANNEL_NAME}」→ {CHANNEL_BASE_URL}")
    elif str(channel.get("baseUrl") or "").rstrip("/") != CHANNEL_BASE_URL:
        plan.append(f"渠道 Base URL {channel.get('baseUrl')} → {CHANNEL_BASE_URL}")
    if channel is not None and int(channel.get("concurrencyLimit") or 0) != CONCURRENCY_LIMIT:
        plan.append(f"渠道最大并发 {channel.get('concurrencyLimit')} → {CONCURRENCY_LIMIT}")

    existing_models: dict[str, dict] = {}
    channel_id = str((channel or {}).get("id") or "")
    if channel_id:
        rows = request("GET", args.base_url, f"/admin/channels/{channel_id}/models", cookie).get("models") or []
        existing_models = {str(row.get("modelKey") or ""): row for row in rows}

    for model in MODELS:
        payload = model_payload(model)
        existing = existing_models.get(model["modelKey"])
        if existing is None:
            plan.append(f"上架渠道模型 {model['displayName']}（{model['resolution']} / {CAPABILITY}）")
        elif model_drift(existing, payload):
            plan.append(f"更新渠道模型 {model['displayName']}（能力配置或上游标识有变化）")

    existing_prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {
        (row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row
        for row in existing_prices
    }
    for model in MODELS:
        key = (f"{channel_id}::{model['modelKey']}" if channel_id else "", CAPABILITY, "")
        existing = price_index.get(key) if channel_id else None
        label = model["modelKey"]
        if existing is None:
            plan.append(f"新增价目 {label}：{model['sellFenPerSecond']} 分/秒")
        elif (
            existing.get("unit") != UNIT
            or existing.get("sellUnitPrice") != model["sellFenPerSecond"]
            or existing.get("enabled") is not True
        ):
            plan.append(
                f"更新价目 {label}：单位 {existing.get('unit')} → {UNIT}，"
                f"售价 {existing.get('sellUnitPrice')} → {model['sellFenPerSecond']} 分/秒"
            )

    if not plan:
        print("渠道、模型与价目已经是目标状态，无需变更。")
        return 0

    for line in plan:
        print(" ·", line)

    if not args.apply:
        print(f"\n预览结束：{len(plan)} 项待写入。加 --apply 才会真正写库。")
        return 0

    if channel is None:
        channel = request(
            "POST",
            args.base_url,
            "/admin/channels",
            cookie,
            {"name": CHANNEL_NAME, "baseUrl": CHANNEL_BASE_URL, "apiKey": api_key, "models": [], "concurrencyLimit": CONCURRENCY_LIMIT},
        )
    else:
        channel_id = str(channel.get("id") or "")
        if (
            str(channel.get("baseUrl") or "").rstrip("/") != CHANNEL_BASE_URL
            or int(channel.get("concurrencyLimit") or 0) != CONCURRENCY_LIMIT
        ):
            request(
                "PUT",
                args.base_url,
                f"/admin/channels/{channel_id}",
                cookie,
                {
                    "name": CHANNEL_NAME,
                    "baseUrl": CHANNEL_BASE_URL,
                    "apiKey": api_key,
                    "models": [str(item.get("modelKey") or "") for item in (channel.get("models") or []) if isinstance(item, dict)]
                    or [str(item) for item in (channel.get("models") or []) if isinstance(item, str)],
                    "concurrencyLimit": CONCURRENCY_LIMIT,
                },
            )

    channel_id = str(channel.get("id") or channel_id)
    if not channel_id:
        print("渠道 ID 缺失，无法继续。", file=sys.stderr)
        return 1

    models = request("GET", args.base_url, f"/admin/channels/{channel_id}/models", cookie).get("models") or []
    by_key = {str(row.get("modelKey") or ""): row for row in models}
    for model in MODELS:
        payload = model_payload(model)
        existing = by_key.get(model["modelKey"])
        if existing is None:
            request("POST", args.base_url, f"/admin/channels/{channel_id}/models", cookie, payload)
        elif model_drift(existing, payload):
            request(
                "PUT",
                args.base_url,
                f"/admin/channels/{channel_id}/models/{urllib.parse.quote(str(existing.get('id') or model['modelKey']))}",
                cookie,
                payload,
            )

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {
        (row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row
        for row in prices
    }
    for model in MODELS:
        row = {
            "modelKey": price_key(model["modelKey"], channel_id),
            "capability": CAPABILITY,
            "priceTier": "",
            "unit": UNIT,
            "vendorCode": VENDOR_CODE,
            "upstreamUnitPrice": None,
            "sellUnitPrice": model["sellFenPerSecond"],
            "multiplier": None,
            "enabled": True,
            "note": note_for(model),
        }
        existing = price_index.get((row["modelKey"], CAPABILITY, ""))
        if existing is None:
            request("POST", args.base_url, "/admin/billing/model-prices", cookie, row)
        else:
            request(
                "PUT",
                args.base_url,
                "/admin/billing/model-prices/" + urllib.parse.quote(str(existing.get("id"))),
                cookie,
                row,
            )

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
