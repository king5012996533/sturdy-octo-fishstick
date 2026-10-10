"""H3 模型卡种子脚本的纯函数测试：不发任何请求，只校验上架参数怎么算。

两条口径必须同时成立，否则线上会出现「前台能选、提交才失败」：
  · 768P 卡开了 30 秒，2K 卡没开；
  · 插件清单的 duration 窗口必须覆盖两张卡登记的全部时长。
脚本自己跑起来才能发现第二处，所以这里把两个文件读在一起比。
"""

import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

spec = importlib.util.spec_from_file_location("seed_minimax_h3", ROOT / "scripts" / "seed-minimax-h3.py")
seed = importlib.util.module_from_spec(spec)
spec.loader.exec_module(seed)

PLUGIN_MANIFEST = ROOT / "plugin-packages" / "minimax-hailuo-video-v2" / "manifest.json"


def model_by_key(model_key):
    for model in seed.MODELS:
        if model["modelKey"] == model_key:
            return model
    raise AssertionError(f"missing model {model_key}")


class MinimaxH3SeedTest(unittest.TestCase):
    def test_thirty_seconds_is_pinned_to_the_768p_card(self):
        per_second = list(range(4, 16))
        self.assertEqual(model_by_key("MiniMax-H3")["durations"], per_second + [30])
        self.assertEqual(model_by_key("MiniMax-H3-2K")["durations"], per_second)

    def test_cards_keep_one_resolution_tier_each(self):
        # 按分辨率拆卡的意义就在这里：填不出两个价的模型必须钉死在一档上，
        # 否则会出现「按 768P 的价跑了 2K 的活」。
        for model in seed.MODELS:
            config = seed.capability_config(model)
            video = config["video"]
            self.assertEqual(video["resolutions"], [model["resolution"]])
            self.assertEqual(video["defaultResolution"], model["resolution"])
            self.assertEqual(video["duration"]["values"], model["durations"])

    def test_sell_price_baseline_matches_the_live_price(self):
        # 基线的唯一用途是"价目行丢了要重建时填什么"，所以它必须等于线上实际生效的价。
        # 2026-10-10 线上：768P 21 分/秒（tier 768P）、2K 30 分/秒（默认 tier）。
        self.assertEqual(model_by_key("MiniMax-H3")["sellFenPerSecond"], 21)
        self.assertEqual(model_by_key("MiniMax-H3-2K")["sellFenPerSecond"], 30)

    def test_plugin_duration_window_covers_every_card(self):
        manifest = json.loads(PLUGIN_MANIFEST.read_text(encoding="utf-8"))
        provider = manifest["contributes"]["providers"][0]
        parameters = {item["name"]: item for item in provider["parameters"]}
        plugin_values = {int(value) for value in parameters["duration"]["values"]}

        for model in seed.MODELS:
            missing = set(model["durations"]) - plugin_values
            self.assertEqual(missing, set(), f"{model['modelKey']} 登记的时长插件不收：{sorted(missing)}")

        # 窗口放宽到 30 是为了 768P，不能再往上开：2K 档的上限由模型卡收紧，
        # 插件是两张卡共用的，这里多放一档就多一批「前端不选、API 能打进来」的越界值。
        self.assertEqual(max(plugin_values), 30)

        assert_rules = [rule for rule in provider["validations"] if "H3 duration" in str(rule.get("message"))]
        self.assertEqual(len(assert_rules), 1, "duration 的上限断言应当只有一条")
        upper = assert_rules[0]["assert"]["$and"][1]["$lte"][1]
        self.assertEqual(upper, 30)


if __name__ == "__main__":
    unittest.main()
