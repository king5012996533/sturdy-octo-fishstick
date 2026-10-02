#!/usr/bin/env python3
"""上架 Replicate 音频模型，并按次定价。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/seed-audio-models.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-audio-models.py --apply

和 seed-image-model-prices.py、seed-text-model-prices.py、seed-video-model-prices.py 一样，
价目是产品决策而不是默认值，所以做成可重放的：改下面的常量、重跑，而不是靠谁记得当初在
后台点过什么。

## 为什么这份脚本同时管"上架"和"定价"

另外三份只写价，因为模型早就上架了。音频不一样：线上一条音频渠道模型都没有，只写价等于
给不存在的模型配价；只上架不配价，用户一提交就吃 409「尚未定价」。这两步拆到两处，下一个
人必定只做一半，所以合成一条命令。

## 为什么按次，不按秒

上游的成本结构本身就是按次的：配乐 $0.15/条，与生成多少秒无关；配音 $0.06/千 input token，
取决于文本长度而不是音频时长。而"时长"这件事在提交那一刻并不存在——配音时长由文本决定、
配乐时长由上游决定，都要等产出才知道。计费侧因此固定收一次（见 app/taskChargeQuantity），
定价单位也只能是 REQUEST；配成 SECOND 会被服务端拒掉，因为那会把"30 分/次 × 1"写成
"30 分/秒 × 1"，数字对而说法错，用户按账单上的算式复核不出来。

## 毛利

| 模型 | 售价 | 上游成本 | 每条 |
| --- | --- | --- | --- |
| 配音 minimax/speech-2.8-turbo | ¥0.30/次 | $0.06/千 token（30 字文案约 $0.002） | 约 +¥0.28 |
| 配乐 minimax/music-2.5 | ¥2.00/次 | $0.15/条（约 ¥1.07） | 约 +¥0.93 |

配音的价看起来"贵"，是因为它在成本上几乎免费：只要文案不是上万字，一次调用的成本都在
一分钱以下。定 30 分买的是"这不是一次免费调用"，不是成本加成。
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

# 渠道：Replicate · 主账号。音频模型挂在同一个渠道下，key 复用它的凭据。
CHANNEL_ID = "CHANNEL_000003"

# 售价：分 / 次。30 分 = ¥0.30，200 分 = ¥2.00。
SPEECH_SELL_FEN_PER_REQUEST = 30
MUSIC_SELL_FEN_PER_REQUEST = 200

MODELS: list[dict] = [
    {
        "modelKey": "minimax/speech-2.8-turbo",
        "displayName": "MiniMax Speech 2.8 Turbo 配音",
        "sellFen": SPEECH_SELL_FEN_PER_REQUEST,
        "upstream": "上游 $0.06/千 input token，随文案长度变化",
    },
    {
        "modelKey": "minimax/music-2.5",
        "displayName": "MiniMax Music 2.5 配乐",
        "sellFen": MUSIC_SELL_FEN_PER_REQUEST,
        "upstream": "上游 $0.15/条，与时长无关（最长约 5 分钟）",
    },
]

CAPABILITY = "AUDIO"
# 必须是 REQUEST：服务端只接受音频按次，见文件头。
UNIT = "REQUEST"
PROTOCOL = "replicate-prediction-audio"
VENDOR_CODE = "replicate-prediction-audio"


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


def price_key(model_key: str) -> str:
    """定价表里的模型标识带渠道前缀，与前台结算时用的 key 一致。"""
    return f"{CHANNEL_ID}::{model_key}"


def note_for(model: dict) -> str:
    return f"{model['upstream']}；按次售价 {Decimal(model['sellFen']) / 100} 元/次"


def print_economics() -> None:
    print(f"渠道 {CHANNEL_ID}：上架 {len(MODELS)} 个音频模型，按次计价（{UNIT}）")
    for model in MODELS:
        print(
            f"  {model['displayName']:28} {Decimal(model['sellFen']) / 100:>5.2f} 元/次"
            f"  ·  {model['upstream']}"
        )


def main() -> int:
    parser = argparse.ArgumentParser(description="上架 Replicate 音频模型并按次定价（默认只预览）")
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

    existing_models = request("GET", args.base_url, f"/admin/channels/{CHANNEL_ID}/models", cookie).get("models") or []
    model_index = {str(row.get("modelKey") or ""): row for row in existing_models}

    existing_prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {
        (row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row
        for row in existing_prices
    }

    plan: list[tuple[str, dict]] = []
    for model in MODELS:
        if model["modelKey"] not in model_index:
            plan.append(("create-model", model))
        key = (price_key(model["modelKey"]), CAPABILITY, "")
        existing = price_index.get(key)
        if existing is None:
            plan.append(("create-price", model))
        elif (
            existing.get("unit") != UNIT
            or existing.get("sellUnitPrice") != model["sellFen"]
            or existing.get("enabled") is not True
        ):
            plan.append(("update-price", model))

    if not plan:
        print("音频模型与价目已经是目标状态，无需变更。")
        return 0

    for action, model in plan:
        if action == "create-model":
            print(f"上架渠道模型 {CHANNEL_ID}::{model['modelKey']}（{CAPABILITY} / {PROTOCOL}）")
        elif action == "create-price":
            print(f"新增价目 {price_key(model['modelKey'])}：{model['sellFen']} 分/次")
        else:
            existing = price_index[(price_key(model["modelKey"]), CAPABILITY, "")]
            print(
                f"更新价目 {price_key(model['modelKey'])}：单位 {existing.get('unit')} → {UNIT}，"
                f"售价 {existing.get('sellUnitPrice')} → {model['sellFen']} 分/次"
            )

    if not args.apply:
        print(f"\n预览结束：{len(plan)} 项待写入。加 --apply 才会真正写库。")
        return 0

    for action, model in plan:
        if action == "create-model":
            request(
                "POST",
                args.base_url,
                f"/admin/channels/{CHANNEL_ID}/models",
                cookie,
                {
                    "modelKey": model["modelKey"],
                    "providerModelKey": model["modelKey"],
                    "displayName": model["displayName"],
                    "capability": CAPABILITY.lower(),
                    "protocol": PROTOCOL,
                    "enabled": True,
                },
            )
            continue

        row = {
            "modelKey": price_key(model["modelKey"]),
            "capability": CAPABILITY,
            "priceTier": "",
            "unit": UNIT,
            "vendorCode": VENDOR_CODE,
            # 两个价格口径在这里没有干净的倍率关系：配音按 token、配乐按条，所以我们
            # 直接给售价，倍率留空（倍率只用于展示"相对成本加了几个点"）。
            "upstreamUnitPrice": None,
            "sellUnitPrice": model["sellFen"],
            "multiplier": None,
            "enabled": True,
            "note": note_for(model),
        }
        if action == "create-price":
            request("POST", args.base_url, "/admin/billing/model-prices", cookie, row)
        else:
            existing = price_index[(price_key(model["modelKey"]), CAPABILITY, "")]
            request(
                "PUT",
                args.base_url,
                "/admin/billing/model-prices/" + urllib.parse.quote(str(existing["id"])),
                cookie,
                row,
            )

    print(f"\n已写入 {len(plan)} 项。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
