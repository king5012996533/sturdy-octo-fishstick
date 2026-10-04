#!/usr/bin/env python3
"""把模型广场的文案发布到后台。

广场上的"这个模型是干什么的"由 `model_showcase_entries` 表承载，写入入口是
`PUT /api/admin/model-showcase`（按模型标识幂等覆盖）。文案本身放在同目录的
`model-showcase-copy.json`：**改文案改这个文件，再重跑脚本**，和
seed-image-model-prices.py、pin-model-catalog.py 一样是可重放的产品决策，而不是
"谁当初在后台点过什么"。

## 两个数据来源，各管一段

| 字段 | 来源 | 维护方式 |
| --- | --- | --- |
| tagline / summary / highlights / readme | 人工中文编写 | 改 json |
| sourceUrl / sourceNote | 上游 Replicate 页面 | `--refresh-upstream` 抓 |

中文文案不抓上游：Replicate 的 model.description 是英文营销语，直接摆到中文页面上
既读不通，也不解释"这个模型在我们平台上能拿来做哪一步"。所以只把上游原文留档进
sourceNote（溯源可查、出问题能对上），展示层一律用中文。

## 用法（默认只预览，不加 --apply 不写任何东西）

    python3 scripts/publish-model-showcase.py                        # 对比本地内容文件与后台现状
    python3 scripts/publish-model-showcase.py --refresh-upstream      # 只抓上游原文，更新 json
    KINO_ADMIN_COOKIE='<管理员会话 Cookie>' \\
        python3 scripts/publish-model-showcase.py --apply             # 真正写入

`--refresh-upstream` 与 `--apply` 互斥：抓取会改内容文件，写入会改线上，混在一次运行里
出问题时说不清是哪一步弄的。

没有上游模型的（例如走自己通道的 deepseek-flash、seedance-2.0/2.5）留空 upstream，
发布时 sourceUrl / sourceNote 原样保留 json 里已有的值。
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request
from datetime import date
from pathlib import Path

COPY_PATH = Path(__file__).with_name("model-showcase-copy.json")
REPLICATE_PREFIX = "https://replicate.com/"
# 同时抓多个模型时，页面上游没有限速，但也别把自己当压测工具。
USER_AGENT = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124 Safari/537.36"

JSON_SCRIPT_PATTERN = re.compile(r'<script[^>]*type="application/json"[^>]*>(.*?)</script>', re.S)
NOT_FOUND_MARKERS = ("Page not found", "404 Not Found")


class ApiError(RuntimeError):
    pass


class UpstreamError(RuntimeError):
    pass


# ---------- 上游抓取 ----------

def extract_model_block(html: str) -> dict | None:
    """从模型页里找出内嵌的 model 对象。

    页面把数据分片在十来个 `<script type="application/json">` 里，model 会在其中好几个
    分片里各带一份（列表、详情、计费各一份），分片顺序不是契约。所以这里**逐个分片做
    递归查找**，而不是写死"第 5 个分片"——上游调整一次布局，写死下标的脚本就静默抓空。
    """
    for raw in JSON_SCRIPT_PATTERN.findall(html):
        try:
            data = json.loads(raw)
        except json.JSONDecodeError:
            continue
        found = find_model_block(data)
        if found:
            return found
    return None


def find_model_block(node) -> dict | None:
    if isinstance(node, dict):
        candidate = node.get("model")
        if isinstance(candidate, dict) and str(candidate.get("description") or "").strip():
            return candidate
        for value in node.values():
            found = find_model_block(value)
            if found:
                return found
    elif isinstance(node, list):
        for value in node:
            found = find_model_block(value)
            if found:
                return found
    return None


def fetch_upstream(model_key: str, timeout: int = 25) -> dict | None:
    """抓一个模型的上游简介；模型在上游不存在时返回 None（由调用方保留原值）。"""
    url = REPLICATE_PREFIX + model_key.strip().lstrip("/")
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT, "Accept-Language": "en-US,en;q=0.9"})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            html = response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return None
        raise UpstreamError(f"{url} → HTTP {error.code}") from error
    except urllib.error.URLError as error:
        raise UpstreamError(f"{url} → 无法连接：{error.reason}") from error

    block = extract_model_block(html)
    if not block:
        raise UpstreamError(f"{url} → 页面里没找到 model 数据（布局可能已变）")
    return {
        "url": str(block.get("url") or url),
        "description": " ".join(str(block.get("description") or "").split()),
    }


# ---------- 内容文件 ----------

def load_copy(path: Path) -> dict:
    if not path.exists():
        raise SystemExit(f"找不到内容文件：{path}")
    return json.loads(path.read_text(encoding="utf-8"))


def save_copy(path: Path, document: dict) -> None:
    path.write_text(json.dumps(document, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def entry_source_note(entry: dict) -> str:
    """sourceNote 是给运营看的溯源备注，不是展示内容：写清原文是哪来的，再附原文。

    自述文件的英文原文更长，放在内容文件的 upstream.readme 里留档，不重复塞进备注；
    这里只标一句它的存在与出处，方便改中文时回去对。
    """
    upstream = entry.get("upstream") or {}
    description = str(upstream.get("description") or "").strip()
    if not description:
        return str(entry.get("sourceNote") or "")
    fetched_at = str(upstream.get("fetchedAt") or "").strip()
    stamp = f"（{fetched_at} 抓取）" if fetched_at else ""
    note = f"上游 Replicate 简介原文{stamp}：{description}"
    if str(upstream.get("readme") or "").strip():
        note += "；自述文件英文原文留档在 upstream.readme"
    return note


def payload_for(entry: dict) -> dict:
    upstream = entry.get("upstream") or {}
    return {
        "modelKey": entry["modelKey"],
        "tagline": entry.get("tagline", ""),
        "summary": entry.get("summary", ""),
        "highlights": entry.get("highlights", []),
        "sourceUrl": str(upstream.get("url") or entry.get("sourceUrl") or ""),
        "sourceNote": entry_source_note(entry),
        "examples": entry.get("examples", []),
        "readme": entry.get("readme", ""),
    }


# ---------- 后台接口 ----------

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


def same_as_existing(payload: dict, existing: dict) -> bool:
    return (
        (existing.get("tagline") or "") == payload["tagline"]
        and (existing.get("summary") or "") == payload["summary"]
        and list(existing.get("highlights") or []) == list(payload["highlights"])
        and (existing.get("sourceUrl") or "") == payload["sourceUrl"]
        and (existing.get("sourceNote") or "") == payload["sourceNote"]
        and (existing.get("readme") or "") == payload["readme"]
    )


# ---------- 两步动作 ----------

def refresh_upstream(document: dict) -> int:
    """抓上游原文写回内容文件；抓不到的（上游没有这个模型）保留原值。"""
    today = date.today().isoformat()
    failures = 0
    for entry in document["models"]:
        model_key = entry["modelKey"]
        try:
            upstream = fetch_upstream(model_key)
        except UpstreamError as error:
            print(f"  [跳过] {model_key}：{error}")
            failures += 1
            continue
        if upstream is None:
            print(f"  [无上游] {model_key}：Replicate 上没有这个模型，保留原值")
            continue
        previous = (entry.get("upstream") or {}).get("description") or ""
        entry["upstream"] = {"url": upstream["url"], "description": upstream["description"], "fetchedAt": today}
        marker = "更新" if previous != upstream["description"] else "无变化"
        print(f"  [{marker}] {model_key}：{upstream['description'][:60]}…")
    return failures


def publish(document: dict, base_url: str, cookie: str, apply: bool, only: set[str]) -> int:
    if not cookie.strip():
        print("缺少管理员会话 Cookie：设置 KINO_ADMIN_COOKIE 或传 --cookie", file=sys.stderr)
        return 2

    existing_entries = request("GET", base_url, "/admin/model-showcase", cookie).get("entries") or []
    existing = {str(item.get("modelKey") or ""): item for item in existing_entries}

    created = updated = unchanged = 0
    for entry in document["models"]:
        model_key = entry["modelKey"]
        if only and model_key not in only:
            continue
        payload = payload_for(entry)
        if not payload["tagline"] and not payload["summary"]:
            print(f"  [跳过] {model_key}：中文文案还是空的，不发半成品")
            continue
        current = existing.get(model_key)
        if current and same_as_existing(payload, current):
            unchanged += 1
            print(f"  [一致] {model_key}")
            continue
        if current:
            updated += 1
            action = "更新"
        else:
            created += 1
            action = "新增"
        print(f"  [{action}] {model_key} → {payload['tagline']}")
        if apply:
            request("PUT", base_url, "/admin/model-showcase", cookie, payload)

    suffix = "" if apply else "（预览，未写入；加 --apply 生效）"
    print(f"\n新增 {created} / 更新 {updated} / 一致 {unchanged}{suffix}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="发布模型广场文案（默认只预览）")
    parser.add_argument("--apply", action="store_true", help="真正写入；不加则只打印将要做的变更")
    parser.add_argument("--refresh-upstream", action="store_true", help="抓 Replicate 原文更新内容文件，不写后台")
    parser.add_argument("--only", action="append", default=[], help="只处理指定模型标识，可重复")
    parser.add_argument("--copy", default=str(COPY_PATH), help="内容文件路径")
    parser.add_argument("--base-url", default=os.environ.get("KINO_BASE_URL", "http://127.0.0.1:8080/api"))
    parser.add_argument("--cookie", default=os.environ.get("KINO_ADMIN_COOKIE", ""))
    args = parser.parse_args()

    if args.apply and args.refresh_upstream:
        print("--apply 与 --refresh-upstream 不能同时用：抓取改文件、写入改线上，分开跑才好定位问题", file=sys.stderr)
        return 2

    path = Path(args.copy)
    document = load_copy(path)

    if args.refresh_upstream:
        print(f"抓取上游原文 → {path}")
        failures = refresh_upstream(document)
        save_copy(path, document)
        print("已写回内容文件" + (f"，{failures} 个模型抓取失败" if failures else ""))
        return 1 if failures else 0

    return publish(document, args.base_url, args.cookie, args.apply, set(args.only))


if __name__ == "__main__":
    sys.exit(main())
