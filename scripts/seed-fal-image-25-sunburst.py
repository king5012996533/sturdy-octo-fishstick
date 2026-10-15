#!/usr/bin/env python3
"""上架 fal.ai 的 GPT Image 2.5 Sunburst（基名 openai/gpt-image-2.5/sunburst）。

fal 把这个模型的文生图与图生图拆成两条端点：`{model}/text-to-image` 只吃 prompt，
`{model}/edit` 必填参考图（实测缺字段与空数组都是 422）。插件按请求里有没有参考图自动选端点，
所以这里登记的是基名，用户侧就是一个模型两条路，不需要在模型列表里拆成两个。
两条端点上游同一套计价口径（都按 quality 与像素计价），共用一份价目。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
    FAL_API_KEY='<fal API Key>' \\
        python3 scripts/seed-fal-image-25-sunburst.py

    KINO_ADMIN_COOKIE='...' FAL_API_KEY='...' \\
    KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-fal-image-25-sunburst.py --apply

密钥只从环境变量读，不落库到脚本、不写进仓库。

## 为什么另起渠道

Replicate 与 fal 是两个独立的生图账号：域名、鉴权、报文结构都不同，共用一个渠道会让运营
分不清哪把钥匙配哪条线。两个渠道上的是同一个模型，所以这里刻意让能力合同与 Replicate 渠道的
`openai/gpt-image-2.5-sunburst` 逐字段一致（比例、质量档、参考图上限、张数上限），
前端在两者之间切换时不会出现"换个渠道就少了两个比例"。

模型标识用 fal 的上游模型基名 `openai/gpt-image-2.5/sunburst`（价目行按"渠道 + 上游模型名"归属）：
fal 目录里这个模型就是 `gpt-image-2.5/sunburst` 一族的两条端点，基名与上游对得上，
`/edit` 与 `/text-to-image` 是同一模型的两条路，不该当成两个模型定价。

## 为什么走 fal.run 同步端点

见 plugin-packages/fal-image/docs/interface.md。一句话：fal 的队列端点把结果放在第二个 GET 里，
本项目的声明式插件没有"轮询成功后再按 JSON 里的 URL 取一次"的编排能力，而取结果的接口在任务
未完成时返回 HTTP 400，会被轮询判成失败。

## 定价

上游按张计价，价格只跟质量档与像素档有关。这里按 fal 官方 1024×1024（本插件的默认比例 1:1
映射到 `square_hd`）那一列取成本：

| 档位 | 上游成本 | 售价（对齐 Replicate） |
| --- | --- | --- |
| LOW | $0.00588 | 45 分 |
| MEDIUM | $0.01317 | 170 分 |
| HIGH | $0.05268 | 465 分 |
| XHIGH | $0.09366 | 900 分 |
| MAX | $0.21072 | 1800 分 |
| 空档 | $0.05268 | 900 分 |

售价不再按倍率算，而是与 Replicate 渠道的同型号逐档对齐：两条渠道同名模型在前台合并成一行，
路由落到哪一条都不该改变扣费，同名不同价等于让用户碰运气。上游 fal 更便宜，对齐之后毛利更高。
空档单独留一行，价等于 HIGH：fal 在没收到 `quality` 时按 `high` 出图，空档按 HIGH 兜底才不会倒贴。

本插件只开放比例（不开自定义像素），不同比例落到 fal 的命名档位（`landscape_16_9` 这类，
都在 1024×576 到 1024×1024 之间），因此按 1024×1024 取成本是这些比例里的上界。

改价改下面的 PRICE_TIERS 常量再重跑脚本。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from decimal import ROUND_CEILING, Decimal

# 渠道对外名字。用户侧看不到渠道名（平台目录按显示名平铺型号），这里只影响后台。
CHANNEL_NAME = "fal.ai · 主账号"
CHANNEL_BASE_URL = "https://fal.run"

# 上游模型标识：渠道模型标识、请求路径、价目归属都用它。
# 用基名而不是 /edit：fal 把文生图与图生图拆成两条端点（{model}/text-to-image 与 {model}/edit），
# 插件按请求里有没有参考图自动选端点，用户侧就是一个模型两条路。
MODEL_KEY = "openai/gpt-image-2.5/sunburst"
DISPLAY_NAME = "GPT Image 2.5 Sunburst"
# 曾经按 /edit 上过架，重跑本脚本时会把这个旧条目与旧价目行一并清掉。
LEGACY_MODEL_KEY = f"{MODEL_KEY}/edit"
PROTOCOL = "fal-image"

# 同步端点一次请求要阻塞到出图，并发上限直接等于"同时在跑的图片数"。
CONCURRENCY_LIMIT = 5

# 能力合同与 Replicate 渠道的同型号逐字段一致，前端切换渠道时参数不缩水。
RATIOS = ["1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4"]
QUALITY_TIERS = ["low", "medium", "high", "xhigh", "max"]
QUALITY_DEFAULT = "low"
MAX_IMAGES = 4
MAX_IMAGE_BYTES = 20 * 1024 * 1024
PROMPT_MAX_CHARS = 32000
MAX_OUTPUTS = 10

CAPABILITY = "IMAGE"
UNIT = "IMAGE"
VENDOR_CODE = "fal"

# 美元 → 人民币汇率与 seed-image-model-prices.py 保持同一个数，否则同一条图片线会出现两套换算。
USD_CNY_RATE = "7.2"
# 售价不按倍率算，而是与 Replicate 渠道的同型号逐档对齐（单位：分/张）。
#
# 同一个模型对用户只该有一个价：前台把同名跨渠道的模型合并成一行，选中后由兼容路由决定
# 落到哪条渠道，若两条价格不同，用户就会莫名其妙被多收或少收。上游成本 fal 更低，
# 所以对齐到 Replicate 的售价之后，毛利反而更高（见 print_economics 的输出）。
ALIGNED_SELL_FEN: dict[str, int] = {
    "LOW": 45,
    "MEDIUM": 170,
    "HIGH": 465,
    "XHIGH": 900,
    "MAX": 1800,
    # 空档价跟着 HIGH：fal 没收到 quality 时按 high 出图。
    "": 900,
}

# 价目行：档位 → 上游美元/张。售价由倍率算出，上游调价只改这里一个数。
PRICE_TIERS: list[tuple[str, str]] = [
    ("LOW", "0.00588"),
    ("MEDIUM", "0.01317"),
    ("HIGH", "0.05268"),
    ("XHIGH", "0.09366"),
    ("MAX", "0.21072"),
    # 未指定 quality 时 fal 按 high 出图，空档必须按 HIGH 的成本兜底。
    ("", "0.05268"),
]

TIER_LABEL = {"LOW": "low", "MEDIUM": "medium", "HIGH": "high", "XHIGH": "xhigh", "MAX": "max", "": "未指定质量"}


def capability_config() -> dict:
    """图片能力合同：比例七档、质量五档，全部落在 1024×1024 以内。"""
    return {
        "version": 1,
        "image": {
            "references": {
                "promptMaxChars": PROMPT_MAX_CHARS,
                "maxImages": MAX_IMAGES,
                "maxImageBytes": MAX_IMAGE_BYTES,
                "maskSupported": False,
            },
            "size": {
                "parameter": "aspect_ratio",
                "values": list(RATIOS),
                "default": RATIOS[0],
                "allowCustom": False,
            },
            "quality": {"supported": True, "values": list(QUALITY_TIERS), "default": QUALITY_DEFAULT},
            "transparentBackground": {"supported": False, "default": False},
            "responseFormat": {"supported": False},
            "outputFormat": {"supported": False},
            "maxOutputs": MAX_OUTPUTS,
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


def upstream_fen(usd: str) -> int:
    """美元/张 → 分/张，向上取整：宁可多收一分，也不因为截断倒贴。"""
    return int((Decimal(usd) * Decimal(USD_CNY_RATE) * 100).to_integral_value(rounding=ROUND_CEILING))


def sell_fen(tier: str) -> int:
    """售价取对齐表：与 Replicate 同型号的一档一价，不再由上游成本推。"""
    return ALIGNED_SELL_FEN[tier]


def note_text(tier: str, usd: str) -> str:
    """每一行都要能独立回答"这笔钱是怎么来的"：档位、上游成本、倍率、价位。"""
    if tier == "":
        return f"售价对齐 Replicate 同型号（未指定质量按 high）·上游 fal ${usd}/张"
    return f"售价对齐 Replicate 同型号 {TIER_LABEL[tier]} 档 · 上游 fal ${usd}/张"


def print_economics() -> None:
    print(f"渠道「{CHANNEL_NAME}」{CHANNEL_BASE_URL}（协议 {PROTOCOL}，最大并发 {CONCURRENCY_LIMIT}）")
    print(f"  模型 {DISPLAY_NAME}（{MODEL_KEY}）；有参考图走 /edit，没有走 /text-to-image；同步端点，单次请求阻塞到出图")
    for tier, usd in PRICE_TIERS:
        label = TIER_LABEL[tier]
        cost = Decimal(upstream_fen(usd)) / 100
        sell = Decimal(sell_fen(tier)) / 100
        print(f"  {label:<8} 售价 {sell:.2f} 元/张（对齐 Replicate） · 上游 {cost:.2f} 元/张 · 毛利 {1 - cost / sell:.0%}")
    print(f"  比例 {len(RATIOS)} 个：{'、'.join(RATIOS)}；参考图上限 {MAX_IMAGES} 张；单次最多 {MAX_OUTPUTS} 张")


def model_payload() -> dict:
    return {
        "modelKey": MODEL_KEY,
        "providerModelKey": MODEL_KEY,
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


def price_payload(tier: str, usd: str, channel_id: str) -> dict:
    return {
        "modelKey": price_key(MODEL_KEY, channel_id),
        "capability": CAPABILITY,
        "priceTier": tier,
        "unit": UNIT,
        "vendorCode": VENDOR_CODE,
        # 上游成本照实记，售价直接定价：上游调价不改用户价，只改这里算出毛利。
        "upstreamUnitPrice": upstream_fen(usd),
        "sellUnitPrice": sell_fen(tier),
        "enabled": True,
        "note": note_text(tier, usd),
    }


def price_drift(existing: dict, payload: dict) -> bool:
    """逐字段比，且只比这份脚本负责的字段：后台另填的备注不算漂移。"""
    for field in ("unit", "upstreamUnitPrice", "sellUnitPrice", "enabled"):
        if existing.get(field) != payload[field]:
            return True
    return str(existing.get("note") or "") != payload["note"]


def find_channel(base_url: str, cookie: str) -> dict | None:
    page = request("GET", base_url, "/admin/channels?pageSize=200", cookie)
    for channel in page.get("channels") or []:
        if str(channel.get("name") or "").strip() == CHANNEL_NAME:
            return channel
    return None


def main() -> int:
    parser = argparse.ArgumentParser(description="上架 fal.ai GPT Image 2.5 Sunburst（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    parser.add_argument("--api-key", default=os.environ.get("FAL_API_KEY", ""))
    args = parser.parse_args()

    cookie = args.cookie.strip()
    if not cookie:
        print("缺少管理员会话：请设置 KINO_ADMIN_COOKIE（浏览器里任意 /api/admin/* 请求的 Cookie 头）", file=sys.stderr)
        return 2

    print_economics()

    channel = find_channel(args.base_url, cookie)
    channel_id = str(channel.get("id") or "") if channel else ""

    existing_models: dict[str, dict] = {}
    if channel_id:
        models = request("GET", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
        existing_models = {str(row.get("modelKey") or ""): row for row in models}

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {(row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row for row in prices}

    payload = model_payload()
    plan: list[str] = []
    if channel is None:
        plan.append(f"新建渠道「{CHANNEL_NAME}」（{CHANNEL_BASE_URL}）")
    elif str(channel.get("baseUrl") or "").rstrip("/") != CHANNEL_BASE_URL or int(channel.get("concurrencyLimit") or 0) != CONCURRENCY_LIMIT:
        plan.append(f"更新渠道「{CHANNEL_NAME}」的 Base URL 与并发上限")

    key = price_key(MODEL_KEY, channel_id)
    legacy_key = price_key(LEGACY_MODEL_KEY, channel_id)
    legacy_model = existing_models.get(LEGACY_MODEL_KEY) if channel_id else None
    legacy_prices = [row for row in prices if str(row.get("modelKey") or "") == legacy_key] if channel_id else []
    if legacy_model is not None:
        plan.append(f"删除旧模型条目 {LEGACY_MODEL_KEY}（改用基名，端点由参考图决定）")
    for row in legacy_prices:
        plan.append(f"删除旧价目 {legacy_key} [{TIER_LABEL.get(row.get('priceTier') or '', row.get('priceTier'))}]")
    existing_model = existing_models.get(MODEL_KEY)
    if channel_id:
        if existing_model is None:
            plan.append(f"上架渠道模型 {DISPLAY_NAME}（{CAPABILITY}）")
        elif model_drift(existing_model, payload):
            plan.append(f"更新渠道模型 {DISPLAY_NAME}（能力配置或上游标识有变化）")
        for tier, usd in PRICE_TIERS:
            label = TIER_LABEL[tier]
            current = price_index.get((key, CAPABILITY, tier))
            if current is None:
                plan.append(f"新增价目 {key} [{label}]：{sell_fen(tier)} 分/张（对齐 Replicate；上游 {upstream_fen(usd)} 分/张）")
            elif price_drift(current, price_payload(tier, usd, channel_id)):
                plan.append(f"更新价目 {key} [{label}]：{sell_fen(tier)} 分/张（对齐 Replicate；上游 {upstream_fen(usd)} 分/张）")

    if not plan:
        print("\n渠道、模型与价目已经是目标状态，无需变更。")
        return 0

    for line in plan:
        print(" ·", line)

    if not args.apply:
        print(f"\n预览结束：{len(plan)} 项待写入。加 --apply 才会真正写库。")
        return 0

    api_key = args.api_key.strip()
    if not api_key:
        print("缺少上游密钥：请设置 FAL_API_KEY", file=sys.stderr)
        return 2

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
        if str(channel.get("baseUrl") or "").rstrip("/") != CHANNEL_BASE_URL or int(channel.get("concurrencyLimit") or 0) != CONCURRENCY_LIMIT:
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

    models = request("GET", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
    by_key = {str(row.get("modelKey") or ""): row for row in models}
    legacy = by_key.get(LEGACY_MODEL_KEY)
    if legacy is not None:
        request("DELETE", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models/{urllib.parse.quote(str(legacy.get('id')))}", cookie)

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    legacy_key = price_key(LEGACY_MODEL_KEY, channel_id)
    for row in [item for item in prices if str(item.get("modelKey") or "") == legacy_key]:
        request("DELETE", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(row.get("id"))), cookie)

    existing = by_key.get(MODEL_KEY)
    if existing is None:
        request("POST", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie, payload)
    elif model_drift(existing, payload):
        request(
            "PUT",
            args.base_url,
            f"/admin/channels/{urllib.parse.quote(channel_id)}/models/{urllib.parse.quote(str(existing.get('id') or MODEL_KEY))}",
            cookie,
            payload,
        )

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {(row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row for row in prices}
    for tier, usd in PRICE_TIERS:
        row = price_payload(tier, usd, channel_id)
        current = price_index.get((row["modelKey"], CAPABILITY, tier))
        if current is None:
            request("POST", args.base_url, "/admin/billing/model-prices", cookie, row)
        elif price_drift(current, row):
            request("PUT", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(current.get("id"))), cookie, row)

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
