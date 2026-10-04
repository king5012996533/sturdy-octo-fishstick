#!/usr/bin/env python3
"""上架腾讯云 TokenHub 混元生图 3.5（hy-image-v3.5-preview）。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
    TENCENT_TOKENHUB_API_KEY='<TokenHub API Key>' \\
        python3 scripts/seed-tencent-hunyuan-image-35.py

    KINO_ADMIN_COOKIE='...' TENCENT_TOKENHUB_API_KEY='...' \\
    KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-tencent-hunyuan-image-35.py --apply

密钥只从环境变量读，不落库到脚本、不写进仓库。

## 为什么另起渠道，而不是并进已有的腾讯混元渠道

仓库里已有的 `tencent-hunyuan-image` 插件打的是腾讯云 CAPI（`hunyuan.tencentcloudapi.com`、
TC3 签名、SecretId/SecretKey）。本模型走的是 TokenHub（MaaS）：Bearer API Key、
`/v1/wand/hunyuan-image/v35-generation`、OpenAI Chat 风格的 messages 请求体。鉴权、路径、
报文结构三处都不同，共用一个渠道只会让运营分不清哪把钥匙配哪条线。

## 尺寸档位

上游 `size` 只接受 `宽x高`（宽高 ∈ [256,8192]，面积 ≤ 16777216），不传时由模型按语义自选宽高，
另可用 `generate_max_pixels` 指定 1K/1.5K/2K 目标面积。这里直接铺 1K/2K/4K 各十个比例，
让面板能像别的图片模型一样按"档位 + 比例"选。

尺寸上限卡在 8,294,400 像素（约 2880×2880）：不是为了上游，而是前台的分档解析把大于这个数的
尺寸直接丢掉（见 web/src/lib/image-resolution-tiers.ts），再大的值填进去也不会出现在面板上。
真要 4096×4096 时，面板的自定义尺寸仍然可以填，上游也接受。

## 定价

上游按 token 计费，单价 10 元/百万 tokens，单张用量只跟面积档位有关：

| 档位 | 上游 tokens | 上游成本 |
| --- | --- | --- |
| 1K / 2K | 15,000 | ¥0.15/张 |
| 4K | 20,000 | ¥0.20/张 |

所以档位差价只有 5 分，而图片线的计费档位是按上游 quality 划分的（见 auth.ImagePriceTiers），
这个模型没有 quality 维度，一张卡填不出两个价。取整策略：**统一按 1K/2K 上游成本 ×5 定价
（75 分/张）**，4K 时不涨价——4K 那单毛利率从 80% 降到 73%，仍高于"按秒折算"和"按 4K
成本定价（100 分）"两种做法的副作用（前者会让同一张卡在 2K/4K 出现两个价，后者会让
绝大多数 1K/2K 用户多付 33%）。上游若把 4K 价格拉开，再按分辨率拆卡重定价。

倍率 5 与图片线其它模型同源（见 seed-image-model-prices.py），改价改这里的常量重跑脚本。
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

CHANNEL_NAME = "腾讯 TokenHub · 主账号"
CHANNEL_BASE_URL = "https://tokenhub.tencentmaas.com"

# 上游模型标识（TokenHub 的 model 参数值），也是渠道模型标识与价目归属。
MODEL_KEY = "hy-image-v3.5-preview"
DISPLAY_NAME = "混元生图 3.5"
PROTOCOL = "tencent-hunyuan-image-35"

# 上游没有公布并发上限；图片是同步短任务，给 4 与其它主渠道一致，超了由上游 429 兜底。
CONCURRENCY_LIMIT = 4

# 参考图上限：上游一轮最多 20 张、单图 ≤ 20MB。这里是平台的取舍——参考图按 data URL 内联，
# 张数越多请求体越大，6 张覆盖"换装 + 风格 + 姿势 + 场景"这类常见组合已经够用。
MAX_IMAGES = 6
MAX_IMAGE_BYTES = 20 * 1024 * 1024
PROMPT_MAX_CHARS = 8000

# 后台/前台共用的精确像素档位：1K、2K、4K 各十个比例。
SIZES = [
    "1024x1024", "1024x1280", "1280x1024", "1536x1024", "1024x1536",
    "1360x1024", "1024x1360", "1824x1024", "1024x1824", "2048x878",
    "2048x2048", "1792x2240", "2240x1792", "2496x1664", "1664x2496",
    "2304x1728", "1728x2304", "2752x1536", "1536x2752", "3136x1344",
    "2880x2880", "2560x3200", "3200x2560", "3504x2336", "2336x3504",
    "3264x2448", "2448x3264", "3840x2160", "2160x3840", "3808x1632",
]

CAPABILITY = "IMAGE"
UNIT = "IMAGE"
VENDOR_CODE = "tencent"

# 售价：分/张。上游 1K/2K ¥0.15、4K ¥0.20，统一按 ¥0.15 的 5 倍卖（见文件头定价说明）。
SELL_FEN_PER_IMAGE = 75
UPSTREAM_FEN_1K = 15
UPSTREAM_FEN_4K = 20
MULTIPLIER = "5"
MULTIPLIER_BP = int(Decimal(MULTIPLIER) * 10000)


def capability_config() -> dict:
    """图片能力合同：尺寸按精确像素给，质量维度整个关掉（上游没有 quality 参数）。"""
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
                "values": ["auto", *SIZES],
                "default": "auto",
                "allowCustom": True,
            },
            "quality": {"supported": False, "values": [], "default": "auto"},
            "transparentBackground": {"supported": False, "default": False},
            "responseFormat": {"supported": False},
            "outputFormat": {"supported": False},
            # 上游一次请求只出一张：多图由用户再点一次，平台按张计费才与上游账单一致。
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


def note_text() -> str:
    return (
        f"上游 TokenHub 混元生图 3.5 ¥{UPSTREAM_FEN_1K / 100:.2f}/张（1K/2K）、"
        f"¥{UPSTREAM_FEN_4K / 100:.2f}/张（4K）×{MULTIPLIER}"
    )


def print_economics() -> None:
    sell = Decimal(SELL_FEN_PER_IMAGE) / 100
    print(f"渠道「{CHANNEL_NAME}」{CHANNEL_BASE_URL}（协议 {PROTOCOL}，最大并发 {CONCURRENCY_LIMIT}）")
    print(f"  模型 {DISPLAY_NAME}（{MODEL_KEY}）")
    for label, upstream_fen in (("1K/2K", UPSTREAM_FEN_1K), ("4K", UPSTREAM_FEN_4K)):
        upstream = Decimal(upstream_fen) / 100
        margin = 1 - upstream / sell
        print(f"  {label:<5} 售价 {sell:.2f} 元/张 · 上游 {upstream:.2f} 元/张 · 毛利 {margin * 100:.0f}%")
    print(f"  尺寸 {len(SIZES)} 个精确像素（1K/2K/4K 各 10 个比例）；参考图上限 {MAX_IMAGES} 张")


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
    """能力配置也要比：只比"模型在不在"，改了图片上限这类参数重跑脚本会被当成无变更。"""
    for field in ("providerModelKey", "displayName", "capability", "protocol"):
        if str(existing.get(field) or "") != str(payload[field]):
            return True
    if existing.get("enabled") is not True:
        return True
    return existing.get("capabilityConfig") != payload["capabilityConfig"]


def price_drift(existing: dict) -> bool:
    return (
        existing.get("unit") != UNIT
        or existing.get("upstreamUnitPrice") != UPSTREAM_FEN_1K
        or existing.get("sellUnitPrice") is not None
        or existing.get("multiplierBp") != MULTIPLIER_BP
        or existing.get("enabled") is not True
    )


def find_channel(base_url: str, cookie: str) -> dict | None:
    page = request("GET", base_url, "/admin/channels?pageSize=200", cookie)
    for channel in page.get("channels") or []:
        if str(channel.get("name") or "").strip() == CHANNEL_NAME:
            return channel
    return None


def main() -> int:
    parser = argparse.ArgumentParser(description="上架腾讯混元生图 3.5（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    parser.add_argument("--api-key", default=os.environ.get("TENCENT_TOKENHUB_API_KEY", ""))
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
    existing_model = existing_models.get(MODEL_KEY)
    existing_price = price_index.get((key, CAPABILITY, "")) if channel_id else None
    if channel_id:
        if existing_model is None:
            plan.append(f"上架渠道模型 {DISPLAY_NAME}（{CAPABILITY}）")
        elif model_drift(existing_model, payload):
            plan.append(f"更新渠道模型 {DISPLAY_NAME}（能力配置或上游标识有变化）")
        if existing_price is None:
            plan.append(f"新增价目 {key}：{SELL_FEN_PER_IMAGE} 分/张")
        elif price_drift(existing_price):
            plan.append(f"更新价目 {key}：{SELL_FEN_PER_IMAGE} 分/张（上游 {UPSTREAM_FEN_1K} 分/张 ×{MULTIPLIER}）")

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
        print("缺少上游密钥：请设置 TENCENT_TOKENHUB_API_KEY", file=sys.stderr)
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
    row = {
        "modelKey": price_key(MODEL_KEY, channel_id),
        "capability": CAPABILITY,
        "priceTier": "",
        "unit": UNIT,
        "vendorCode": VENDOR_CODE,
        "upstreamUnitPrice": UPSTREAM_FEN_1K,
        "sellUnitPrice": None,
        "multiplier": MULTIPLIER,
        "enabled": True,
        "note": note_text(),
    }
    current = price_index.get((row["modelKey"], CAPABILITY, ""))
    if current is None:
        request("POST", args.base_url, "/admin/billing/model-prices", cookie, row)
    elif price_drift(current):
        request("PUT", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(current.get("id"))), cookie, row)

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
