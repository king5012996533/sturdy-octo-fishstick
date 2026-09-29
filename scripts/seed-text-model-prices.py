#!/usr/bin/env python3
"""把文本模型的三档 token 价按「官方高峰价 × 2」写进定价表。

这份价目是产品决策，不是默认值，所以和 pin-model-catalog.py 一样做成"可重放"的：
价目变了就改下面的 PEAK_PRICE_FEN，重跑一遍，而不是靠谁记得当初在后台点过什么。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/seed-text-model-prices.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-text-model-prices.py --apply

## 为什么是「高峰价 × 2」

上游按峰谷两档计价，空闲价恒为高峰价的一半（DeepSeek：北京时间周一至周五
9:00-12:00、14:00-18:00 为高峰，其余时段含周末与法定节假日为空闲）。

售价锚在**高峰价**上乘 2，好处是任何时段都不亏，而用户看到的价格恒定：
高峰毛利 50%，空闲时段成本减半、售价不动，毛利 75%。反过来若让售价跟着峰谷浮动，
Agent 的一次运行横跨 9:00 边界时就没有干净的口径可解释。

## 为什么单位是「分 / 百万 token」

官方最便宜的一档是 0.02 元/百万 token。换成"分/千 token"是 0.02 分——金额列是整数分，
存不下，只能向上取整成 1 分，那等于把 ¥0.02 按 ¥10 卖。用百万做分母后 0.02 元 = 2 分，
全部档位都能原样落库。

## 为什么不直接填售价

上游单价 + 模型专属倍率（20000）两条一起写，售价由服务端按倍率算出来。
上游调价时只要改上游价一个数，售价自动跟随；直接把售价写死会在下一次调价时
留下一个没人记得住的偏差。

价目行里的档位字段叫 priceTier（该字段曾专指 token 档位，现已泛化成"同一模型同一能力
下的价格档位"）。文本用它区分缓存 / 输入 / 输出，图片则用它区分上游的 quality 档位。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

# 官方高峰价，单位：分 / 百万 token。（0.04 元 = 4 分）
# 换算口径：元/百万 token × 100 = 分/百万 token。
PEAK_PRICE_FEN: dict[str, dict[str, int]] = {
    "deepseek-flash": {"CACHE": 4, "INPUT": 200, "OUTPUT": 800},
    "deepseek-v4-pro": {"CACHE": 30, "INPUT": 900, "OUTPUT": 2700},
}

# 售价相对高峰成本的倍率：20000 = ×2。倍率写在这里而不是"把售价乘好再填"，
# 是为了让"上游调价后售价自动跟随"这件事继续成立。
MULTIPLIER = "2"

CAPABILITY = "TEXT"
UNIT = "TOKEN_1M"
VENDOR_CODE = "deepseek"
TIER_NOTE = {
    "CACHE": "输入·缓存命中",
    "INPUT": "输入·缓存未命中",
    "OUTPUT": "输出",
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


def desired_rows() -> list[dict]:
    rows: list[dict] = []
    for model_key, tiers in PEAK_PRICE_FEN.items():
        for tier in ("CACHE", "INPUT", "OUTPUT"):
            upstream = tiers[tier]
            rows.append(
                {
                    "modelKey": model_key,
                    "capability": CAPABILITY,
                    "priceTier": tier,
                    "unit": UNIT,
                    "vendorCode": VENDOR_CODE,
                    # 直接用售价 = null + 倍率：售价由服务端算出（上游价 × 2），
                    # 上游调价只改上游价一个数。
                    "upstreamUnitPrice": upstream,
                    "sellUnitPrice": None,
                    "multiplier": MULTIPLIER,
                    "enabled": True,
                    "note": f"{TIER_NOTE[tier]}·高峰价×{MULTIPLIER}（官方价目，空闲时段成本减半）",
                }
            )
    return rows


def same_as_existing(row: dict, existing: dict) -> bool:
    return (
        existing.get("unit") == row["unit"]
        and existing.get("upstreamUnitPrice") == row["upstreamUnitPrice"]
        and existing.get("sellUnitPrice") is None
        and existing.get("multiplierBp") == 20000
        and existing.get("enabled") is True
    )


def main() -> int:
    parser = argparse.ArgumentParser(description="写入文本模型三档 token 价（默认只预览）")
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
        print("文本价目已经是目标状态，无需变更。")
        return 0

    for action, row, existing in plan:
        target = f"{row['modelKey']} · {row['priceTier']}（{TIER_NOTE[row['priceTier']]}）"
        if action == "create":
            print(f"新建 {target}：上游 {row['upstreamUnitPrice']} 分/百万 token，倍率 ×{MULTIPLIER}")
        else:
            print(
                f"更新 {target}：上游 {existing.get('upstreamUnitPrice')} → {row['upstreamUnitPrice']} 分/百万 token，"
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

    print(f"\n已写入 {len(plan)} 条文本价目。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
