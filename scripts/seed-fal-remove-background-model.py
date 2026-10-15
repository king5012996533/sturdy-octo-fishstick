#!/usr/bin/env python3
"""上架 fal.ai 的抠图端点（`fal-ai/birefnet/v2`）。

这条端点在 fal 目录里是一个独立 app，不是"某个生图模型的 /edit"：请求只有一个 `image_url`，
没有提示词、没有比例、没有质量档，输出是分辨率不变、背景透明（带 alpha 通道）的 PNG。
因此它在渠道上是一条普通图片模型（要有能力合同、要有价目，路由与计费都走通用链路），
但**不该出现在模型下拉里**：用户在面板上选中它、手里却没有参考图，只会得到一次必然失败的
生成。这层区分由能力合同的 `image.toolOnly` 表达，前端据此把它从可选模型里摘掉，渠道目录
里照常下发。

为什么另起脚本而不是并进 seed-fal-upscale-model.py：那个脚本管的是 AI 超分（协议 `fal-upscale`、
4× 放大、调用方必须重编码）。抠图是另一条端点、另一份能力合同（不改尺寸、不做有损重编码），
挤进一个脚本只会让两条功能互相污染默认值。仓库里每个模型族一个 seed 脚本是既有约定。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/seed-fal-remove-background-model.py

    KINO_ADMIN_COOKIE='...' \\
    KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-fal-remove-background-model.py --apply

渠道（`fal.ai · 主账号`，`https://fal.run`）由 seed-fal-image-models.py 负责创建并持有密钥，
本脚本只往这个渠道上加一条模型和一份价目，不改渠道本身，也就不需要上游密钥。

## 定价

与超分不同，上游按**算力秒**计费：fal 模型页记的是 $0.000575/秒，一张图多少钱取决于分辨率。
2026-10-16 实测（同一个 fal 账号，走 data URL 内联输入）：

  · 1024² 输入 → 端到端 5.7 秒，输出 1024² 带 alpha PNG 0.58 MB；
  · 4096×2731 输入 → 端到端 7.4 秒，输出同尺寸带 alpha PNG 2.74 MB。

端到端时间里含着排队，算力秒拿不到（用量接口要管理员权限，当前 key 是 403），所以这里按
一张 5 秒算力保守估成本：$0.000575 × 5 ≈ $0.0029 → 按 7.2 折算 2.09 分，向上取整 2 分；
`UPSTREAM_USD` 记 0.003 美元（2.16 分，取整后 3 分）作为上限口径——宁可多算一分成本，
也不因为估低了把毛利算虚。

售价 10 积分/张（1 积分 = 1 分）：用户花 0.1 元拿到一张抠好的透明图，即使按上限口径成本
也有 70% 毛利。定价不跟随上游成本自动算——抠图是画布上的一次动作，用户按"一张 1 毛"来
理解它；上游调价时改 UPSTREAM_USD 一个数就能从预览里看到新毛利。
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

# 与 seed-fal-image-models.py 同一套渠道坐标，本脚本不创建渠道。
CHANNEL_NAME = "fal.ai · 主账号"
CHANNEL_BASE_URL = "https://fal.run"

PROTOCOL = "fal-remove-background"

# 渠道模型标识取上游端点名：价目行按"渠道 + 模型标识"归属，与上游对得上，对账时不用再翻一层映射。
MODEL_KEY = "fal-ai/birefnet/v2"
DISPLAY_NAME = "移除背景"
# 插件里写死的创建路径。它必须与 MODEL_KEY 指向同一条端点，否则价目挂在一个走不通的模型上。
ENDPOINT_PATH = "/fal-ai/birefnet/v2"

CAPABILITY = "IMAGE"
# 按张计价：画布一次抠图就是一张图，用量取自请求的 count（抠图固定为 1）。
# 不用 REQUEST：那类价目的账单算式会省掉用量，一旦有别的入口带着 count>1 提交过来，
# 扣费金额与账单上写出来的算式就对不上了。
UNIT = "IMAGE"
VENDOR_CODE = "fal"

# 与其他 seed 脚本同一个汇率，否则同一条图片线会出现两套换算。
USD_CNY_RATE = "7.2"
UPSTREAM_USD = "0.003"
SELL_FEN = 10

MAX_IMAGE_BYTES = 20 * 1024 * 1024
PROMPT_MAX_CHARS = 32000
MAX_IMAGES = 1
MAX_OUTPUTS = 1


def capability_config() -> dict:
    """抠图的能力合同：没有可调参数，只有"必须有一张原图"和"固定出 1 张"。

    比例、质量档、自定义尺寸一律关掉——上游 BiRefNet 不认这些字段，开着它们在面板上等于
    给用户一组拨了不生效的旋钮。`transparentBackground` 也保持关闭：它描述的是"能不能把
    透明底作为参数发给上游"，而这条端点**永远**返回带 alpha 的图，不存在可调项。
    `toolOnly` 让它不进入模型下拉（见模块文档）。
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
            # default 写 "auto" 而不是空串：接口落库时会把空串规范化成 "auto"，声明空串会让
            # 每次复跑都比出"能力配置有变化"、白白多发一次 PUT（2026-10-16 线上实测）。
            # 这里声明的就是存储层最终保留的那个值。
            "size": {"parameter": "none", "values": [], "default": "auto", "allowCustom": False},
            "quality": {"supported": False, "values": [], "default": "auto"},
            "transparentBackground": {"supported": False, "default": False},
            "responseFormat": {"supported": False},
            "outputFormat": {"supported": False},
            "maxOutputs": MAX_OUTPUTS,
            "toolOnly": True,
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


def upstream_fen(usd: str = UPSTREAM_USD) -> int:
    """美元/张 → 分/张，向上取整：宁可多算一分成本，也不因为截断把毛利算虚。"""
    return int((Decimal(usd) * Decimal(USD_CNY_RATE) * 100).to_integral_value(rounding=ROUND_CEILING))


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
    """能力配置也要比：只比"模型在不在"，改了 toolOnly 这类开关重跑脚本会被当成无变更。"""
    for field in ("providerModelKey", "displayName", "capability", "protocol"):
        if str(existing.get(field) or "") != str(payload[field]):
            return True
    if existing.get("enabled") is not True:
        return True
    return existing.get("capabilityConfig") != payload["capabilityConfig"]


def price_payload(channel_id: str) -> dict:
    return {
        "modelKey": price_key(MODEL_KEY, channel_id),
        "capability": CAPABILITY,
        # 抠图没有质量档，价目只有"不区分档位"这一行。
        "priceTier": "",
        "unit": UNIT,
        "vendorCode": VENDOR_CODE,
        # 上游成本照实记：运营改用户价时不用动成本，毛利跟着重算。
        "upstreamUnitPrice": upstream_fen(),
        "sellUnitPrice": SELL_FEN,
        "enabled": True,
        "note": f"移除背景固定价（BiRefNet v2，分辨率不变，输出带 alpha PNG）· 上游 fal ${UPSTREAM_USD}/张（按算力秒估的上限口径）",
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


def print_economics() -> None:
    cost = Decimal(upstream_fen()) / 100
    sell = Decimal(SELL_FEN) / 100
    print(f"渠道「{CHANNEL_NAME}」{CHANNEL_BASE_URL}（协议 {PROTOCOL}）")
    print(f"  模型 {DISPLAY_NAME}（{MODEL_KEY}）")
    print(f"    POST {ENDPOINT_PATH}；同步端点，一次请求阻塞到出图；分辨率不变，无参数")
    print(f"    售价 {sell:.2f} 元/张 · 上游按算力秒计费，按一张估 ${UPSTREAM_USD}（{cost:.2f} 元）· 毛利 {1 - cost / sell:.0%}")
    print("    能力合同：只收 1 张原图、固定出 1 张、无比例/质量档，且 toolOnly（不进模型下拉）")


def main() -> int:
    parser = argparse.ArgumentParser(description="上架 fal.ai 抠图端点（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    args = parser.parse_args()

    cookie = args.cookie.strip()
    if not cookie:
        print("缺少管理员会话：请设置 KINO_ADMIN_COOKIE（浏览器里任意 /api/admin/* 请求的 Cookie 头）", file=sys.stderr)
        return 2

    print_economics()

    channel = find_channel(args.base_url, cookie)
    if channel is None:
        print(f"\n未找到渠道「{CHANNEL_NAME}」：请先跑 seed-fal-image-models.py 建渠道（它带上游密钥）。", file=sys.stderr)
        return 1
    channel_id = str(channel.get("id") or "")
    if not channel_id:
        print("渠道 ID 缺失，无法继续。", file=sys.stderr)
        return 1

    existing_models = read_models(args.base_url, cookie, channel_id)
    prices = read_prices(args.base_url, cookie)
    payload = model_payload()
    price_row = price_payload(channel_id)
    existing_model = existing_models.get(MODEL_KEY)
    current_price = next(
        (
            row
            for row in prices
            if row.get("modelKey") == price_row["modelKey"] and row.get("capability") == CAPABILITY and (row.get("priceTier") or "") == ""
        ),
        None,
    )

    plan: list[str] = []
    if existing_model is None:
        plan.append(f"上架渠道模型 {DISPLAY_NAME}（{CAPABILITY}）")
    elif model_drift(existing_model, payload):
        plan.append(f"更新渠道模型 {DISPLAY_NAME}（能力配置或上游标识有变化）")
    if current_price is None:
        plan.append(f"新增价目 {price_row['modelKey']}：{SELL_FEN} 分/张（上游 {upstream_fen()} 分/张）")
    elif price_drift(current_price, price_row):
        plan.append(f"更新价目 {price_row['modelKey']}：{SELL_FEN} 分/张（上游 {upstream_fen()} 分/张）")

    if not plan:
        print("\n渠道模型与价目已经是目标状态，无需变更。")
        return 0

    for line in plan:
        print(" ·", line)

    if not args.apply:
        print(f"\n预览结束：{len(plan)} 项待写入。加 --apply 才会真正写库。")
        return 0

    if existing_model is None:
        request("POST", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie, payload)
    elif model_drift(existing_model, payload):
        request(
            "PUT",
            args.base_url,
            f"/admin/channels/{urllib.parse.quote(channel_id)}/models/{urllib.parse.quote(str(existing_model.get('id') or MODEL_KEY))}",
            cookie,
            payload,
        )

    if current_price is None:
        request("POST", args.base_url, "/admin/billing/model-prices", cookie, price_row)
    elif price_drift(current_price, price_row):
        request("PUT", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(current_price.get("id"))), cookie, price_row)

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
