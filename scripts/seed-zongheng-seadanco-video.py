#!/usr/bin/env python3
"""上架纵横科技 seadan 视频族（对外 seadanco2.0mini / seadanco2.5），按条定价。

用法（默认预览，不加 --apply 不写任何东西）：

    KINO_ADMIN_COOKIE='<浏览器里的会话 Cookie>' \
        python3 scripts/seed-zongheng-seadanco-video.py

    KINO_ADMIN_COOKIE='...' KINO_BASE_URL=https://kinotv.xingtudesign.com/api \
        python3 scripts/seed-zongheng-seadanco-video.py --apply

只管这两个模型和它们的价目：渠道「纵横科技 · 主账号」已经存在，本脚本不改它的 Base URL、
并发与密钥。

## 为什么两条货写在一个脚本里

两条都是「按条计价 + 分辨率分档 + 插件层不吃参考视频/音频」的同族货，除了上游标识、
分辨率枚举、时长形状和价格之外没有别的差别。一个模型一个脚本，会把同一份能力合同的
构造复制两遍，改一处漏一处。

## 为什么单位是 REQUEST

上游按条结算：一条一个价，与时长、分辨率都无关。单位填 REQUEST，用量恒为 1；沿用默认的
SECOND 会按用户选的秒数相乘，一条 800 分的片子被按 30 倍收。

## 上游在持续换货，这个脚本要跟着对账

2026-10-16 首次联调时上游回 503 video_model_not_configured，模型当场停用。回查上游清单时
发现更麻烦的事：**`DDseadanco2.5` 这个名字已经从公开清单里消失了**——停用的那条模型不只是
被上游拒收，是它认的上游标识已经不存在。本次按上游现清单重新指认 `kksedanco2.5` 接替它。

顺带记下这次的教训：`GET /api/models`（公开能力中枢）列的是**全部**在售模型，不等于我们
这把 Key 被授权——授权清单要看 `GET /v1/models`（见上游文档「鉴权与模型」）。上架前先确认
目标模型在授权清单里，否则上架等于给用户摆一个必然 503 的按钮。

## 参考视频与参考音频在本平台上用不上

上游给这两条都声明了参考视频/参考音频（3/3 与 10/10），但 `zongheng-video` 插件在协议层
直接拒收（见 plugin-packages/zongheng-video/manifest.json 的 validations）。能力合同里写 0，
面板上就不会出现这两个入口——用户看不到的开关才是不会误用的开关。

## 规格要对账到真实请求，能力接口也会多报

分辨率、参考图上限取自 `GET /v1/models?include_capabilities=1` 的 capabilities，但**时长必须实测**：

- capabilities 给 Fseadanco2.0mini 的 `duration_options` 是 5–16，`max_duration_seconds` 也是 16，
  而 16 秒实测被上游 400 拒收（`invalid_parameters`「视频时长不符合当前模型要求」），15 秒才通过。
  卡片文案「5-15秒」是对的，机器字段反而多报了一秒。
- 参考图上限、分辨率、比例与实测一致：16:9 / 9:16 / 1:1 都实测出片，21:9 与 9:21 被拒，4:3 被接受。
  4:3 能跑但没挂——同一条渠道上 grok 只挂三个画幅，这里跟着同一套口径，不为了多一个选项把面板摊开。

结论：能力接口说支持不等于真能提交，上架前至少拿一条真实请求打过一次。

## seadanco2.5 先挂停用

`kksedanco2.5` 名字认、时长校验也过（固定 30 秒），但一条参数完全合法的 30 秒请求稳定回
502 `video_service_unavailable`（连续三次一致），说明这条逆向货的后端当时就是断的。800 分一条的
按钮点下去必然失败，先留停用；上游恢复后把 `enabled` 改回 True 重跑本脚本即可。

## 清晰度分档：价差是定价选择，不是成本差异

上游按条结算、与清晰度无关，所以 480p 与 720p 的价差纯粹是定价：480p 卖 300 分（¥3.00，
毛利 33%）当入口价，720p 卖 400 分（¥4.00，毛利 50%）。两档都留在能力合同里，面板上就是
一个二选一的清晰度开关——有 100 分的差价，让用户自己挑比只给一档更说得通。

价目按 `priceTier` 分档，平台把用户选的清晰度折成档位名再取价（`480`/`480p` → `480P`，
见 backend/internal/app/task_credit.go 的 videoResolutionPriceTier），所以三条价目必须写成
`480P` / `720P` / 空档。空档是"这次请求没带清晰度"时的兜底，按默认档 720p 计价：上游成本
固定，收低了就是纯亏。

## 毛利

| 模型 | 状态 | 售价 | 上游成本 | 毛利 |
| --- | --- | --- | --- | --- |
| seadanco2.0mini | 上架 | 480p ¥3.00 / 720p ¥4.00 | ¥2.00 | 33% / 50% |
| seadanco2.5 | 停用（上游 502） | ¥8.00 | ¥5.00 | 38% |
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
CHANNEL_BASE_URL = "https://cnd-coo-new.pages.dev"

CAPABILITY = "VIDEO"
# 必须是 REQUEST：上游按条结算，用量恒为 1。
UNIT = "REQUEST"
PROTOCOL = "zongheng-video"
VENDOR_CODE = "zongheng-video"

PROMPT_MAX_CHARS = 8000
MAX_IMAGE_BYTES = 30 * 1024 * 1024
# 上游 `aspect_ratio_options` 对这两个模型都是空数组。空数组按上游文档的意思是"没有可选项时
# 省略该字段"，不是"任意比例都行"；但插件模板会给 ratio 兜一个 16:9，线上 grok 也是这么跑的。
# 这里沿用同一套画幅，避免同一条渠道上出现两种画幅口径。
RATIOS = ["16:9", "9:16", "1:1"]
DEFAULT_RATIO = "16:9"

MODELS: list[dict] = [
    {
        "key": "seadanco2.0mini",
        "display": "seadanco2.0mini",
        "upstream": "Fseadanco2.0mini",
        "enabled": True,
        "hold_reason": "",
        "resolutions": ["720p", "480p"],
        "default_resolution": "720p",
        # 按条计价，一条的价钱不随时长变，拉满才是这档的用法（同族既有口径）。
        # 上限是 15 不是能力接口写的 16：16 秒实测被上游拒收，见文件头。
        "duration": {"selection": "range", "min": 5, "max": 15, "step": 1, "default": 15},
        "max_images": 9,
        "upstream_fen": 200,
        "tiers": [
            ("480P", 300, "480p"),
            ("720P", 400, "720p"),
            ("", 400, "未指定分辨率（按 720p 档）"),
        ],
    },
    {
        "key": "seadanco2.5",
        "display": "seadanco2.5",
        "upstream": "kksedanco2.5",
        # 上游此刻 502，见文件头；价目照目标价写好，等 enabled 打开就是可用状态。
        "enabled": False,
        "hold_reason": "上游 video_service_unavailable，等恢复再开",
        "resolutions": ["720p"],
        "default_resolution": "720p",
        # 上游只有 30 秒一档，用 enum 而不是 range(30,30)：面板上就是一个固定值，
        # 不是一根可以拖动但拖不出第二个值的滑杆。
        "duration": {"selection": "enum", "values": [30], "default": 30},
        "max_images": 30,
        "upstream_fen": 500,
        "tiers": [("720P", 800, "720p"), ("", 800, "未指定分辨率")],
    },
]


def capability_config(model: dict) -> dict:
    """能力合同：分辨率照上游枚举、时长照上游形状，参考视频与参考音频一律关掉。"""
    return {
        "version": 1,
        "video": {
            "references": {
                "promptMaxChars": PROMPT_MAX_CHARS,
                "minImages": 0,
                "maxImages": model["max_images"],
                "maxImageBytes": MAX_IMAGE_BYTES,
                # 上游给的参考视频/参考音频本插件发不出去，见文件头。
                "maxVideos": 0,
                "maxVideoBytes": 0,
                "maxVideoDurationSeconds": 0,
                "maxAudios": 0,
                "maxAudioBytes": 0,
                "maxAudioDurationSeconds": 0,
            },
            "duration": dict(model["duration"]),
            "ratios": RATIOS,
            "defaultRatio": DEFAULT_RATIO,
            "resolutions": model["resolutions"],
            "defaultResolution": model["default_resolution"],
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


def price_key(channel_id: str, model: dict) -> str:
    """定价表里的模型标识带渠道前缀，与前台结算时用的 key 一致。"""
    return f"{channel_id}::{model['key']}"


def duration_text(model: dict) -> str:
    duration = model["duration"]
    if duration["selection"] == "enum":
        values = duration["values"]
        return f"固定 {values[0]} 秒" if len(values) == 1 else "/".join(str(v) for v in values) + " 秒"
    return f"{duration['min']}–{duration['max']} 秒（默认 {duration['default']}）"


def margin_percent(model: dict, fen: int) -> str:
    """毛利百分比：预览和写库备注用的是同一个口径。"""
    return f"{(1 - Decimal(model['upstream_fen']) / Decimal(fen)) * 100:.0f}%"


def note_for(model: dict, fen: int) -> str:
    note = (
        f"上游 纵横科技 {model['upstream']} 按条结算 ¥{model['upstream_fen'] / 100:.2f}/条"
        f"（与分辨率、时长无关）；一条 {fen} 分（¥{fen / 100:.2f}），毛利 {margin_percent(model, fen)}"
    )
    if not model["enabled"]:
        note += f"；当前停用：{model['hold_reason']}"
    return note


def print_economics() -> None:
    print(f"渠道「{CHANNEL_NAME}」{CHANNEL_BASE_URL}（协议 {PROTOCOL}）")
    for model in MODELS:
        print(
            f"  模型 {model['display']}（上游 {model['upstream']}）· "
            f"{'上架' if model['enabled'] else '停用：' + model['hold_reason']} · "
            f"分辨率 {'/'.join(model['resolutions'])} · 时长 {duration_text(model)} · "
            f"参考图 ≤{model['max_images']} 张"
        )
        for tier, fen, label in model["tiers"]:
            print(f"    {label:<12} {fen:>5} 分/条（¥{fen / 100:.2f}/条，毛利 {margin_percent(model, fen)}）")


def model_payload(model: dict) -> dict:
    return {
        "modelKey": model["key"],
        "providerModelKey": model["upstream"],
        "displayName": model["display"],
        "capability": CAPABILITY.lower(),
        "protocol": PROTOCOL,
        "enabled": model["enabled"],
        "capabilityConfig": capability_config(model),
    }


def model_drift_reason(existing: dict, payload: dict) -> str:
    """说清差在哪，而不是只报"有变化"。

    停用（等上游开通/换货）是这里的常见状态，只报一句"能力配置或上游标识有变化"会让
    "把停用的模型启用"看起来像有人改坏了能力配置。能力配置也要比：只比"模型在不在"，
    改了参考图上限这类参数重跑脚本会被当成无变更。
    """
    # 两个方向都要报：等上游恢复把 enabled 改回 True 时，只看"是否已启用"会把
    # "启用 → 停用"当成无变更，脚本跑完看着成功、货其实还挂着。
    if (existing.get("enabled") is True) != bool(payload["enabled"]):
        return "已停用 → 启用" if payload["enabled"] else "启用 → 停用"
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


def price_drift_reason(existing: dict, fen: int) -> str:
    """只报真正变了的那几项。

    停用与改价经常成对出现（先停用等上游换货、换完再改回售价），把三个字段一律拼进
    文案，会让"只变了售价"看起来像"单位也变了"——现价目本来就是 REQUEST，却被打成
    "REQUEST → REQUEST"，看的人只会怀疑脚本读错了现有值。
    """
    changes: list[str] = []
    if existing.get("unit") != UNIT:
        changes.append(f"单位 {existing.get('unit')} → {UNIT}")
    if existing.get("sellUnitPrice") != fen:
        changes.append(f"售价 {existing.get('sellUnitPrice')} → {fen} 分/条")
    if existing.get("enabled") is not True:
        changes.append("已停用 → 启用")
    return "，".join(changes)


def model_drift(existing: dict, payload: dict) -> bool:
    return model_drift_reason(existing, payload) != ""


def find_channel(base_url: str, cookie: str) -> dict | None:
    page = request("GET", base_url, "/admin/channels?pageSize=200", cookie)
    for channel in page.get("channels") or []:
        if str(channel.get("name") or "").strip() == CHANNEL_NAME:
            return channel
    return None


def main() -> int:
    parser = argparse.ArgumentParser(description="上架纵横科技 seadan 视频族（对外 seadanco2.0mini / seadanco2.5，默认只预览）")
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

    rows = request("GET", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie).get("models") or []
    existing_by_key = {str(row.get("modelKey") or ""): row for row in rows}

    prices = request("GET", args.base_url, "/admin/billing/model-prices", cookie).get("prices") or []
    price_index = {
        (row.get("modelKey"), row.get("capability"), row.get("priceTier") or ""): row
        for row in prices
    }

    plan: list[str] = []
    for model in MODELS:
        payload = model_payload(model)
        existing_model = existing_by_key.get(model["key"])
        if existing_model is None:
            state = "" if model["enabled"] else "，停用"
            plan.append(
                f"上架渠道模型 {model['display']}（上游 {model['upstream']} / {CAPABILITY} / {PROTOCOL}{state}）"
            )
        elif (reason := model_drift_reason(existing_model, payload)):
            plan.append(f"更新渠道模型 {model['display']}（{reason}）")
        for tier, fen, label in model["tiers"]:
            existing = price_index.get((price_key(channel_id, model), CAPABILITY, tier))
            if existing is None:
                plan.append(f"新增价目 {model['key']} [{label}]：{fen} 分/条")
            elif reason := price_drift_reason(existing, fen):
                plan.append(f"更新价目 {model['key']} [{label}]：{reason}")

    if not plan:
        print("渠道模型与价目已经是目标状态，无需变更。")
        return 0

    for line in plan:
        print(" ·", line)

    if not args.apply:
        print(f"\n预览结束：{len(plan)} 项待写入。加 --apply 才会真正写库。")
        return 0

    for model in MODELS:
        payload = model_payload(model)
        existing_model = existing_by_key.get(model["key"])
        if existing_model is None:
            request("POST", args.base_url, f"/admin/channels/{urllib.parse.quote(channel_id)}/models", cookie, payload)
        elif model_drift(existing_model, payload):
            request(
                "PUT",
                args.base_url,
                f"/admin/channels/{urllib.parse.quote(channel_id)}/models/{urllib.parse.quote(str(existing_model.get('id') or model['key']))}",
                cookie,
                payload,
            )
        for tier, fen, _label in model["tiers"]:
            body = {
                "modelKey": price_key(channel_id, model),
                "capability": CAPABILITY,
                "priceTier": tier,
                "unit": UNIT,
                "vendorCode": VENDOR_CODE,
                "upstreamUnitPrice": None,
                "sellUnitPrice": fen,
                "multiplier": None,
                "enabled": True,
                "note": note_for(model, fen),
            }
            existing = price_index.get((body["modelKey"], CAPABILITY, tier))
            if existing is None:
                request("POST", args.base_url, "/admin/billing/model-prices", cookie, body)
            else:
                request("PUT", args.base_url, "/admin/billing/model-prices/" + urllib.parse.quote(str(existing.get("id"))), cookie, body)

    print(f"\n已写入。渠道 ID：{channel_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
