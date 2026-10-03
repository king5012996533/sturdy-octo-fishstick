"""模型广场文案发布：上游解析、溯源备注、幂等比较、预览不写线上。

不发真实请求：上游页面用内嵌 JSON 的 HTML 片段伪造，后台用本地 ThreadingHTTPServer
顶替。真去抓 Replicate 的测试会在对方改版那天变成红灯，而它本来该报的是"布局解析"这件事。
"""

import importlib.util
import io
import json
from contextlib import redirect_stderr, redirect_stdout
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import threading
import unittest

spec = importlib.util.spec_from_file_location("publish_model_showcase", Path(__file__).with_name("publish-model-showcase.py"))
showcase = importlib.util.module_from_spec(spec)
spec.loader.exec_module(showcase)


def script_block(payload: dict) -> str:
    return f'<script id="react-component-props-1" type="application/json">{json.dumps(payload)}</script>'


def entry(model_key: str, **overrides) -> dict:
    base = {"modelKey": model_key, "tagline": "定位语", "summary": "简介", "highlights": ["要点"]}
    base.update(overrides)
    return base


class UpstreamParsingTest(unittest.TestCase):
    def test_finds_model_block_regardless_of_script_order(self):
        html = "".join(
            [
                script_block({"theme": "dark"}),
                '<script type="application/json">{ 这不是 JSON }</script>',
                script_block({"initialStatus": "succeeded", "model": {"name": "sunburst", "description": ""}}),
                script_block({"model": {"name": "sunburst", "description": "Most capable image model", "url": "https://replicate.com/openai/x"}}),
            ]
        )
        block = showcase.extract_model_block(html)
        self.assertIsNotNone(block)
        self.assertEqual(block["description"], "Most capable image model")

    def test_returns_none_when_page_has_no_model_description(self):
        html = script_block({"theme": "dark"}) + script_block({"model": {"name": "x", "description": "   "}})
        self.assertIsNone(showcase.extract_model_block(html))
        self.assertIsNone(showcase.extract_model_block("<html>没有内嵌数据</html>"))


class SourceNoteTest(unittest.TestCase):
    def test_keeps_manual_note_when_there_is_no_upstream(self):
        self.assertEqual(showcase.entry_source_note(entry("deepseek-flash", sourceNote="手写溯源")), "手写溯源")
        self.assertEqual(showcase.entry_source_note(entry("deepseek-flash")), "")

    def test_quotes_upstream_text_with_fetch_date(self):
        note = showcase.entry_source_note(
            entry(
                "openai/gpt-image-2.5-sunburst",
                upstream={"url": "https://replicate.com/openai/gpt-image-2.5-sunburst", "description": "Most capable image model", "fetchedAt": "2026-10-04"},
            )
        )
        self.assertIn("2026-10-04", note)
        self.assertIn("Most capable image model", note)


class PayloadTest(unittest.TestCase):
    def test_maps_upstream_to_source_fields_and_defaults_examples(self):
        payload = showcase.payload_for(
            entry("openai/gpt-image-2.5-sunburst", upstream={"url": "https://replicate.com/openai/gpt-image-2.5-sunburst", "description": "原文"})
        )
        self.assertEqual(payload["sourceUrl"], "https://replicate.com/openai/gpt-image-2.5-sunburst")
        self.assertIn("原文", payload["sourceNote"])
        self.assertEqual(payload["examples"], [])

    def test_same_as_existing_watches_every_published_field(self):
        payload = showcase.payload_for(entry("openai/gpt-image-2"))
        existing = dict(payload)
        self.assertTrue(showcase.same_as_existing(payload, existing))

        for field, value in (("tagline", "改过的定位语"), ("summary", "改过的简介"), ("highlights", ["别的"]), ("sourceUrl", "https://x"), ("sourceNote", "备注")):
            drifted = dict(existing)
            drifted[field] = value
            self.assertFalse(showcase.same_as_existing(payload, drifted), f"{field} 变了却没识别出来")

        self.assertFalse(showcase.same_as_existing(payload, {}))


class FakeAdminServer:
    """只实现发布脚本用到的那两个端点：列出现有文案、按模型标识覆盖写入。"""

    def __init__(self):
        self.entries: list[dict] = []
        self.writes: list[dict] = []
        server = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):  # 测试输出里不要 HTTP 访问日志
                pass

            def _send(self, payload: dict) -> None:
                body = json.dumps({"code": 0, "msg": "", "data": payload}).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                self._send({"entries": server.entries})

            def do_PUT(self):
                length = int(self.headers.get("Content-Length") or 0)
                payload = json.loads(self.rfile.read(length).decode("utf-8"))
                server.writes.append(payload)
                server.entries = [item for item in server.entries if item.get("modelKey") != payload["modelKey"]] + [payload]
                self._send(payload)

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.httpd.server_address[1]}/api"

    def close(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()


class PublishTest(unittest.TestCase):
    def setUp(self):
        self.server = FakeAdminServer()
        self.addCleanup(self.server.close)
        self.document = {
            "note": "测试用",
            "models": [
                entry("openai/gpt-image-2"),
                # 中文文案还没写的条目：不能发半成品上去，否则广场上会出现一张只有名字的卡。
                entry("google/imagen-4", tagline="", summary="", highlights=[]),
            ],
        }

    def run_publish(self, apply: bool) -> str:
        output = io.StringIO()
        with redirect_stdout(output):
            code = showcase.publish(self.document, self.server.base_url, "canana_session=test", apply, set())
        self.assertEqual(code, 0)
        return output.getvalue()

    def test_preview_lists_changes_without_writing(self):
        output = self.run_publish(apply=False)
        self.assertIn("[新增] openai/gpt-image-2", output)
        self.assertIn("未写入", output)
        self.assertIn("[跳过] google/imagen-4", output)
        self.assertEqual(self.server.writes, [])

    def test_apply_writes_once_then_reports_unchanged(self):
        self.run_publish(apply=True)
        self.assertEqual([item["modelKey"] for item in self.server.writes], ["openai/gpt-image-2"])

        output = self.run_publish(apply=True)
        self.assertIn("[一致] openai/gpt-image-2", output)
        self.assertIn("一致 1", output)
        self.assertEqual(len(self.server.writes), 1)

    def test_missing_cookie_refuses_to_run(self):
        with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            code = showcase.publish(self.document, self.server.base_url, "", True, set())
        self.assertEqual(code, 2)
        self.assertEqual(self.server.writes, [])


if __name__ == "__main__":
    unittest.main()
