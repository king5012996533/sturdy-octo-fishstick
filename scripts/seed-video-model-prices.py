#!/usr/bin/env python3
"""把视频模型 Seedance 2.0 / 2.5 的按条价写进定价表。

这份价目是产品决策，不是默认值，所以和 seed-image-model-prices.py、seed-text-model-prices.py
一样做成"可重放"的：价目变了就改下面的常量，重跑一遍，而不是靠谁记得当初在后台点过什么。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/seed-video-model-prices.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-video-model-prices.py --apply

## 为什么按「条」而不是按「秒」

上游（插件 aigenvideo-seedance）对 Seedance 2.0 与 2.5 **同价，且不分时长**：一条多少钱，
跟他生成 5 秒还是 30 秒无关。成本是常数、时长是变量，两者结构不同，所以按秒定价必然错
一头——3 毛/秒的话 5 秒档只收 1.5 元，而每出一条都要付 5 元，等于最短的那一档每单亏 3.5 元，
而它恰恰是用户最常选的。

按条计价后毛利不再随时长漂移：收入与成本都是常数，一条就是一条。代价是 5 秒和 15 秒同价，
用户会倾向选最长时长——成本不变，所以这不是漏洞。

后端的 `credits = 单价 × 用量` 不变，靠「计费单位 = REQUEST 时用量恒为 1」把秒数挡在相乘
之外（见 auth/credit_task.go）。所以这张表里的单位必须是 REQUEST，写成 SECOND 会把一条
600 积分的视频乘成 9000 积分。

## 内测福利价：2.0 是低于成本的

| 模型 | 上游成本 | 售价 | 倍率 | 每条 |
| --- | --- | --- | --- | --- |
| Seedance 2.0（5/10/15 秒） | ¥5 | ¥3 | ×0.6 | **-¥2** |
| Seedance 2.5（30 秒 720P） | ¥5 | ¥6 | ×1.2 | +¥1 |

上游两档同价，所以 2.5 的差价全是毛利。2.0 定到成本以下是有意为之的内测福利，用来换早期
用户；它同时是引流主力，跑量起来后亏的绝对值会跟着涨，**这是需要盯的一条线，不是可以忘掉
的临时配置**。要回到保本，把 SELL_YUAN 改成 "5" 重跑即可。

倍率而不是售价写进价目行：上游调价时只改 UPSTREAM_YUAN_PER_VIDEO 一个数，售价按比例自动
跟随，两档之间的相对关系也不会被改歪。
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

# 上游每条的实价（元）。两个模型同一个价，且与视频时长无关。
UPSTREAM_YUAN_PER_VIDEO = "5"

# 每档的售价（元）。2.0 低于成本，见文件头 docstring。
MODELS: list[dict[str, str]] = [
    {
        "modelKey": "CHANNEL_000007::seedance-2.0",
        "label": "Seedance 2.0（5/10/15 秒）",
        "sellYuan": "3",
    },
    {
        "modelKey": "CHANNEL_000007::seedance-2.5",
        "label": "Seedance 2.5（固定 30 秒 720P）",
        "sellYuan": "6",
    },
]

CAPABILITY = "VIDEO"
# 必须是 REQUEST：按秒结算会把每条价乘成"单价 × 秒数"。理由见文件头 docstring。
UNIT = "REQUEST"
VENDOR_CODE = "aigenvideo-seedance"


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


def yuan_to_fen(value: str) -> int:
    """元 → 分。¥5 = 500 分，1 积分等于 1 分，所以这里同时就是积分。

    元转分必须是整数：金额列存不下小数，而"每条 5.5 元"这类价一旦被截断，就会在
    每一条视频上稳定地少收一点，累计起来无法追溯。除不尽直接报错，逼调用方改价。
    """
    fen = Decimal(value) * 100
    if fen != fen.to_integral_value():
        raise ValueError(f"售价/成本必须精确到分，无法表示 {value} 元")
    return int(fen)


def multiplier_text(sell_yuan: str) -> str:
    """倍率 = 售价 / 上游成本，写成后台表单收的倍数字符串。"""
    multiplier = Decimal(sell_yuan) / Decimal(UPSTREAM_YUAN_PER_VIDEO)
    return format(multiplier.normalize(), "f")


def multiplier_bp(sell_yuan: str) -> int:
    multiplier = Decimal(sell_yuan) / Decimal(UPSTREAM_YUAN_PER_VIDEO)
    return int(multiplier * 10000)


def desired_rows() -> list[dict]:
    upstream_fen = yuan_to_fen(UPSTREAM_YUAN_PER_VIDEO)
    rows: list[dict] = []
    for model in MODELS:
        sell_fen = yuan_to_fen(model["sellYuan"])
        rows.append(
            {
                "modelKey": model["modelKey"],
                "capability": CAPABILITY,
                "priceTier": "",
                "unit": UNIT,
                "vendorCode": VENDOR_CODE,
                # 售价 = null + 倍率：售价由服务端按倍率算出，上游调价只改上游价一个数。
                "upstreamUnitPrice": upstream_fen,
                "sellUnitPrice": None,
                "multiplier": multiplier_text(model["sellYuan"]),
                "enabled": True,
                "note": (
                    f"上游 ¥{UPSTREAM_YUAN_PER_VIDEO}/条（不分时长）×{multiplier_text(model['sellYuan'])}"
                    f" = ¥{model['sellYuan']}/条"
                    + ("（内测福利价，低于成本）" if sell_fen < upstream_fen else "")
                ),
            }
        )
    return rows


def same_as_existing(row: dict, existing: dict) -> bool:
    return (
        existing.get("unit") == row["unit"]
        and existing.get("upstreamUnitPrice") == row["upstreamUnitPrice"]
        and existing.get("sellUnitPrice") is None
        and existing.get("multiplierBp") == multiplier_bp(
            next(model["sellYuan"] for model in MODELS if model["modelKey"] == row["modelKey"])
        )
        and existing.get("enabled") is True
    )


def main() -> int:
    parser = argparse.ArgumentParser(description="写入视频模型按条价（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    args = parser.parse_args()

    cookie = args.cookie.strip()
    if not cookie:
        print("缺少管理员会话：请设置 KINO_ADMIN_COOKIE（浏览器里任意 /api/admin/* 请求的 Cookie 头）", file=sys.stderr)
        return 2

    upstream_fen = yuan_to_fen(UPSTREAM_YUAN_PER_VIDEO)
    existing_rows = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    index = {
        (row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row
        for row in existing_rows
    }

    plan: list[tuple[str, dict, dict | None]] = []
    for row in desired_rows():
        existing = index.get((row["modelKey"], row["capability"], row["priceTier"]))
        if existing is not None and same_as_existing(row, existing):
            continue
        plan.append(("update" if existing else "create", row, existing))

    labels = {model["modelKey"]: model["label"] for model in MODELS}
    for model in MODELS:
        sell_fen = yuan_to_fen(model["sellYuan"])
        mark = "低于成本 " if sell_fen < upstream_fen else ""
        print(
            f"{model['label']}：上游 ¥{UPSTREAM_YUAN_PER_VIDEO}/条 → 售价 ¥{model['sellYuan']}/条"
            f"（{mark}倍率 ×{multiplier_text(model['sellYuan'])}，每条 {sell_fen - upstream_fen:+d} 积分）"
        )

    if not plan:
        print("\n视频价目已经是目标状态，无需变更。")
        return 0

    print()
    for action, row, existing in plan:
        target = f"{labels.get(row['modelKey'], row['modelKey'])} · {row['modelKey']}"
        if action == "create":
            print(f"新建 {target}：上游 {row['upstreamUnitPrice']} 分/条，倍率 ×{row['multiplier']}")
        else:
            print(
                f"更新 {target}：上游 {existing.get('upstreamUnitPrice')} → {row['upstreamUnitPrice']} 分/条，"
                f"倍率 ×{row['multiplier']}（原有独立售价会被清掉，改由倍率计算）"
            )

    if not args.apply:
        print(f"\n预览结束：{len(plan)} 条待写入。加 --apply 才会真正写库。")
        return 0

    for action, row, existing in plan:
        if action == "create":
            request("POST", args.base_url, "/admin/billing/model-prices", cookie, row)
        else:
            request(
                "PUT",
                args.base_url,
                "/admin/billing/model-prices/" + urllib.parse.quote(existing["id"]),
                cookie,
                row,
            )

    print(f"\n已写入 {len(plan)} 条视频价目。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
