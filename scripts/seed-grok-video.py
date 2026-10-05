#!/usr/bin/env python3
"""上架纵横科技 TTP-grok 视频模型（对外 grok-imagine-video/v1.5），按分辨率分档定价。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
    ZONGHENG_API_KEY='<纵横科技 API Key>' \\
        python3 scripts/seed-grok-video.py

    KINO_ADMIN_COOKIE='...' ZONGHENG_API_KEY='...' \\
    KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-grok-video.py --apply

密钥只从环境变量读，不落库到脚本、不写进仓库。

## 为什么是"一条模型 + 两行价"

上游按分辨率分别定价（480p 与 720p 是两个价），价目表的视频档位就是分辨率本身
（见 auth/pricing_model.go 的 IsVideoResolutionPriceTier），因此两档价挂在同一个模型
下面，用户在面板上选哪一档就扣哪一档的钱——不必像 H3 那样拆成两张模型卡。

## 为什么单位是 REQUEST

上游按条结算：一条一个价，与时长无关（6 秒和 15 秒成本一样）。价目单位选 REQUEST
之后用量恒为 1；沿用默认的 SECOND 会按用户选的秒数相乘，一条 180 分的视频被算成
180 × 15 分。

## 毛利

上游 ¥0.80/条，与分辨率、时长都无关：

| 档位 | 售价 | 上游成本 | 毛利 |
| --- | --- | --- | --- |
| 480P | ¥1.50 | ¥0.80 | 47% |
| 720P | ¥1.80 | ¥0.80 | 56% |
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

CHANNEL_NAME = "纵横科技 · 主账号"
CHANNEL_BASE_URL = "https://cnd-coo-new.pages.dev"

# 上游模型标识：平台对外只暴露下面的 modelKey，真实标识留在渠道配置里。
UPSTREAM_MODEL = "TTP-grok"

# 上游并发未知（pages.dev 网关背后是聚合池），先按其他视频渠道的口径钉 4。
CONCURRENCY_LIMIT = 4

CAPABILITY = "VIDEO"
# 必须是 REQUEST：上游按条结算，用量恒为 1。
UNIT = "REQUEST"
PROTOCOL = "zongheng-video"
VENDOR_CODE = "zongheng-video"

MODEL_KEY = "grok-imagine-video/v1.5"
DISPLAY_NAME = "grok-imagine-video/v1.5"

# 规格来自上游公开能力中枢 GET /api/models：时长 6–15 秒、清晰度只有 480p/720p、
# 参考图上限 7 张、参考视频与参考音频为 0。照抄平台默认的 9 张图与 1440p 档位会被
# 上游直接拒收，用户只会拿到一次失败任务。
DURATIONS = list(range(6, 16))
RATIOS = ["16:9", "9:16", "1:1"]
MAX_IMAGES = 7

PRICE_TIERS: list[dict] = [
    {"tier": "480P", "sellFenPerItem": 150, "label": "480p"},
    {"tier": "720P", "sellFenPerItem": 180, "label": "720p"},
]

# 上游单条成本（分）：8 积分/条 ≈ ¥0.80，与分辨率、时长都无关，只用于算毛利与备注。
UPSTREAM_FEN_PER_ITEM = 80


def capability_config() -> dict:
    """能力合同：分辨率两档可选，参考视频与参考音频一律关掉。"""
    return {
        "version": 1,
        "video": {
            "references": {
                "promptMaxChars": 8000,
                "minImages": 0,
                "maxImages": MAX_IMAGES,
                "maxImageBytes": 30 * 1024 * 1024,
                # 上游 max_video_refs / max_audio_refs 都是 0：填 0 让面板根本不出现这两个
                # 入口，而不是等着插件侧把请求拦下来——用户看不到的开关才是不会误用的开关。
                "maxVideos": 0,
                "maxVideoBytes": 0,
                "maxVideoDurationSeconds": 0,
                "maxAudios": 0,
                "maxAudioBytes": 0,
                "maxAudioDurationSeconds": 0,
            },
            "duration": {"selection": "range", "min": 6, "max": 15, "step": 1, "default": 6},
            "ratios": RATIOS,
            "defaultRatio": "16:9",
            "resolutions": ["480p", "720p"],
            "defaultResolution": "720p",
            "generateAudio": {"supported": False, "default": False},
            "watermark": {"supported": False, "default": False},
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


def price_key(channel_id: str) -> str:
    """定价表里的模型标识带渠道前缀，与前台结算时用的 key 一致。"""
    return f"{channel_id}::{MODEL_KEY}"


def note_for(row: dict) -> str:
    margin = 1 - Decimal(UPSTREAM_FEN_PER_ITEM) / Decimal(row["sellFenPerItem"])
    return (
        f"上游 纵横科技 {UPSTREAM_MODEL} 按条结算 ¥{UPSTREAM_FEN_PER_ITEM / 100:.2f}/条（与分辨率、时长无关）；"
        f"{row['label']} 档一条 {row['sellFenPerItem']} 分（¥{row['sellFenPerItem'] / 100:.2f}），毛利 {margin * 100:.0f}%"
    )


def print_economics() -> None:
    print(f"渠道「{CHANNEL_NAME}」{CHANNEL_BASE_URL}（协议 {PROTOCOL}，最大并发 {CONCURRENCY_LIMIT}）")
    print(f"  模型 {DISPLAY_NAME}（上游 {UPSTREAM_MODEL}）· 时长 {DURATIONS[0]}–{DURATIONS[-1]} 秒 · 画幅 {', '.join(RATIOS)} · 参考图 ≤{MAX_IMAGES} 张")
    for row in PRICE_TIERS:
        margin = 1 - Decimal(UPSTREAM_FEN_PER_ITEM) / Decimal(row["sellFenPerItem"])
        print(
            f"  {row['tier']:<5} {row['sellFenPerItem']:>4} 分/条 "
            f"（¥{row['sellFenPerItem'] / 100:.2f}/条，毛利 {margin * 100:.0f}%）"
        )


def model_payload() -> dict:
    return {
        "modelKey": MODEL_KEY,
        "providerModelKey": UPSTREAM_MODEL,
        "displayName": DISPLAY_NAME,
        "capability": CAPABILITY.lower(),
        "protocol": PROTOCOL,
        "enabled": True,
        "capabilityConfig": capability_config(),
    }


def model_drift(existing: dict, payload: dict) -> bool:
    """能力配置也要比：只比"模型在不在"，改了参考图上限这类参数重跑脚本会被当成无变更。"""
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
    parser = argparse.ArgumentParser(description="上架纵横科技 TTP-grok 视频模型并按分辨率分档定价（默认只预览）")
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

    api_key = os.environ.get("ZONGHENG_API_KEY", "").strip()
    channel = find_channel(args.base_url, cookie)
    if channel is None and not api_key:
        print("渠道不存在，创建它需要 ZONGHENG_API_KEY。", file=sys.stderr)
        return 2

    plan: list[str] = []
    if channel is None:
        plan.append(f"创建渠道「{CHANNEL_NAME}」→ {CHANNEL_BASE_URL}")
    elif str(channel.get("baseUrl") or "").rstrip("/") != CHANNEL_BASE_URL:
        plan.append(f"渠道 Base URL {channel.get('baseUrl')} → {CHANNEL_BASE_URL}")
    if channel is not None and int(channel.get("concurrencyLimit") or 0) != CONCURRENCY_LIMIT:
        plan.append(f"渠道最大并发 {channel.get('concurrencyLimit')} → {CONCURRENCY_LIMIT}")

    channel_id = str((channel or {}).get("id") or "")
    existing_model: dict | None = None
    if channel_id:
        rows = request("GET", args.base_url, f"/admin/channels/{channel_id}/models", cookie).get("models") or []
        existing_model = next((row for row in rows if str(row.get("modelKey") or "") == MODEL_KEY), None)
    payload = model_payload()
    if existing_model is None:
        plan.append(f"上架渠道模型 {DISPLAY_NAME}（{UPSTREAM_MODEL} / {CAPABILITY}）")
    elif model_drift(existing_model, payload):
        plan.append(f"更新渠道模型 {DISPLAY_NAME}（能力配置或上游标识有变化）")

    existing_prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {
        (row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row
        for row in existing_prices
    }
    for row in PRICE_TIERS:
        key = (price_key(channel_id) if channel_id else "", CAPABILITY, row["tier"])
        existing = price_index.get(key) if channel_id else None
        if existing is None:
            plan.append(f"新增价目 {MODEL_KEY} {row['tier']}：{row['sellFenPerItem']} 分/条")
        elif (
            existing.get("unit") != UNIT
            or existing.get("sellUnitPrice") != row["sellFenPerItem"]
            or existing.get("enabled") is not True
        ):
            plan.append(
                f"更新价目 {MODEL_KEY} {row['tier']}：单位 {existing.get('unit')} → {UNIT}，"
                f"售价 {existing.get('sellUnitPrice')} → {row['sellFenPerItem']} 分/条"
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
        if str(channel.get("baseUrl") or "").rstrip("/") != CHANNEL_BASE_URL or int(channel.get("concurrencyLimit") or 0) != CONCURRENCY_LIMIT:
            request(
                "PUT",
                args.base_url,
                f"/admin/channels/{channel_id}",
                cookie,
                {
                    "name": CHANNEL_NAME,
                    "baseUrl": CHANNEL_BASE_URL,
                    "apiKey": api_key or channel.get("apiKey") or "",
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
    existing = next((row for row in models if str(row.get("modelKey") or "") == MODEL_KEY), None)
    if existing is None:
        request("POST", args.base_url, f"/admin/channels/{channel_id}/models", cookie, payload)
    elif model_drift(existing, payload):
        request(
            "PUT",
            args.base_url,
            f"/admin/channels/{channel_id}/models/{urllib.parse.quote(str(existing.get('id') or MODEL_KEY))}",
            cookie,
            payload,
        )

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {
        (row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row
        for row in prices
    }
    for row in PRICE_TIERS:
        body = {
            "modelKey": price_key(channel_id),
            "capability": CAPABILITY,
            "priceTier": row["tier"],
            "unit": UNIT,
            "vendorCode": VENDOR_CODE,
            "upstreamUnitPrice": None,
            "sellUnitPrice": row["sellFenPerItem"],
            "multiplier": None,
            "enabled": True,
            "note": note_for(row),
        }
        existing = price_index.get((body["modelKey"], CAPABILITY, row["tier"]))
        if existing is None:
            request("POST", args.base_url, "/admin/billing/model-prices", cookie, body)
        else:
            request("PUT", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(existing.get("id"))), cookie, body)

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
