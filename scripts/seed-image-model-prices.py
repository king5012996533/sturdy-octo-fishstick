#!/usr/bin/env python3
"""把图片模型 openai/gpt-image-2 的质量三档价写进定价表。

这份价目是产品决策，不是默认值，所以和 pin-model-catalog.py、seed-text-model-prices.py
一样做成"可重放"的：价目变了就改下面的 UPSTREAM_USD_PER_IMAGE，重跑一遍，而不是靠谁
记得当初在后台点过什么。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/seed-image-model-prices.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-image-model-prices.py --apply

## 为什么只按质量档，不按分辨率

上游 Replicate 对 openai/gpt-image-2 的价目是 low / medium / high / auto 四档，
**没有尺寸维度**：同一质量档下 1:1 与 4K 同价。所以"用户选了 4K 却按最便宜的价卖"
这种事在当前价目上不会成立——分辨率不参与计价，质量档才是唯一的价格变量。

价目里唯一要防的坑是 auto 与 high 同价。模型配置默认传 low，一旦哪天漏传质量参数，
上游就按 auto 收，价格与 high 一致。空档（priceTier 为空）那一行存在的意义就是给
"没传质量"留一个 high 价的落点，绝不让它回落到 low——low 与 high 差 10.7 倍，
落错档就是资损，而且不会报错。

## 为什么是「上游价 × 5」

口径与文本一致：售价 = 上游成本 × 固定倍率，倍率写进价目行，上游调价时只改上游成本
一个数，售价自动跟随。

倍率取 5 是**产品定的**，不是算出来的：视频线按走量定价、几乎不赚钱，图片线承担这套
价目的毛利。×5 对应约 80% 毛利（×2 只有 50%），三档都一样：

| 档位 | 上游成本 | ×5 售价 | 毛利 |
| --- | --- | --- | --- |
| low | $0.012 | 45 分（¥0.45） | 80.8% |
| medium | $0.047 | 170 分（¥1.70） | 80.1% |
| high / auto | $0.128 | 465 分（¥4.65） | 80.2% |

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

# 上游 Replicate 对 openai/gpt-image-2 的价目，单位：美元/张。
# 只有质量档一个维度：分辨率不进价目，所以这里没有尺寸键。
UPSTREAM_USD_PER_IMAGE: dict[str, str] = {
    "LOW": "0.012",
    "MEDIUM": "0.047",
    "HIGH": "0.128",
    # 空档 = 用户没选质量，上游此时按 auto 计费，而 auto 与 high 同价，故成本取 0.128。
    "": "0.128",
}

# 美元 → 人民币汇率。上游按美元计价，换算成人民币后再乘 100 得到分。
USD_CNY_RATE = "7.2"

# 写入顺序固定（由便宜到贵，空档最后）：预览输出与幂等比较都按这个顺序复核。
TIER_ORDER = ("LOW", "MEDIUM", "HIGH", "")

# 每档的自解释文案。空档必须点明它与 high 同价的原因，否则后人看到它会以为是重复配置。
TIER_NOTE = {
    "LOW": "上游 low 档",
    "MEDIUM": "上游 medium 档",
    "HIGH": "上游 high 档",
    "": "未指定质量（上游按 auto 计费，与 high 同价）",
}

# 售价相对上游成本的倍率：5 = ×5（约 80% 毛利）。产品定价决策，见文件头 docstring：
# 视频线走量、图片线承担毛利，所以这个数比文本价目的 ×2 高。
# 接口收的是倍数（字符串），万分比由它推出，免得"接口填 ×5、幂等比较却另写 50000"
# 两处各留一份会漂移的数字。
MULTIPLIER = "5"
MULTIPLIER_BP = int(Decimal(MULTIPLIER) * 10000)

# 计费用标识 = 渠道路径 + "::" + 上游模型名（与 taskChargeModelKey 同一口径）。
MODEL_KEY = "CHANNEL_000003::openai/gpt-image-2"
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


def note_for(tier: str, usd: str) -> str:
    if tier == "":
        return f"{TIER_NOTE[tier]}·上游 ${usd}/张 ×{MULTIPLIER}"
    return f"{TIER_NOTE[tier]} ${usd}/张 ×{MULTIPLIER}"


def desired_rows() -> list[dict]:
    rows: list[dict] = []
    for tier in TIER_ORDER:
        usd = UPSTREAM_USD_PER_IMAGE[tier]
        rows.append(
            {
                "modelKey": MODEL_KEY,
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
                "note": note_for(tier, usd),
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
    parser = argparse.ArgumentParser(description="写入图片模型质量三档价（默认只预览）")
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
        tier_label = row["priceTier"] or "不区分"
        target = f"{row['modelKey']} · {tier_label}（{TIER_NOTE[row['priceTier']]}）"
        if action == "create":
            print(f"新建 {target}：上游 {row['upstreamUnitPrice']} 分/张，倍率 ×{MULTIPLIER}")
        else:
            print(
                f"更新 {target}：上游 {existing.get('upstreamUnitPrice')} → {row['upstreamUnitPrice']} 分/张，"
                f"倍率 ×{MULTIPLIER}（原有独立售价会被清掉，改由倍率计算）"
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
