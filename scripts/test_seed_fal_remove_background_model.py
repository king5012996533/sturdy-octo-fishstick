"""fal 抠图上架脚本的纯函数测试：不发任何请求，只校验上架参数怎么算。

两条口径必须同时成立，否则线上会出现「用户能选到一个必然失败的模型」或「价目挂在走不通的端点上」：
  · 渠道模型标识与插件里写死的创建路径必须指向同一条端点（BiRefNet 的路径不按模型名拼）；
  · 能力合同必须关掉一切可调参数，并把 `toolOnly` 打开（抠图没有原图就发不出去，不该进模型下拉）。
"""

import importlib.util
import json
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

spec = importlib.util.spec_from_file_location("seed_fal_remove_background_model", ROOT / "scripts" / "seed-fal-remove-background-model.py")
seed = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = seed
spec.loader.exec_module(seed)

CHANNEL_ID = "CHANNEL_000012"


def plugin_provider():
    manifest = json.loads((ROOT / "plugin-packages" / "fal-remove-background" / "manifest.json").read_text(encoding="utf-8"))
    return manifest["contributes"]["providers"][0]


class FalRemoveBackgroundSeedTest(unittest.TestCase):
    def test_registered_model_key_is_the_endpoint_the_plugin_calls(self):
        # 价目行按"渠道 + 模型标识"归属，而请求路径由插件写死。两边一旦分叉，
        # 用户按的模型就会把请求打到另一条端点上，或者干脆 404。
        provider = plugin_provider()
        self.assertEqual(seed.PROTOCOL, provider["id"])
        self.assertEqual(seed.ENDPOINT_PATH, provider["create"]["path"])
        self.assertEqual(seed.MODEL_KEY, seed.ENDPOINT_PATH.lstrip("/"), "渠道模型标识要对得上上游端点名")
        self.assertEqual(provider["baseUrl"], seed.CHANNEL_BASE_URL)

    def test_capability_contract_hides_the_model_and_closes_every_knob(self):
        image = seed.capability_config()["image"]
        # 抠图模型少一张原图就发不出去，摆进模型下拉只会让用户选中一次必然失败的生成。
        self.assertTrue(image["toolOnly"])
        self.assertEqual(image["maxOutputs"], 1)
        self.assertEqual(image["references"]["maxImages"], 1)
        self.assertFalse(image["references"]["maskSupported"])
        # BiRefNet 不认比例、质量与自定义尺寸，开着它们在面板上等于一组拨了不生效的旋钮。
        self.assertEqual(image["size"]["parameter"], "none")
        self.assertEqual(image["size"]["values"], [])
        self.assertFalse(image["size"]["allowCustom"])
        self.assertFalse(image["quality"]["supported"])
        self.assertEqual(image["quality"]["values"], [])
        # transparentBackground 描述的是"能不能把透明底作为参数发给上游"。这条端点永远返回
        # 带 alpha 的图，没有可调项；打开它会让面板多出一个拨不动的开关。
        self.assertFalse(image["transparentBackground"]["supported"])

    def test_empty_input_is_rejected_before_the_request_leaves(self):
        # 没有原图的抠图不是"参数不全"，是这件事本身不成立；要在本地拦下。
        validations = plugin_provider()["validations"]
        self.assertEqual(len(validations), 1)
        self.assertIn("一张原图", validations[0]["message"])

    def test_model_payload_carries_the_remove_background_protocol(self):
        payload = seed.model_payload()
        self.assertEqual(payload["modelKey"], seed.MODEL_KEY)
        self.assertEqual(payload["providerModelKey"], seed.MODEL_KEY)
        self.assertEqual(payload["protocol"], "fal-remove-background")
        self.assertEqual(payload["capability"], "image")
        self.assertTrue(payload["enabled"])
        self.assertEqual(payload["capabilityConfig"], seed.capability_config())

    def test_normalised_defaults_do_not_look_like_drift(self):
        # 接口会把空串 default 规范化成 "auto" 再落库，所以脚本声明的必须就是落库后的那个值：
        # 否则每次复跑都判成"能力配置有变化"，对生产重复发一次 PUT（2026-10-16 线上实测过）。
        stored = seed.model_payload()
        stored["id"] = "MODEL_000233"
        self.assertFalse(seed.model_drift(stored, seed.model_payload()))
        # 真正的开关变化仍要能识别出来：toolOnly 被关掉必须写回，否则模型会重新出现在下拉里。
        drifted = seed.model_payload()
        drifted["capabilityConfig"]["image"]["toolOnly"] = False
        self.assertTrue(seed.model_drift(drifted, seed.model_payload()))

    def test_price_row_is_by_image_and_keyed_by_channel(self):
        row = seed.price_payload(CHANNEL_ID)
        self.assertEqual(row["modelKey"], f"{CHANNEL_ID}::{seed.MODEL_KEY}")
        # 抠图没有质量档，价目只有"不区分档位"这一行；档位填错了会落到取不到价的那条路径上。
        self.assertEqual(row["priceTier"], "")
        self.assertEqual(row["capability"], "IMAGE")
        self.assertEqual(row["vendorCode"], "fal")
        # 按张计价而不是按次：按次的账单算式会省掉用量，扣费金额与算式对不上。
        self.assertEqual(row["unit"], "IMAGE")
        self.assertEqual(row["sellUnitPrice"], seed.SELL_FEN)
        self.assertEqual(row["upstreamUnitPrice"], seed.upstream_fen())

    def test_price_sells_above_upstream_cost(self):
        # 上游按算力秒计费，一张图到底多少秒要看下个月的账单；这里按实测端到端耗时（1024² 5.7 秒、
        # 4096² 7.4 秒）取 5 秒算力做上限口径。哪天实测涨过这个数，先把售价重新定过再上架。
        self.assertEqual(seed.upstream_fen(), 3, "0.003 美元按 7.2 折算应为 2.16 分，向上取整 3 分")
        self.assertLess(seed.upstream_fen(), seed.SELL_FEN)


if __name__ == "__main__":
    unittest.main()
