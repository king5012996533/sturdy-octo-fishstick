#!/usr/bin/env python3
"""把在售图片模型的质量档价写进定价表。

覆盖 openai/gpt-image-2、openai/gpt-image-2.5-sunburst、openai/gpt-image-2.5-flare
与 google/imagen-4 / imagen-4-fast。这份价目是产品决策，不是默认值，所以和
pin-model-catalog.py、seed-text-model-prices.py 一样做成"可重放"的：价目变了就改下面的
UPSTREAM_USD_PER_IMAGE，重跑一遍，而不是靠谁记得当初在后台点过什么。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/seed-image-model-prices.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-image-model-prices.py --apply

## 为什么只按质量档，不按分辨率

上游 Replicate 对 OpenAI 图片族的价目是 low / medium / high / xhigh / max / auto 六档
（2.0 只到 high，2.5 系五档齐备），**没有尺寸维度**：同一质量档下 1:1 与 4K 同价。所以"用户选了 4K 却按最便宜的价卖"这种事在当前价目上不会成立——分辨率不参与
计价，质量档才是唯一的价格变量。google/imagen-4 系连质量维度都没有（界面上的 1k/2k
是分辨率档，不参与计价），整个模型只有"不区分档位"一行价。

价目里唯一要防的坑是空档（priceTier 为空）。它代表"面板没传质量"，上游此时按 auto
计费，所以空档成本必须填**各自模型的** auto 价：2.0 是 $0.128，而 2.5 系是 $0.25。
两份价目长得几乎一样，空档抄错就是资损，而且不会报错。

2.5 系的 xhigh（$0.25）与 max（$0.50）要求 auth.ImagePriceTiers 与能力合同同步扩档，
两处都已放开；2.0 上游没有这两档，能力合同按 base 前缀区分，所以它的价目里也不写这两行
（写了会在面板露出一个选了就报错的档位）。xhigh 与 auto 同价 $0.25，是两行不同的价，
空档不能拿 xhigh 顶替：空档代表"面板没传质量"，与用户主动选 xhigh 是两种请求。

## 为什么是「上游价 × 5」

口径与文本一致：售价 = 上游成本 × 固定倍率，倍率写进价目行，上游调价时只改上游成本
一个数，售价自动跟随。

倍率取 5 是**产品定的**，不是算出来的：视频线按走量定价、几乎不赚钱，图片线承担这套
价目的毛利。×5 对应约 80% 毛利（×2 只有 50%），三档都一样：

| 模型 | 档位 | 上游成本 | ×5 售价 | 毛利 |
| --- | --- | --- | --- | --- |
| gpt-image-2 / 2.5 系 | low | $0.012 | 45 分（¥0.45） | 80.8% |
| gpt-image-2 / 2.5 系 | medium | $0.047 | 170 分（¥1.70） | 80.1% |
| gpt-image-2 / 2.5 系 | high | $0.128 | 465 分（¥4.65） | 80.2% |
| gpt-image-2.5 系 | xhigh | $0.25 | 900 分（¥9.00） | 80.0% |
| gpt-image-2.5 系 | max | $0.50 | 1800 分（¥18.00） | 80.0% |
| gpt-image-2 | 空档（auto） | $0.128 | 465 分（¥4.65） | 80.2% |
| gpt-image-2.5 系 | 空档（auto） | $0.25 | 900 分（¥9.00） | 80.0% |
| imagen-4 | 不区分档位 | $0.04 | 145 分（¥1.45） | 80.0% |
| imagen-4-fast | 不区分档位 | $0.02 | 75 分（¥0.75） | 80.0% |

调价只改这个常量，重跑脚本即可；`same_as_existing` 会把倍率变化识别成"需要更新"。

## 为什么金额向上取整到分

上游单价是美元，先乘汇率换成人民币、再乘 100 得到"分"。low 档 0.012 × 7.2 × 100 =
8.64 分，而金额列是整数分，存不下小数。这里一律向上取整（8.64 → 9，而不是四舍五入到 9
或截断到 8）：宁可多收零点几厘，也不让任何一档的售价低于上游成本。
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

# 上游 Replicate 的在售图片价目，单位：美元/张。外层键是计费用标识，内层键是价格档位。
#
# 内层顺序即写入顺序（由便宜到贵、空档最后），预览输出与幂等比较都按这个顺序复核。
# 档位取值与 auth.ImagePriceTiers 对齐：LOW / MEDIUM / HIGH / XHIGH / MAX 是质量档，
# 空串表示"这个模型不按质量分档"——imagen 系没有质量维度，只有空档一行。
UPSTREAM_USD_PER_IMAGE: dict[str, dict[str, str]] = {
    "CHANNEL_000003::openai/gpt-image-2": {
        "LOW": "0.012",
        "MEDIUM": "0.047",
        "HIGH": "0.128",
        # 空档 = 面板没选质量，上游按 auto 计费；本模型 auto 与 high 同价，故成本取 0.128。
        "": "0.128",
    },
    "CHANNEL_000003::openai/gpt-image-2.5-sunburst": {
        "LOW": "0.012",
        "MEDIUM": "0.047",
        "HIGH": "0.128",
        "XHIGH": "0.25",
        "MAX": "0.50",
        # 2.5 系 auto 是 $0.25，是 2.0 的近两倍：这条不能抄上面那一份。
        # 它与 XHIGH 同价，但语义不同（漏传质量 vs 主动选极高），两行都要留。
        "": "0.25",
    },
    "CHANNEL_000003::openai/gpt-image-2.5-flare": {
        "LOW": "0.012",
        "MEDIUM": "0.047",
        "HIGH": "0.128",
        "XHIGH": "0.25",
        "MAX": "0.50",
        # flare 与 sunburst 同价目，同样不能用 2.0 的 auto 价。
        "": "0.25",
    },
    "CHANNEL_000003::google/imagen-4": {
        # 无质量维度，上游按张计价。
        "": "0.04",
    },
    "CHANNEL_000003::google/imagen-4-fast": {
        "": "0.02",
    },
}

# 美元 → 人民币汇率。上游按美元计价，换算成人民币后再乘 100 得到分。
USD_CNY_RATE = "7.2"

# 质量档的自解释文案。空档的写法由 note_for 决定：分档模型是"漏传质量"的 auto 落点，
# 不分档模型（imagen）只有一行价，写"未指定质量"会让人以为是漏配。
TIER_NOTE = {
    "LOW": "上游 low 档",
    "MEDIUM": "上游 medium 档",
    "HIGH": "上游 high 档",
    "XHIGH": "上游 xhigh 档",
    "MAX": "上游 max 档",
}

# 售价相对上游成本的倍率：5 = ×5（约 80% 毛利）。产品定价决策，见文件头 docstring：
# 视频线走量、图片线承担毛利，所以这个数比文本价目的 ×2 高。
# 接口收的是倍数（字符串），万分比由它推出，免得"接口填 ×5、幂等比较却另写 50000"
# 两处各留一份会漂移的数字。
MULTIPLIER = "5"
MULTIPLIER_BP = int(Decimal(MULTIPLIER) * 10000)

# 单价表按"计费用标识"归属：渠道路径 + "::" + 上游模型名（与 taskChargeModelKey 同一口径）。
CAPABILITY = "IMAGE"
UNIT = "IMAGE"
VENDOR_CODE = "replicate"


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


def upstream_fen(usd: str) -> int:
    """美元/张 → 分/张，向上取整（取整理由见文件头 docstring）。"""
    fen = Decimal(usd) * Decimal(USD_CNY_RATE) * 100
    return int(fen.to_integral_value(rounding=ROUND_CEILING))


def note_for(tier: str, usd: str, quality_tiered: bool) -> str:
    """档位备注。quality_tiered 指这个模型是否有质量维度，决定空档该怎么解释。"""
    if tier == "":
        prefix = "未指定质量（上游按 auto 计费）" if quality_tiered else "不区分档位"
        return f"{prefix}·上游 ${usd}/张 ×{MULTIPLIER}"
    return f"{TIER_NOTE[tier]} ${usd}/张 ×{MULTIPLIER}"


def desired_rows() -> list[dict]:
    rows: list[dict] = []
    for model_key, tiers in UPSTREAM_USD_PER_IMAGE.items():
        # 有质量档的模型一定有 LOW：用它区分"空档是 auto 落点"与"这个模型根本不分档"。
        quality_tiered = "LOW" in tiers
        for tier, usd in tiers.items():
            rows.append(
                {
                    "modelKey": model_key,
                    "capability": CAPABILITY,
                    "priceTier": tier,
                    "unit": UNIT,
                    "vendorCode": VENDOR_CODE,
                    # 售价 = null + 倍率：售价由服务端按 MULTIPLIER 算出，
                    # 上游调价只改上游价一个数。
                    "upstreamUnitPrice": upstream_fen(usd),
                    "sellUnitPrice": None,
                    "multiplier": MULTIPLIER,
                    "enabled": True,
                    "note": note_for(tier, usd, quality_tiered),
                }
            )
    return rows


def same_as_existing(row: dict, existing: dict) -> bool:
    return (
        existing.get("unit") == row["unit"]
        and existing.get("upstreamUnitPrice") == row["upstreamUnitPrice"]
        and existing.get("sellUnitPrice") is None
        and existing.get("multiplierBp") == MULTIPLIER_BP
        and existing.get("enabled") is True
    )


def main() -> int:
    parser = argparse.ArgumentParser(description="写入在售图片模型的价目（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    args = parser.parse_args()

    cookie = args.cookie.strip()
    if not cookie:
        print("缺少管理员会话：请设置 KINO_ADMIN_COOKIE（浏览器里任意 /api/admin/* 请求的 Cookie 头）", file=sys.stderr)
        return 2

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
        print("图片价目已经是目标状态，无需变更。")
        return 0

    for action, row, existing in plan:
        # 档位名只在有档位时前置；不分档的模型由 note 说明，避免出现"不区分档位（不区分档位…）"。
        tier_label = f" · {row['priceTier']}" if row["priceTier"] else ""
        target = f"{row['modelKey']}{tier_label}（{row['note']}）"
        if action == "create":
            print(f"新建 {target}：上游 {row['upstreamUnitPrice']} 分/张")
        else:
            print(
                f"更新 {target}：上游 {existing.get('upstreamUnitPrice')} → {row['upstreamUnitPrice']} 分/张"
                f"（原有独立售价会被清掉，改由倍率计算）"
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

    print(f"\n已写入 {len(plan)} 条图片价目。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
