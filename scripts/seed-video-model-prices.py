#!/usr/bin/env python3
"""把视频模型 Seedance 2.0 / 2.5 的按秒价写进定价表。

这份价目是产品决策，不是默认值，所以和 seed-image-model-prices.py、seed-text-model-prices.py
一样做成"可重放"的：价目变了就改下面的常量，重跑一遍，而不是靠谁记得当初在后台点过什么。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/seed-video-model-prices.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-video-model-prices.py --apply

## 用户按秒付，上游按条收

售价定 **¥0.3/秒**，所以价目行的单位是 `SECOND`，秒数由提交时的 `videoSeconds` 决定：
用户选 15 秒就扣 15 秒的钱，选 5 秒就扣 5 秒。这个值在任务创建阶段一定会被填上——
用户的选择，或模型声明的默认时长（见 app/task_creation.go 的 applyChannelCapabilityDefaults），
所以按秒计费不需要为"取不到时长"留兜底分支。

上游（插件 aigenvideo-seedance）是**按条**结算：2.0 与 2.5 同价，一条 ¥5，与生成多少秒
无关。成本是常数、收入随时长线性涨，这个结构决定了每一档的毛利都不一样：

| 模型 | 时长 | 收入 | 上游成本 | 每条 |
| --- | --- | --- | --- | --- |
| Seedance 2.0 | 5 秒 | ¥1.5 | ¥5 | **-¥3.5** |
| Seedance 2.0 | 10 秒 | ¥3 | ¥5 | -¥2 |
| Seedance 2.0 | 15 秒 | ¥4.5 | ¥5 | -¥0.5 |
| Seedance 2.5 | 30 秒 | ¥9 | ¥5 | +¥4 |

即 2.0 的三档全在成本线以下（5 秒档最狠），2.5 固定 30 秒、是唯一有毛利的档。这是刻意的
内测福利价，用来换早期用户；但它不是"低毛利"，是**按条计的净亏**，跑量起来亏损同比例放大。
要回到保本，把 SELL_FEN_PER_SECOND 改成 100 重跑（5 秒档刚好打平），或改成 34 让 15 秒档打平。

## 为什么这一行的 upstream 是空的

上游成本只有"每条 ¥5"这个事实，而这一行的单位是秒——把 500 填进 `upstream_unit_price`
会被读成"每秒钟成本 500 分"，那是六倍于真实成本的假数字。成本口径写进 note，
售价用 `sell_unit_price` 直接落库（倍率那一级在这里没有干净的定义）。
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

# 售价：分 / 秒。30 分 = ¥0.3/秒。
SELL_FEN_PER_SECOND = 30

# 上游的结算事实：每条固定 ¥5，与时长无关。只用于把账算给你看，不落库（见文件头）。
UPSTREAM_FEN_PER_VIDEO = 500

# durations 只用于打印上面那张账，不参与写库：真实可选时长由渠道模型的能力配置决定
# （2.0 = 5/10/15，2.5 = 固定 30）。这里与它重复一份是有意的——改渠道配置忘了改这里，
# 代价只是预览表格少显示一行，而不会写错价。
MODELS: list[dict] = [
    {"modelKey": "CHANNEL_000007::seedance-2.0", "label": "Seedance 2.0", "durations": [5, 10, 15]},
    {"modelKey": "CHANNEL_000007::seedance-2.5", "label": "Seedance 2.5", "durations": [30]},
]

CAPABILITY = "VIDEO"
# 必须是 SECOND：计费侧按 `单价 × 秒数` 相乘，秒数取自提交时的 videoSeconds。
UNIT = "SECOND"
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


def note_for(model: dict) -> str:
    return (
        f"上游 ¥{Decimal(UPSTREAM_FEN_PER_VIDEO) / 100}/条（不分时长）；"
        f"按秒售价 {Decimal(SELL_FEN_PER_SECOND) / 100} 元/秒"
    )


def desired_rows() -> list[dict]:
    return [
        {
            "modelKey": model["modelKey"],
            "capability": CAPABILITY,
            "priceTier": "",
            "unit": UNIT,
            "vendorCode": VENDOR_CODE,
            # 直接定价：成本按条、售价按秒，两者之间没有干净的倍率（见文件头）。
            "upstreamUnitPrice": None,
            "sellUnitPrice": SELL_FEN_PER_SECOND,
            "multiplier": None,
            "enabled": True,
            "note": note_for(model),
        }
        for model in MODELS
    ]


def same_as_existing(row: dict, existing: dict) -> bool:
    return (
        existing.get("unit") == row["unit"]
        and existing.get("upstreamUnitPrice") is None
        and existing.get("sellUnitPrice") == row["sellUnitPrice"]
        and existing.get("enabled") is True
    )


def print_economics() -> None:
    print(
        f"上游按条结算：每条 ¥{Decimal(UPSTREAM_FEN_PER_VIDEO) / 100}，"
        f"与时长无关；售价 ¥{Decimal(SELL_FEN_PER_SECOND) / 100}/秒"
    )
    for model in MODELS:
        for seconds in model["durations"]:
            revenue = SELL_FEN_PER_SECOND * seconds
            delta = revenue - UPSTREAM_FEN_PER_VIDEO
            mark = "低于成本 " if delta < 0 else ""
            print(
                f"  {model['label']:14} {seconds:>2} 秒：收入 {revenue:>4} 分 vs 成本 "
                f"{UPSTREAM_FEN_PER_VIDEO} 分 → {mark}{delta:+d} 分（{delta / 100:+.2f} 元）"
            )


def main() -> int:
    parser = argparse.ArgumentParser(description="写入视频模型按秒价（默认只预览）")
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

    if not plan:
        print("视频价目已经是目标状态，无需变更。")
        return 0

    for action, row, existing in plan:
        if action == "create":
            print(f"新建 {row['modelKey']}：单位 {row['unit']}，售价 {row['sellUnitPrice']} 分/秒")
        else:
            print(
                f"更新 {row['modelKey']}：单位 {existing.get('unit')} → {row['unit']}，"
                f"售价 {existing.get('sellUnitPrice')} → {row['sellUnitPrice']} 分/秒"
                f"（上游成本与倍率会被清空，见文件头说明）"
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
