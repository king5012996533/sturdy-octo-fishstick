#!/usr/bin/env python3
"""上架 fal.ai 的 GPT Image 生图模型（`openai/gpt-image-2.5/{sunburst,flare}`、`openai/gpt-image-2`）。

fal 把同一个模型的文生图与图生图拆成两条端点：一条只吃 prompt，
一条（`{model}/edit`）必填参考图（实测缺字段与空数组都是 422）。插件按请求里有没有参考图自动选端点，
所以这里登记的是基名，用户侧就是一个模型两条路，不需要在模型列表里拆成两个。
两条端点上游同一套计价口径（都按 quality 与像素计价），共用一份价目。

文生图那条路径在 fal 有两套命名：2.5 是 `{model}/text-to-image`，
gpt-image-2 就是基名本身（POST /openai/gpt-image-2，试 /text-to-image 回 404）。
插件里有一份名单区分这两套，加模型前先确认属于哪一套。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
    FAL_API_KEY='<fal API Key>' \\
        python3 scripts/seed-fal-image-models.py

    KINO_ADMIN_COOKIE='...' FAL_API_KEY='...' \\
    KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-fal-image-models.py --apply

密钥只从环境变量读，不落库到脚本、不写进仓库。

## 为什么另起渠道

Replicate 与 fal 是两个独立的生图账号：域名、鉴权、报文结构都不同，共用一个渠道会让运营
分不清哪把钥匙配哪条线。两个渠道上的是同一个模型，所以这里刻意让能力合同与 Replicate 渠道的
`openai/gpt-image-2.5-{sunburst,flare}` 逐字段一致（比例、质量档、参考图上限、张数上限），
前端在两者之间切换时不会出现"换个渠道就少了两个比例"。

模型标识用 fal 的上游模型基名（价目行按"渠道 + 上游模型名"归属）：fal 目录里这些模型各两条端点，
基名与上游对得上，文生图与图生图是同一模型的两条路，不该当成两个模型定价。

## 为什么走 fal.run 同步端点

见 plugin-packages/fal-image/docs/interface.md。一句话：fal 的队列端点把结果放在第二个 GET 里，
本项目的声明式插件没有"轮询成功后再按 JSON 里的 URL 取一次"的编排能力，而取结果的接口在任务
未完成时返回 HTTP 400，会被轮询判成失败。

## 定价

上游按张计价，价格只跟质量档与像素档有关。成本取 fal 模型页 Pricing 表里 1024×1024 那一列，
也就是本插件的默认比例 1:1（上游映射到 `square_hd`）；售价不按倍率算，而是与 Replicate 渠道的
同型号逐档对齐：两条渠道同名模型在前台合并成一行，路由落到哪一条都不该改变扣费，
同名不同价等于让用户碰运气。

### GPT Image 2.5（sunburst / flare）

表头是 "pricing of ChatGPT Images 2.5"，sunburst 与 flare 共用同一张。上游确实更便宜
（HIGH 是 $0.0527，Replicate 那边同档 $0.129），对齐售价之后毛利 89%~94%：

| 档位 | 上游成本 | 售价（对齐 Replicate） |
| --- | --- | --- |
| LOW | $0.00588 | 45 分 |
| MEDIUM | $0.01317 | 170 分 |
| HIGH | $0.05268 | 465 分 |
| XHIGH | $0.09366 | 900 分 |
| MAX | $0.21072 | 1800 分 |
| 空档 | $0.05268 | 900 分 |

### GPT Image 2

表头是 "pricing of ChatGPT Images 2.0"。上游只认 `auto/low/medium/high`（`xhigh` 实测回
422 literal_error），所以能力合同只开三档；对应地 Replicate 那边这个模型也只有三档，
空档价落在 HIGH 上（465 分，不是 900）：

| 档位 | 上游成本 | 售价（对齐 Replicate） | 毛利 |
| --- | --- | --- | --- |
| LOW | $0.006 | 45 分 | 89% |
| MEDIUM | $0.053 | 170 分 | 77% |
| HIGH | $0.211 | 465 分 | 67% |
| 空档 | $0.211 | 465 分 | 67% |

这个模型上游并不比 Replicate 便宜：HIGH 档 fal 要 $0.211，Replicate 那边记的是 $0.129。
低价档 fal 更划算（LOW $0.006 对 $0.0125），中高价档反而更贵——所以"fal 优先"对 2.5 成立，
对 gpt-image-2 只在 LOW 档成立。

空档都单独留一行，价等于该模型的 HIGH：fal 在没收到 `quality` 时按 `high` 出图，
空档按 HIGH 兜底才不会倒贴。

本插件只开放比例（不开自定义像素），不同比例落到 fal 的命名档位（`landscape_16_9` 这类）
或显式像素（3:2 → 1536×1024）。比例里像素最多的是 1536×1024，但按上游那张表它比 1024×1024
还便宜，所以按 1024×1024 取成本就是本插件可及尺寸的上界。

换模型只改 MODELS；每个模型自带 quality_tiers / price_tiers / aligned_sell_fen 三份数据，改一处就够。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from decimal import ROUND_CEILING, Decimal

# 渠道对外名字。用户侧看不到渠道名（平台目录按显示名平铺型号），这里只影响后台。
CHANNEL_NAME = "fal.ai · 主账号"
CHANNEL_BASE_URL = "https://fal.run"

PROTOCOL = "fal-image"

# 同步端点一次请求要阻塞到出图，并发上限直接等于"同时在跑的图片数"。
CONCURRENCY_LIMIT = 5

# 能力合同与 Replicate 渠道的同型号逐字段一致，前端切换渠道时参数不缩水。
# 比例是七个共享档；质量档跟着模型走（2.5 五档，gpt-image-2 只有三档，见 MODELS）。
RATIOS = ["1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4"]
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
# 落到哪条渠道，若两条价格不同，用户就会莫名其妙被多收或少收。
# 对齐到 Replicate 的售价之后毛利多少，取决于上游成本，所以每个模型自带一份对齐表，
# 一档一价写在模型定义里，一眼能看出"这个模型每一档卖多少、上游花多少"。
TIER_LABEL = {"LOW": "low", "MEDIUM": "medium", "HIGH": "high", "XHIGH": "xhigh", "MAX": "max", "": "未指定质量"}


@dataclass(frozen=True)
class FalImageModel:
    """渠道模型标识就是 fal 的模型基名；显示名必须与 Replicate 同型号逐字一致，否则前台不会合并成一行。

    每个模型自带三份数据，因为它们并不通用：上游模型的档位数量不同（2.5 五档、gpt-image-2 三档），
    上游成本不同，Replicate 侧的售价也不同。把它们挂在模型上而不是写成全局常量，加模型时改一处就够。
    """

    model_key: str
    display_name: str
    # 能力合同里开放的质量档，必须与 Replicate 渠道同型号完全一致，否则换个渠道就少了选项。
    quality_tiers: tuple[str, ...]
    # 价目行：档位 → 上游美元/张。上游调价只改这里一个数。
    price_tiers: tuple[tuple[str, str], ...]
    # 档位 → 售价（分/张），与 Replicate 渠道同型号逐档一致。
    aligned_sell_fen: dict[str, int]
    # 历史条目：曾经按 /edit 上过架，重跑脚本时连同旧价目一起清掉。
    legacy_model_keys: tuple[str, ...] = field(default_factory=tuple)


# 2.5 的售价对齐表：Replicate 渠道这五个档位的上游成本是 9/34/93/180/360 分，倍率 5 倍。
GPT_IMAGE_25_SELL_FEN: dict[str, int] = {
    "LOW": 45,
    "MEDIUM": 170,
    "HIGH": 465,
    "XHIGH": 900,
    "MAX": 1800,
    # 空档价跟着 HIGH：fal 没收到 quality 时按 high 出图。
    "": 900,
}
QUALITY_TIERS_25 = ("low", "medium", "high", "xhigh", "max")
# 2.5 的上游成本取自 fal 模型页 "pricing of ChatGPT Images 2.5" 那张表的 1024×1024 列。
PRICE_TIERS_25: tuple[tuple[str, str], ...] = (
    ("LOW", "0.00588"),
    ("MEDIUM", "0.01317"),
    ("HIGH", "0.05268"),
    ("XHIGH", "0.09366"),
    ("MAX", "0.21072"),
    # 未指定 quality 时 fal 按 high 出图，空档必须按 HIGH 的成本兜底。
    ("", "0.05268"),
)

# gpt-image-2 的售价对齐表：Replicate 渠道这四档的上游成本是 9/34/93/93 分，倍率同样是 5 倍。
# 注意空档是 93 而不是 180——Replicate 那边 gpt-image-2 没有 xhigh/max，空档落在 HIGH 上。
GPT_IMAGE_2_SELL_FEN: dict[str, int] = {
    "LOW": 45,
    "MEDIUM": 170,
    "HIGH": 465,
    "": 465,
}
# 上游只认 auto/low/medium/high（xhigh 实测回 422 literal_error），所以这里只开三档。
QUALITY_TIERS_2 = ("low", "medium", "high")
# 上游成本取自 fal 模型页 "pricing of ChatGPT Images 2.0" 那张表的 1024×1024 列：
# low $0.006 / medium $0.053 / high $0.211。比例里像素最多的是 3:2 的 1536×1024，
# 但按表它比 1024×1024 还便宜（$0.005/$0.042/$0.165），所以 1024×1024 就是本插件可及尺寸的成本上界。
PRICE_TIERS_2: tuple[tuple[str, str], ...] = (
    ("LOW", "0.006"),
    ("MEDIUM", "0.053"),
    ("HIGH", "0.211"),
    ("", "0.211"),
)

MODELS: list[FalImageModel] = [
    FalImageModel(
        model_key="openai/gpt-image-2.5/sunburst",
        display_name="GPT Image 2.5 Sunburst",
        quality_tiers=QUALITY_TIERS_25,
        price_tiers=PRICE_TIERS_25,
        aligned_sell_fen=GPT_IMAGE_25_SELL_FEN,
        legacy_model_keys=("openai/gpt-image-2.5/sunburst/edit",),
    ),
    FalImageModel(
        model_key="openai/gpt-image-2.5/flare",
        display_name="GPT Image 2.5 Flare",
        quality_tiers=QUALITY_TIERS_25,
        price_tiers=PRICE_TIERS_25,
        aligned_sell_fen=GPT_IMAGE_25_SELL_FEN,
    ),
    FalImageModel(
        # fal 目录里这个模型的文生图端点就是基名本身（POST /openai/gpt-image-2），
        # 不是 /text-to-image；插件按基名名单切路径，见 plugin-packages/fal-image/docs/interface.md。
        model_key="openai/gpt-image-2",
        display_name="GPT Image 2",
        quality_tiers=QUALITY_TIERS_2,
        price_tiers=PRICE_TIERS_2,
        aligned_sell_fen=GPT_IMAGE_2_SELL_FEN,
    ),
]


def capability_config(model: FalImageModel) -> dict:
    """图片能力合同：比例七档是共享的，质量档跟着模型（2.5 五档、gpt-image-2 三档）。"""
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
            "quality": {"supported": True, "values": list(model.quality_tiers), "default": QUALITY_DEFAULT},
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


def sell_fen(model: FalImageModel, tier: str) -> int:
    """售价取该模型的对齐表：与 Replicate 同型号的一档一价，不再由上游成本推。"""
    return model.aligned_sell_fen[tier]


def note_text(tier: str, usd: str) -> str:
    """每一行都要能独立回答"这笔钱是怎么来的"：档位、上游成本、价位。"""
    if tier == "":
        return f"售价对齐 Replicate 同型号（未指定质量按 high）·上游 fal ${usd}/张"
    return f"售价对齐 Replicate 同型号 {TIER_LABEL[tier]} 档 · 上游 fal ${usd}/张"


def text_to_image_endpoint(model: FalImageModel) -> str:
    """fal 的文生图路径有两套命名：多数是 {model}/text-to-image，gpt-image-2 这类就是基名本身。"""
    return model.model_key if model.model_key == "openai/gpt-image-2" else f"{model.model_key}/text-to-image"


def print_economics() -> None:
    print(f"渠道「{CHANNEL_NAME}」{CHANNEL_BASE_URL}（协议 {PROTOCOL}，最大并发 {CONCURRENCY_LIMIT}）")
    for model in MODELS:
        tiers = "/".join(model.quality_tiers)
        print(f"  模型 {model.display_name}（{model.model_key}）质量档 {tiers}")
        print(f"    文生图 POST /{text_to_image_endpoint(model)}；图生图 POST /{model.model_key}/edit；同步端点，单次请求阻塞到出图")
        for tier, usd in model.price_tiers:
            label = TIER_LABEL[tier]
            cost = Decimal(upstream_fen(usd)) / 100
            sell = Decimal(sell_fen(model, tier)) / 100
            print(f"    {label:<8} 售价 {sell:.2f} 元/张（对齐 Replicate） · 上游 {cost:.2f} 元/张 · 毛利 {1 - cost / sell:.0%}")
    print(f"  比例 {len(RATIOS)} 个：{'、'.join(RATIOS)}；参考图上限 {MAX_IMAGES} 张；单次最多 {MAX_OUTPUTS} 张")


def model_payload(model: FalImageModel) -> dict:
    return {
        "modelKey": model.model_key,
        "providerModelKey": model.model_key,
        "displayName": model.display_name,
        "capability": CAPABILITY.lower(),
        "protocol": PROTOCOL,
        "enabled": True,
        "capabilityConfig": capability_config(model),
    }


def model_drift(existing: dict, payload: dict) -> bool:
    """能力配置也要比：只比"模型在不在"，改了参考图上限这类参数重跑脚本会被当成无变更。"""
    for field in ("providerModelKey", "displayName", "capability", "protocol"):
        if str(existing.get(field) or "") != str(payload[field]):
            return True
    if existing.get("enabled") is not True:
        return True
    return existing.get("capabilityConfig") != payload["capabilityConfig"]


def price_payload(model: FalImageModel, tier: str, usd: str, channel_id: str) -> dict:
    return {
        "modelKey": price_key(model.model_key, channel_id),
        "capability": CAPABILITY,
        "priceTier": tier,
        "unit": UNIT,
        "vendorCode": VENDOR_CODE,
        # 上游成本照实记，售价直接定价：上游调价不改用户价，只改这里算出毛利。
        "upstreamUnitPrice": upstream_fen(usd),
        "sellUnitPrice": sell_fen(model, tier),
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


def read_models(base_url: str, cookie: str, channel_id: str) -> dict[str, dict]:
    models = request("GET", base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
    return {str(row.get("modelKey") or ""): row for row in models}


def read_prices(base_url: str, cookie: str) -> list[dict]:
    return request("GET", base_url, "/admin/billing/model-prices", cookie).get("prices") or []


def main() -> int:
    parser = argparse.ArgumentParser(description="上架 fal.ai GPT Image 2.5 生图模型（默认只预览）")
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

    existing_models = read_models(args.base_url, cookie, channel_id) if channel_id else {}
    prices = read_prices(args.base_url, cookie)
    price_index = {(row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row for row in prices}

    plan: list[str] = []
    if channel is None:
        plan.append(f"新建渠道「{CHANNEL_NAME}」（{CHANNEL_BASE_URL}）")
    elif str(channel.get("baseUrl") or "").rstrip("/") != CHANNEL_BASE_URL or int(channel.get("concurrencyLimit") or 0) != CONCURRENCY_LIMIT:
        plan.append(f"更新渠道「{CHANNEL_NAME}」的 Base URL 与并发上限")

    for model in MODELS:
        payload = model_payload(model)
        key = price_key(model.model_key, channel_id)
        if channel_id:
            for legacy_model_key in model.legacy_model_keys:
                if existing_models.get(legacy_model_key) is not None:
                    plan.append(f"删除旧模型条目 {legacy_model_key}（改用基名，端点由参考图决定）")
                legacy_key = price_key(legacy_model_key, channel_id)
                for row in [item for item in prices if str(item.get("modelKey") or "") == legacy_key]:
                    plan.append(f"删除旧价目 {legacy_key} [{TIER_LABEL.get(row.get('priceTier') or '', row.get('priceTier'))}]")
            existing_model = existing_models.get(model.model_key)
            if existing_model is None:
                plan.append(f"上架渠道模型 {model.display_name}（{CAPABILITY}）")
            elif model_drift(existing_model, payload):
                plan.append(f"更新渠道模型 {model.display_name}（能力配置或上游标识有变化）")
            for tier, usd in model.price_tiers:
                label = TIER_LABEL[tier]
                current = price_index.get((key, CAPABILITY, tier))
                if current is None:
                    plan.append(f"新增价目 {key} [{label}]：{sell_fen(model, tier)} 分/张（对齐 Replicate；上游 {upstream_fen(usd)} 分/张）")
                elif price_drift(current, price_payload(model, tier, usd, channel_id)):
                    plan.append(f"更新价目 {key} [{label}]：{sell_fen(model, tier)} 分/张（对齐 Replicate；上游 {upstream_fen(usd)} 分/张）")

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

    by_key = read_models(args.base_url, cookie, channel_id)
    for model in MODELS:
        for legacy_model_key in model.legacy_model_keys:
            legacy = by_key.get(legacy_model_key)
            if legacy is not None:
                request("DELETE", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models/{urllib.parse.quote(str(legacy.get('id')))}", cookie)

    prices = read_prices(args.base_url, cookie)
    for model in MODELS:
        for legacy_model_key in model.legacy_model_keys:
            legacy_key = price_key(legacy_model_key, channel_id)
            for row in [item for item in prices if str(item.get("modelKey") or "") == legacy_key]:
                request("DELETE", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(row.get("id"))), cookie)

    for model in MODELS:
        payload = model_payload(model)
        existing = by_key.get(model.model_key)
        if existing is None:
            request("POST", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie, payload)
        elif model_drift(existing, payload):
            request(
                "PUT",
                args.base_url,
                f"/admin/channels/{urllib.parse.quote(channel_id)}/models/{urllib.parse.quote(str(existing.get('id') or model.model_key))}",
                cookie,
                payload,
            )

    prices = read_prices(args.base_url, cookie)
    price_index = {(row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row for row in prices}
    for model in MODELS:
        for tier, usd in model.price_tiers:
            row = price_payload(model, tier, usd, channel_id)
            current = price_index.get((row["modelKey"], CAPABILITY, tier))
            if current is None:
                request("POST", args.base_url, "/admin/billing/model-prices", cookie, row)
            elif price_drift(current, row):
                request("PUT", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(current.get("id"))), cookie, row)

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
