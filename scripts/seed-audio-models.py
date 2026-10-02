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
| 配乐 minimax/music-2.5 | ¥3.00/次 | $0.15/条（约 ¥1.07） | 约 +¥1.93 |
| 配乐 lucataco/ace-step 短档 | ¥0.60/次 | 15 秒约 $0.0035（约 ¥0.025） | 约 +¥0.58 |
| 配乐 lucataco/ace-step 中档 | ¥1.20/次 | 60 秒约 $0.0053（约 ¥0.037） | 约 +¥1.16 |
| 配乐 lucataco/ace-step 长档 | ¥2.40/次 | 180 秒约 $0.0139（约 ¥0.10） | 约 +¥2.30 |

配音的价看起来"贵"，是因为它在成本上几乎免费：只要文案不是上万字，一次调用的成本都在
一分钱以下。定 30 分买的是"这不是一次免费调用"，不是成本加成。

## 为什么 ACE-Step 按次却要分三档

ace-step 是三档里唯一能按秒指定时长的，而三档的上游成本差得极小（15 秒与 3 分钟只差
约 7 分钱），所以分档不是成本加成，是定价策略：单价一刀切等于鼓励所有人选最长档——
同样收 60 分没人会选 15 秒，每条的实际收入被锁死在最短档的水平上。

档位边界在代码里（backend/internal/app/audio_price_tier.go）：≤30 秒是短档、≤90 秒是
中档、更长是长档，正好把前台的 15/30/60/90/120/180 六档劈成 2/2/2。除三档外还要配一行
「不区分」兜底：客户端没带上时长时上游会按缺省产出 60 秒，那一行按中档价填，否则这批
请求会被按短档少收一半。

配音与 music-2.5 没有时长维度（时长由文本或上游决定），服务端也不会收到时长参数，
所以它们只需配「不区分」一行。

## 为什么两条音乐线路并存

minimax/music-2.5 的出曲时长由歌词与编排决定，实测同一份输入连跑三次拿到 86 / 69 / 60 秒，
做不出"给我 30 秒"这件事；ace-step 能按秒指定（1–240 秒），但它是 Replicate 的社区模型，
人声质量不如 MiniMax。短视频配乐要的是可控时长，整首歌要的是质量，所以两条都留。

ace-step 没有默认版本，Replicate 只接受 /v1/predictions + version 创建预测，版本号固定在
插件里（见 plugin-packages/generate-catalog.mjs 的 ACE_STEP_VERSION）；上游换版本要改插件并发版，
或者临时用 providerOptions.version 覆盖。
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

# 售价：分 / 次。30 分 = ¥0.30，300 分 = ¥3.00，60 分 = ¥0.60。
SPEECH_SELL_FEN_PER_REQUEST = 30
MUSIC_SELL_FEN_PER_REQUEST = 300

# ace-step 的三档时长价（tier, 分/次, 前台档位名），边界见 audio_price_tier.go：
# 短 ≤30 秒、中 ≤90 秒、长 >90 秒。改价改这里再重跑，不用发版。
ACE_STEP_TIERS: list[tuple[str, int, str]] = [
    ("SHORT", 60, "短 ≤30 秒"),
    ("MEDIUM", 120, "中 ≤90 秒"),
    ("LONG", 240, "长 >90 秒"),
]

# 兜底价按中档：客户端没带时长（旧前端、参数被清洗）时上游按缺省产出 60 秒，
# 按短档兜底等于给这批请求打对折。
ACE_STEP_FALLBACK_SELL_FEN = 120

ACE_STEP_UPSTREAM = "上游按 L40S 算力计费：15 秒约 $0.0035、3 分钟约 $0.0139；可指定 1–240 秒，空歌词即纯器乐"

MODELS: list[dict] = [
    {
        "modelKey": "minimax/speech-2.8-turbo",
        "displayName": "MiniMax Speech 2.8 Turbo 配音",
        "prices": [
            {"tier": "", "sellFen": SPEECH_SELL_FEN_PER_REQUEST, "upstream": "上游 $0.06/千 input token，随文案长度变化"},
        ],
    },
    {
        "modelKey": "minimax/music-2.5",
        "displayName": "MiniMax Music 2.5 配乐",
        "prices": [
            {"tier": "", "sellFen": MUSIC_SELL_FEN_PER_REQUEST, "upstream": "上游 $0.15/条，与时长无关（最长约 5 分钟）"},
        ],
    },
    {
        "modelKey": "lucataco/ace-step",
        "displayName": "ACE-Step 配乐（可选时长）",
        "prices": [
            {"tier": tier, "sellFen": fen, "label": label, "upstream": ACE_STEP_UPSTREAM}
            for tier, fen, label in ACE_STEP_TIERS
        ]
        + [
            {
                "tier": "",
                "sellFen": ACE_STEP_FALLBACK_SELL_FEN,
                "label": "不区分（兜底）",
                "upstream": ACE_STEP_UPSTREAM + "；客户端没带时长时上游按 60 秒产出",
            },
        ],
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


def note_for(price: dict) -> str:
    return f"{price['upstream']}；按次售价 {Decimal(price['sellFen']) / 100} 元/次"


def tier_label(price: dict) -> str:
    return price.get("label") or "不区分"


def print_economics() -> None:
    print(f"渠道 {CHANNEL_ID}：上架 {len(MODELS)} 个音频模型，按次计价（{UNIT}）")
    for model in MODELS:
        print(f"  {model['displayName']}")
        for price in model["prices"]:
            print(f"    {tier_label(price):>12}  {Decimal(price['sellFen']) / 100:>5.2f} 元/次  ·  {price['upstream']}")


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

    # 计划项带上价目行：同一模型可能有多档，只记模型就分不清这一项要写哪一行。
    plan: list[tuple[str, dict, dict | None]] = []
    for model in MODELS:
        if model["modelKey"] not in model_index:
            plan.append(("create-model", model, None))
        for price in model["prices"]:
            key = (price_key(model["modelKey"]), CAPABILITY, price["tier"])
            existing = price_index.get(key)
            if existing is None:
                plan.append(("create-price", model, price))
            elif (
                existing.get("unit") != UNIT
                or existing.get("sellUnitPrice") != price["sellFen"]
                or existing.get("enabled") is not True
            ):
                plan.append(("update-price", model, price))

    if not plan:
        print("音频模型与价目已经是目标状态，无需变更。")
        return 0

    for action, model, price in plan:
        row_name = f"{price_key(model['modelKey'])}[{tier_label(price) if price else ''}]"
        if action == "create-model":
            print(f"上架渠道模型 {CHANNEL_ID}::{model['modelKey']}（{CAPABILITY} / {PROTOCOL}）")
        elif action == "create-price":
            print(f"新增价目 {row_name}：{price['sellFen']} 分/次")
        else:
            existing = price_index[(price_key(model["modelKey"]), CAPABILITY, price["tier"])]
            print(
                f"更新价目 {row_name}：单位 {existing.get('unit')} → {UNIT}，"
                f"售价 {existing.get('sellUnitPrice')} → {price['sellFen']} 分/次"
            )

    if not args.apply:
        print(f"\n预览结束：{len(plan)} 项待写入。加 --apply 才会真正写库。")
        return 0

    for action, model, price in plan:
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

        assert price is not None
        row = {
            "modelKey": price_key(model["modelKey"]),
            "capability": CAPABILITY,
            "priceTier": price["tier"],
            "unit": UNIT,
            "vendorCode": VENDOR_CODE,
            # 两个价格口径在这里没有干净的倍率关系：配音按 token、配乐按条，所以我们
            # 直接给售价，倍率留空（倍率只用于展示"相对成本加了几个点"）。
            "upstreamUnitPrice": None,
            "sellUnitPrice": price["sellFen"],
            "multiplier": None,
            "enabled": True,
            "note": note_for(price),
        }
        if action == "create-price":
            request("POST", args.base_url, "/admin/billing/model-prices", cookie, row)
        else:
            existing = price_index[(price_key(model["modelKey"]), CAPABILITY, price["tier"])]
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
