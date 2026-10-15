#!/usr/bin/env python3
"""上架火山方舟（Ark）三款 Seedream 5.0 生图模型。

型号（上游 Model ID 即渠道模型标识、价目归属）：

| 显示名 | 上游 Model ID | 档位 | 上游单价（元/张） |
| --- | --- | --- | --- |
| Seedream 5.0 Pro | doubao-seedream-5-0-pro-260628 | 1K、1.5K、2K | 单图 ≤261 万像素 0.30，>261 万像素 0.60 |
| Seedream 5.0 Flash | doubao-seedream-5-0-flash-260915 | 1K、1.5K、2K | 0.12（不分档） |
| Seedream 5.0 Lite | doubao-seedream-5-0-260128 | 2K、3K、4K | 0.22（不分档） |

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' ARK_API_KEY='<方舟 API Key>' \\
        python3 scripts/seed-volcengine-ark-seedream.py

    KINO_ADMIN_COOKIE='...' ARK_API_KEY='...' \\
    KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-volcengine-ark-seedream.py --apply

密钥只从环境变量读，不落库到脚本、不写进仓库。

## 为什么另起渠道

火山方舟是独立账号：域名、鉴权（Bearer API Key）、报文结构都与现有七条渠道不同。渠道 Base URL
必须带 `/api/v3`：插件请求模板里的路径是 `/api/v3/images/generations`，宿主会用渠道 Base URL
的版本前缀把它折叠成一条（不会拼出 `/api/v3/api/v3/...`），而后台"测试连接"那条直连路径只按
渠道 Base URL 拼路径，少了 `/api/v3` 就会打到 `/v1/images/generations`。

## 尺寸：为什么用平台的标准像素表，而不是照抄官方档位表

官方 `size` 支持两种写法且不可混用：分辨率档位（`2K`）或宽×高（`2048x2048`）。档位写法把比例
交给提示词里的自然语言，用户在面板上就选不到 16:9 / 9:16 —— 而这个平台的比例是硬需求。

所以这里走官方的方式 2（精确像素），像素值取平台现成的 1K/2K/4K 标准表（与混元生图 3.5 同一张
表），并逐个核过官方给的取值范围：

| 模型 | 官方总像素范围 | 本脚本铺的档位 |
| --- | --- | --- |
| Seedream 5.0 Pro | [921600, 4624220] | 1K（1.05M–1.87M）、2K（3.98M–4.23M） |
| Seedream 5.0 Flash | 官方未公布，暂按 Pro 的口径 | 1K、2K |
| Seedream 5.0 Lite | [3686400, 16777216] | 2K（3.98M–4.23M）、4K（6.21M–8.29M） |

Lite 的 3K 与官方 4K（4096x4096 = 16.7M）不进面板：面板只有 1K/2K/4K 三档且上限 8.29M，
16.7M 的尺寸会被前台直接丢掉。用 3840x2160 这一档 4K 顶上，既在官方接受的像素范围内，也是
用户真正会用的规格。

Flash 的宽高像素范围官方没写；先按 Pro 的口径（≤4.62M）取 1K/2K，上线前用一张真图核对——若
上游回参数错误，把 FLASH 的尺寸表换成档位写法（`["auto", "1K", "1.5K", "2K"]`）即可。

## 定价：上游成本 × 5

口径与图片线其它模型一致（见 seed-image-model-prices.py）：售价 = 上游成本 × 固定倍率，倍率 5
对应约 80% 毛利。上游成本取方舟"模型服务价格"页的输出图单价，按平台的尺寸档折算：

| 模型 | 档位 | 上游成本 | 售价 | 毛利 |
| --- | --- | --- | --- | --- |
| Pro | SIZE_1K | 30 分 | 150 分 | 80% |
| Pro | SIZE_2K | 60 分 | 300 分 | 80% |
| Flash | SIZE_1K / SIZE_2K | 12 分 | 60 分 | 80% |
| Lite | SIZE_2K / SIZE_4K | 22 分 | 110 分 | 80% |

两点要记住：

- 上游只公布了两种成本（Pro ≤261 万像素 0.30、>261 万像素 0.60），所以 Pro 的两行价差是真实
  存在的成本差，不是运营口径。Flash / Lite 不分档，两行同价是照抄上游，不是抄错。
- 空档必须留：`auto`、比例串、以及自定义尺寸之外的请求算不出像素档，没有这一行会直接变成
  "未定价"。空档按该模型最贵的一档兜底（Pro 按 2K）。

输入图方面 Pro 第 2 张起收 0.02 元/张（Flash / Lite 免费），量级太小，不单独定价。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass

CHANNEL_NAME = "火山方舟 · 主账号"
CHANNEL_BASE_URL = "https://ark.cn-beijing.volces.com/api/v3"

# 生图是同步短任务，上游未公布并发上限；给 5 与其它主渠道一致，超了由上游 429 兜底。
CONCURRENCY_LIMIT = 5

PROTOCOL = "volcengine-ark-image"
CAPABILITY = "IMAGE"
UNIT = "IMAGE"
VENDOR_CODE = "volcengine-ark"

PROMPT_MAX_CHARS = 8000
# 参考图上限：官方 Pro / Flash 是 2-10 张，Lite 是 2-14 张。统一取 10——参考图按 data URL
# 内联，张数越多请求体越大，10 张已覆盖"换装 + 风格 + 姿势 + 场景"这类常见组合。
MAX_IMAGES = 10
MAX_IMAGE_BYTES = 30 * 1024 * 1024

# 平台标准像素表（与混元生图 3.5 同一张），1K/2K/4K 各十个比例。
STD_1K = [
    "1024x1024", "1024x1280", "1280x1024", "1536x1024", "1024x1536",
    "1360x1024", "1024x1360", "1824x1024", "1024x1824", "2048x878",
]
STD_2K = [
    "2048x2048", "1792x2240", "2240x1792", "2496x1664", "1664x2496",
    "2304x1728", "1728x2304", "2752x1536", "1536x2752", "3136x1344",
]
STD_4K = [
    "2880x2880", "2560x3200", "3200x2560", "3504x2336", "2336x3504",
    "3264x2448", "2448x3264", "3840x2160", "2160x3840", "3808x1632",
]

TIER_LABEL = {"SIZE_1K": "1K", "SIZE_2K": "2K", "SIZE_4K": "4K", "": "未指定尺寸"}


@dataclass(frozen=True)
class ArkModel:
    """一个上游型号：渠道模型标识、显示名、尺寸表、价目行。"""

    model_key: str
    display_name: str
    sizes: list[str]
    # 价目行：(档位, 上游成本分/张, 售价分/张)。空档必须留，按最贵的一档兜底。
    price_tiers: list[tuple[str, int, int]]


MODELS: list[ArkModel] = [
    ArkModel(
        model_key="doubao-seedream-5-0-pro-260628",
        display_name="Seedream 5.0 Pro",
        sizes=["auto", *STD_1K, *STD_2K],
        price_tiers=[("SIZE_1K", 30, 150), ("SIZE_2K", 60, 300), ("", 60, 300)],
    ),
    ArkModel(
        model_key="doubao-seedream-5-0-flash-260915",
        display_name="Seedream 5.0 Flash",
        sizes=["auto", *STD_1K, *STD_2K],
        price_tiers=[("SIZE_1K", 12, 60), ("SIZE_2K", 12, 60), ("", 12, 60)],
    ),
    ArkModel(
        model_key="doubao-seedream-5-0-260128",
        display_name="Seedream 5.0 Lite",
        sizes=["auto", *STD_2K, *STD_4K],
        price_tiers=[("SIZE_2K", 22, 110), ("SIZE_4K", 22, 110), ("", 22, 110)],
    ),
]


def capability_config(model: ArkModel) -> dict:
    """图片能力合同：尺寸给精确像素，质量/透明背景/返回格式整个关掉。

    上游确实有 quality 之外的 `background` 与 `output_format`，但这两项走插件命名空间的
    providerOptions（后台按型号填 defaultOptions），不在面板上开死开关——面板开了而插件
    不下发，等于给用户一个按了没反应的按钮。输出数量固定 1：官方参数表没有 `n`，要更多张
    就再点一次生成，平台按张计费才与上游账单一致。
    """
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
                "parameter": "size",
                "values": model.sizes,
                "default": "auto",
                "allowCustom": True,
            },
            "quality": {"supported": False, "values": [], "default": "auto"},
            "transparentBackground": {"supported": False, "default": False},
            "responseFormat": {"supported": False},
            "outputFormat": {"supported": False},
            "maxOutputs": 1,
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


def note_text(tier: str, upstream_fen: int, sell_fen: int) -> str:
    """每一行都要能独立回答"这笔钱是怎么来的"：档位、上游成本、我们的售价。"""
    label = TIER_LABEL[tier]
    if tier == "":
        return f"未指定尺寸（auto/自定义/比例）按该型号最贵档兜底：上游 ¥{upstream_fen / 100:.2f}/张，售价 {sell_fen} 分/张"
    return f"火山方舟官方输出图单价 {label} 档 ¥{upstream_fen / 100:.2f}/张，售价 {sell_fen} 分/张"


def print_economics() -> None:
    print(f"渠道「{CHANNEL_NAME}」{CHANNEL_BASE_URL}（协议 {PROTOCOL}，最大并发 {CONCURRENCY_LIMIT}）")
    for model in MODELS:
        print(f"  {model.display_name}（{model.model_key}）· {len(model.sizes) - 1} 个精确像素 + auto")
        for tier, upstream_fen, sell_fen in model.price_tiers:
            margin = 1 - upstream_fen / sell_fen
            print(
                f"    {TIER_LABEL[tier]:<6} 售价 {sell_fen / 100:.2f} 元/张 · 上游 {upstream_fen / 100:.2f} 元/张 · 毛利 {margin * 100:.0f}%"
            )
    print(f"  参考图上限 {MAX_IMAGES} 张，单图 ≤ {MAX_IMAGE_BYTES // (1024 * 1024)} MB；输出数量固定 1 张")


def model_payload(model: ArkModel) -> dict:
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
    """能力配置也要比：只比"模型在不在"，改了图片上限这类参数重跑脚本会被当成无变更。"""
    for field in ("providerModelKey", "displayName", "capability", "protocol"):
        if str(existing.get(field) or "") != str(payload[field]):
            return True
    if existing.get("enabled") is not True:
        return True
    return existing.get("capabilityConfig") != payload["capabilityConfig"]


def price_payload(model: ArkModel, tier: str, upstream_fen: int, sell_fen: int, channel_id: str) -> dict:
    return {
        "modelKey": price_key(model.model_key, channel_id),
        "capability": CAPABILITY,
        "priceTier": tier,
        "unit": UNIT,
        "vendorCode": VENDOR_CODE,
        "upstreamUnitPrice": upstream_fen,
        "sellUnitPrice": sell_fen,
        "multiplier": None,
        "enabled": True,
        "note": note_text(tier, upstream_fen, sell_fen),
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
    parser = argparse.ArgumentParser(description="上架火山方舟 Seedream 5.0 三款生图模型（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    parser.add_argument("--api-key", default=os.environ.get("ARK_API_KEY", ""))
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
        rows = request("GET", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
        existing_models = {str(row.get("modelKey") or ""): row for row in rows}

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {(row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row for row in prices}

    plan: list[str] = []
    if channel is None:
        plan.append(f"新建渠道「{CHANNEL_NAME}」（{CHANNEL_BASE_URL}）")
    elif str(channel.get("baseUrl") or "").rstrip("/") != CHANNEL_BASE_URL or int(channel.get("concurrencyLimit") or 0) != CONCURRENCY_LIMIT:
        plan.append(f"更新渠道「{CHANNEL_NAME}」的 Base URL 与并发上限")

    # 新渠道还没有 ID，价目 key 用占位前缀，让预览也能看到将要建的每一行价。
    price_scope = channel_id or "<新渠道>"
    for model in MODELS:
        payload = model_payload(model)
        existing = existing_models.get(model.model_key)
        if existing is None:
            plan.append(f"上架渠道模型 {model.display_name}（{CAPABILITY}）")
        elif model_drift(existing, payload):
            plan.append(f"更新渠道模型 {model.display_name}（能力配置或上游标识有变化）")
        for tier, upstream_fen, sell_fen in model.price_tiers:
            label = TIER_LABEL[tier] or "未指定尺寸"
            key = price_key(model.model_key, price_scope)
            current = price_index.get((key, CAPABILITY, tier)) if channel_id else None
            if current is None:
                plan.append(f"新增价目 {key} [{label}]：{sell_fen} 分/张（上游 {upstream_fen} 分/张）")
            elif price_drift(current, price_payload(model, tier, upstream_fen, sell_fen, channel_id)):
                plan.append(f"更新价目 {key} [{label}]：{sell_fen} 分/张（上游 {upstream_fen} 分/张）")

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
        print("缺少上游密钥：请设置 ARK_API_KEY", file=sys.stderr)
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

    rows = request("GET", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
    by_key = {str(row.get("modelKey") or ""): row for row in rows}
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

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {(row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row for row in prices}
    for model in MODELS:
        for tier, upstream_fen, sell_fen in model.price_tiers:
            row = price_payload(model, tier, upstream_fen, sell_fen, channel_id)
            current = price_index.get((row["modelKey"], CAPABILITY, tier))
            if current is None:
                request("POST", args.base_url, "/admin/billing/model-prices", cookie, row)
            elif price_drift(current, row):
                request("PUT", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(current.get("id"))), cookie, row)

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
