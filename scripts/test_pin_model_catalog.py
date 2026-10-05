"""货架收敛的纯函数测试：不发任何请求，只校验清单怎么算。

这两处货架必须同时收敛（渠道 enabled 与平台模型配置的清单），而它们都是"错了不报错、
只是前台少一个模型"的那种错。所以把算清单的部分单独测出来，比跑一遍真实接口可靠。
"""

import importlib.util
import io
import json
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("pin_model_catalog", Path(__file__).with_name("pin-model-catalog.py"))
pin = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pin)


def channel_model(model_key, capability, enabled=True):
    return {
        "id": "MODEL_x",
        "modelKey": model_key,
        "displayName": model_key,
        "icon": "",
        "capability": capability,
        "protocol": "replicate-prediction-image",
        "capabilityConfig": {"version": 1},
        "enabled": enabled,
        "variants": [{"resolution": "*"}],
    }


def base_config():
    """一份"当前状态"的平台配置：渠道清单用 managed_profile 生成，保证形状规范。"""

    return {
        "imageModel": "C1::m-old",
        "models": ["C1::m-old", "C1::audio-1", "C2::t-1", "beefapi::x"],
        "imageModels": ["C1::m-old", "beefapi::x"],
        "videoModels": [],
        "audioModels": ["C1::audio-1"],
        "channels": [
            {
                "id": "C1",
                "models": ["m-old", "audio-1"],
                "modelProfiles": [
                    pin.managed_profile(channel_model("m-old", "image")),
                    pin.managed_profile(channel_model("audio-1", "audio")),
                ],
            },
            {"id": "beefapi", "models": ["x"], "modelProfiles": [{"model": "x", "capability": "image", "protocol": "openai-image"}]},
        ],
    }


class ManagedProfileTest(unittest.TestCase):
    def test_keeps_only_the_config_shape(self):
        profile = pin.managed_profile(channel_model("openai/gpt-image-2", "image"))
        self.assertEqual(
            sorted(profile),
            ["capability", "capabilityConfig", "displayName", "icon", "model", "protocol"],
        )
        # 平台配置读的是 `model`，渠道模型接口给的是 `modelKey`：这层改名是必须的，
        # 少改一次前台就认不出这条画像。
        self.assertEqual(profile["model"], "openai/gpt-image-2")


class ReconcileListTest(unittest.TestCase):
    def test_preserves_order_and_appends_new(self):
        self.assertEqual(pin.reconcile_list(["a", "b"], ["b", "c", "a"]), ["a", "b", "c"])

    def test_drops_entries_that_left_the_target(self):
        self.assertEqual(pin.reconcile_list(["a", "b"], ["a"]), ["a"])


class ReconcileShelfListTest(unittest.TestCase):
    def test_keeps_unmanaged_channel_entries(self):
        # beefapi 不归这个脚本管：它不该因为读不到能力画像就被删掉。
        result = pin.reconcile_shelf_list(["C1::m-old", "beefapi::x"], ["C1::m-new"], {"C1"})
        self.assertEqual(result, ["beefapi::x", "C1::m-new"])

    def test_drops_stale_entries_of_managed_channels(self):
        result = pin.reconcile_shelf_list(["C1::m-old", "C1::m-new"], ["C1::m-new"], {"C1"})
        self.assertEqual(result, ["C1::m-new"])


class ShelfPlanTest(unittest.TestCase):
    def test_adds_models_and_rebuilds_profiles_and_lists(self):
        shelf = {
            "C1": [
                pin.managed_profile(channel_model("m-old", "image")),
                pin.managed_profile(channel_model("audio-1", "audio")),
                pin.managed_profile(channel_model("m-new", "image")),
            ]
        }
        config, changes = pin.shelf_plan(json.loads(json.dumps(base_config())), shelf)
        channel = config["channels"][0]
        self.assertEqual(channel["models"], ["m-old", "audio-1", "m-new"])
        self.assertEqual([profile["model"] for profile in channel["modelProfiles"]], ["m-old", "audio-1", "m-new"])
        self.assertEqual(config["imageModels"], ["C1::m-old", "beefapi::x", "C1::m-new"])
        self.assertEqual(config["models"], ["C1::m-old", "C1::audio-1", "C2::t-1", "beefapi::x", "C1::m-new"])
        # 与模型无关的键必须原样留着：这份配置里还有默认选中模型、提示词等设置。
        self.assertEqual(config["imageModel"], "C1::m-old")
        self.assertTrue(any("m-new" in line for line in changes))

    def test_dropping_a_model_removes_it_everywhere(self):
        shelf = {"C1": [pin.managed_profile(channel_model("audio-1", "audio"))]}
        config, _ = pin.shelf_plan(json.loads(json.dumps(base_config())), shelf)
        self.assertEqual(config["channels"][0]["models"], ["audio-1"])
        self.assertEqual(config["imageModels"], ["beefapi::x"])
        self.assertEqual(config["models"], ["C1::audio-1", "C2::t-1", "beefapi::x"])

    def test_unmanaged_channel_is_untouched(self):
        shelf = {
            "C1": [
                pin.managed_profile(channel_model("m-old", "image")),
                pin.managed_profile(channel_model("audio-1", "audio")),
            ]
        }
        config, changes = pin.shelf_plan(json.loads(json.dumps(base_config())), shelf)
        # C1 的清单本来就是这两条：不该产生任何变更。
        self.assertEqual(config["channels"][1]["models"], ["x"])
        self.assertEqual(
            config["channels"][1]["modelProfiles"],
            [{"model": "x", "capability": "image", "protocol": "openai-image"}],
        )
        self.assertEqual(changes, [])

    def test_missing_channels_block_is_tolerated(self):
        config, changes = pin.shelf_plan({"models": []}, {"C1": []})
        self.assertEqual(config, {"models": []})
        self.assertEqual(changes, [])



class RegisterMissingChannelsTest(unittest.TestCase):
    def test_appends_channel_absent_from_config(self):
        config = json.loads(json.dumps(base_config()))
        shelf = {"C1": [pin.managed_profile(channel_model("m-old", "image"))], "C9": [pin.managed_profile(channel_model("v-new", "video"))]}
        added = pin.register_missing_channels(config, shelf, {"C9": {"name": "新渠道", "apiFormat": "openai", "sortOrder": 3}})
        self.assertEqual(len(added), 1)
        channel = config["channels"][-1]
        self.assertEqual(channel["id"], "C9")
        self.assertEqual(channel["name"], "新渠道")
        self.assertEqual(channel["models"], ["v-new"])
        self.assertEqual([profile["model"] for profile in channel["modelProfiles"]], ["v-new"])
        # 系统渠道在浏览器侧只拿相对地址，上游地址与平台密钥不下发。
        self.assertEqual(channel["baseUrl"], "/api/ai/system/C9")
        self.assertEqual(channel["apiKey"], "system")

    def test_existing_channel_is_left_alone(self):
        config = json.loads(json.dumps(base_config()))
        shelf = {"C1": [pin.managed_profile(channel_model("m-old", "image"))]}
        self.assertEqual(pin.register_missing_channels(config, shelf, {}), [])
        self.assertEqual(len(config["channels"]), 2)

    def test_empty_shelf_channel_is_skipped(self):
        # 一个模型都没上架的渠道补进配置只会让前台多一条空渠道。
        config = json.loads(json.dumps(base_config()))
        self.assertEqual(pin.register_missing_channels(config, {"C9": []}, {}), [])
        self.assertEqual(len(config["channels"]), 2)

    def test_missing_channels_block_is_tolerated(self):
        self.assertEqual(pin.register_missing_channels({"models": []}, {"C9": [{"model": "x"}]}, {}), [])

class CliTest(unittest.TestCase):
    def test_requires_admin_cookie(self):
        import os
        from unittest.mock import patch

        with patch.dict(os.environ, {"KINO_ADMIN_COOKIE": ""}, clear=False):
            with patch("sys.argv", ["pin-model-catalog.py"]):
                stderr = io.StringIO()
                with patch("sys.stderr", stderr):
                    self.assertEqual(pin.main(), 2)
        self.assertIn("KINO_ADMIN_COOKIE", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
