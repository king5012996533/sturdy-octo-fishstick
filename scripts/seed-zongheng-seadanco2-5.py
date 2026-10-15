#!/usr/bin/env python3
"""上架纵横科技 DDseadanco2.5（对外 seadanco2.5）视频模型，按条定价。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \\
        python3 scripts/seed-zongheng-seadanco2-5.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \\
        python3 scripts/seed-zongheng-seadanco2-5.py --apply

只管这个模型和它的价目：渠道「纵横科技 · 主账号」已经存在，本脚本不改它的 Base URL、并发与密钥。

## 现在的线上状态：已停用，等上游开通

2026-10-16 首次联调时上游回 `503 video_model_not_configured`（"当前模型接入配置异常，请联系管理员"），
所以模型当场停用了，先不摆给用户看。上游把我们的 key 配好之后重跑本脚本（加 `--apply`）
就会把它重新启用——停用只是运行时状态，脚本里的目标状态仍然是"上架可用"。

## 规格来自上游公开能力中枢 GET /api/models

`DDseadanco2.5`（上游 id 167，路线7）声明：720P、时长 5–30 秒、参考图 30 张、参考视频 10、
参考音频 10，描述里写着"原生不卡脸"。

两个上限在本平台上用不上，能力合同与上游不一致是有意的：

- 参考视频与参考音频：`zongheng-video` 插件在协议层直接拒绝（"该视频模型不支持参考视频"），
  所以写 0。面板上不出现的入口，比让用户配好再被拦下来好。
- 参考图 30 张照抄上游：请求体里的图会换成平台的对象地址（CANVAS_PUBLIC_BASE_URL 已配），
  张数上限交给上游判。

## 为什么单位是 REQUEST

上游按条结算：一条 ¥5，与时长、分辨率都无关（上游 `billing_unit = per_request`，
50 上游积分/条 ≈ ¥5）。单位填 REQUEST，用量恒为 1；填 SECOND 会按用户选的秒数相乘，
一条 30 秒的片子按 30 倍收——用户拉长时长反而成了资损口。

## 毛利

| 档位 | 售价 | 上游成本 | 毛利 |
| --- | --- | --- | --- |
| 720P | ¥10.00 | ¥5.00 | 50% |

空档（面板没选分辨率、或取值算不出档位）也留一行，按 720P 的价兜底：没有这一行，
`auto` 会以"未定价"把用户挡回去，而不是按贵的档收钱。
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

CHANNEL_NAME = "纵横科技 · 主账号"

# 上游模型标识：平台对外只暴露下面的 modelKey，真实标识留在渠道配置里。
UPSTREAM_MODEL = "DDseadanco2.5"

MODEL_KEY = "seadanco2.5"
DISPLAY_NAME = "seadanco2.5"

CAPABILITY = "VIDEO"
# 必须是 REQUEST：上游按条结算，用量恒为 1。
UNIT = "REQUEST"
PROTOCOL = "zongheng-video"
VENDOR_CODE = "zongheng-video"

# 上游只声明 720p，面板就只给 720p：把没验过的档位摆出来等于用没验过的成本出货。
RESOLUTIONS = ["720p"]
DEFAULT_RESOLUTION = "720p"
# 时长照上游的 5–30 秒开放，默认 30——按条计价，一条的价钱不随时长变，拉满才是这档的用法。
DURATION_MIN = 5
DURATION_MAX = 30
DURATION_DEFAULT = 30
RATIOS = ["16:9", "9:16", "1:1"]
MAX_IMAGES = 30

PRICE_TIERS: list[dict] = [
    {"tier": "720P", "sellFenPerItem": 1000, "label": "720p"},
    {"tier": "", "sellFenPerItem": 1000, "label": "未指定分辨率"},
]

# 上游单条成本（分）：50 上游积分/条 ≈ ¥5.00，与分辨率、时长都无关，只用于算毛利与备注。
UPSTREAM_FEN_PER_ITEM = 500


def capability_config() -> dict:
    """能力合同：720P 单档、时长 5–30 秒、参考视频与参考音频一律关掉。"""
    return {
        "version": 1,
        "video": {
            "references": {
                "promptMaxChars": 8000,
                "minImages": 0,
                "maxImages": MAX_IMAGES,
                "maxImageBytes": 30 * 1024 * 1024,
                # 上游给的 10 个参考视频 / 10 个参考音频本插件发不出去，见文件头。
                "maxVideos": 0,
                "maxVideoBytes": 0,
                "maxVideoDurationSeconds": 0,
                "maxAudios": 0,
                "maxAudioBytes": 0,
                "maxAudioDurationSeconds": 0,
            },
            "duration": {
                "selection": "range",
                "min": DURATION_MIN,
                "max": DURATION_MAX,
                "step": 1,
                "default": DURATION_DEFAULT,
            },
            "ratios": RATIOS,
            "defaultRatio": "16:9",
            "resolutions": RESOLUTIONS,
            "defaultResolution": DEFAULT_RESOLUTION,
            "generateAudio": {"supported": False, "default": False},
            "watermark": {"supported": False, "default": False},
            "operations": ["text_to_video", "image_to_video", "reference_to_video"],
            "defaultOperation": "text_to_video",
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


def price_key(channel_id: str) -> str:
    """定价表里的模型标识带渠道前缀，与前台结算时用的 key 一致。"""
    return f"{channel_id}::{MODEL_KEY}"


def note_for(row: dict) -> str:
    margin = 1 - Decimal(UPSTREAM_FEN_PER_ITEM) / Decimal(row["sellFenPerItem"])
    return (
        f"上游 纵横科技 {UPSTREAM_MODEL} 按条结算 ¥{UPSTREAM_FEN_PER_ITEM / 100:.2f}/条"
        f"（与分辨率、时长无关）；{row['label']} 一条 {row['sellFenPerItem']} 分"
        f"（¥{row['sellFenPerItem'] / 100:.2f}），毛利 {margin * 100:.0f}%"
    )


def print_economics() -> None:
    print(f"渠道「{CHANNEL_NAME}」（协议 {PROTOCOL}）")
    print(
        f"  模型 {DISPLAY_NAME}（上游 {UPSTREAM_MODEL}）· 分辨率 {'/'.join(RESOLUTIONS)} · "
        f"时长 {DURATION_MIN}–{DURATION_MAX} 秒（默认 {DURATION_DEFAULT}）· "
        f"画幅 {', '.join(RATIOS)} · 参考图 ≤{MAX_IMAGES} 张"
    )
    for row in PRICE_TIERS:
        margin = 1 - Decimal(UPSTREAM_FEN_PER_ITEM) / Decimal(row["sellFenPerItem"])
        print(
            f"  {row['label']:<12} {row['sellFenPerItem']:>5} 分/条 "
            f"（¥{row['sellFenPerItem'] / 100:.2f}/条，毛利 {margin * 100:.0f}%）"
        )


def model_payload() -> dict:
    return {
        "modelKey": MODEL_KEY,
        "providerModelKey": UPSTREAM_MODEL,
        "displayName": DISPLAY_NAME,
        "capability": CAPABILITY.lower(),
        "protocol": PROTOCOL,
        "enabled": True,
        "capabilityConfig": capability_config(),
    }


def model_drift_reason(existing: dict, payload: dict) -> str:
    """说清差在哪，而不是只报"有变化"。

    停用（等我上游开通）是这里的常见状态，只报一句"能力配置或上游标识有变化"会让
    "把停用的模型启用"看起来像有人改坏了能力配置。能力配置也要比：只比"模型在不在"，
    改了参考图上限这类参数重跑脚本会被当成无变更。
    """
    if existing.get("enabled") is not True:
        return "已停用 → 启用"
    for field, label in (
        ("providerModelKey", "上游标识"),
        ("displayName", "显示名"),
        ("capability", "能力"),
        ("protocol", "协议"),
    ):
        if str(existing.get(field) or "") != str(payload[field]):
            return f"{label} {existing.get(field)!r} → {payload[field]!r}"
    if existing.get("capabilityConfig") != payload["capabilityConfig"]:
        return "能力配置有变化"
    return ""


def model_drift(existing: dict, payload: dict) -> bool:
    return model_drift_reason(existing, payload) != ""


def find_channel(base_url: str, cookie: str) -> dict | None:
    page = request("GET", base_url, "/admin/channels?pageSize=200", cookie)
    for channel in page.get("channels") or []:
        if str(channel.get("name") or "").strip() == CHANNEL_NAME:
            return channel
    return None


def main() -> int:
    parser = argparse.ArgumentParser(description="上架纵横科技 DDseadanco2.5（对外 seadanco2.5）视频模型（默认只预览）")
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

    channel = find_channel(args.base_url, cookie)
    if channel is None:
        print(f"渠道「{CHANNEL_NAME}」不存在。本脚本只上架模型与价目，建渠道请在后台先建好。", file=sys.stderr)
        return 1
    channel_id = str(channel.get("id") or "")

    payload = model_payload()
    rows = request("GET", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
    existing_model = next((row for row in rows if str(row.get("modelKey") or "") == MODEL_KEY), None)

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {
        (row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row
        for row in prices
    }

    plan: list[str] = []
    if existing_model is None:
        plan.append(f"上架渠道模型 {DISPLAY_NAME}（上游 {UPSTREAM_MODEL} / {CAPABILITY} / {PROTOCOL}）")
    elif (reason := model_drift_reason(existing_model, payload)):
        plan.append(f"更新渠道模型 {DISPLAY_NAME}（{reason}）")
    for row in PRICE_TIERS:
        existing = price_index.get((price_key(channel_id), CAPABILITY, row["tier"]))
        if existing is None:
            plan.append(f"新增价目 {MODEL_KEY} [{row['label']}]：{row['sellFenPerItem']} 分/条")
        elif (
            existing.get("unit") != UNIT
            or existing.get("sellUnitPrice") != row["sellFenPerItem"]
            or existing.get("enabled") is not True
        ):
            plan.append(
                f"更新价目 {MODEL_KEY} [{row['label']}]：单位 {existing.get('unit')} → {UNIT}，"
                f"售价 {existing.get('sellUnitPrice')} → {row['sellFenPerItem']} 分/条"
            )

    if not plan:
        print("渠道模型与价目已经是目标状态，无需变更。")
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

    for row in PRICE_TIERS:
        body = {
            "modelKey": price_key(channel_id),
            "capability": CAPABILITY,
            "priceTier": row["tier"],
            "unit": UNIT,
            "vendorCode": VENDOR_CODE,
            "upstreamUnitPrice": None,
            "sellUnitPrice": row["sellFenPerItem"],
            "multiplier": None,
            "enabled": True,
            "note": note_for(row),
        }
        existing = price_index.get((body["modelKey"], CAPABILITY, row["tier"]))
        if existing is None:
            request("POST", args.base_url, "/admin/billing/model-prices", cookie, body)
        else:
            request("PUT", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(existing.get("id"))), cookie, body)

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
