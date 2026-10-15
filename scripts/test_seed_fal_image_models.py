"""fal 生图模型上架脚本的纯函数测试：不发任何请求，只校验上架参数怎么算。

两条口径必须同时成立，否则线上会出现「同型号两条渠道扣费不一样」或「换了渠道参数缩水」：
  · 价目与 Replicate 渠道的同型号逐档一价（前台把同名跨渠道模型合并成一行，落点不该改价格）；
  · 显示名与能力合同必须与 Replicate 渠道逐字一致（写错一个字，前台就合并不成一行）。
"""

import importlib.util
import json
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

spec = importlib.util.spec_from_file_location("seed_fal_image_models", ROOT / "scripts" / "seed-fal-image-models.py")
seed = importlib.util.module_from_spec(spec)
# dataclass 解析注解时要按模块名回查 sys.modules，importlib 加载的模块必须先登记再执行。
sys.modules[spec.name] = seed
spec.loader.exec_module(seed)

# 线上 Replicate 渠道同名模型的售价（分/张），来源见 seed-image-model-prices.py 的倍率口径。
# 2.5 是五档（空档落 HIGH 之后还有 xhigh/max），gpt-image-2 只有三档：Replicate 那边这个
# 模型没有 xhigh/max，倍率同为 50000，空档的上游成本是 93 分，所以空档价是 465 而不是 900。
REPLICATE_SELL_FEN = {
    "openai/gpt-image-2.5/sunburst": {"LOW": 45, "MEDIUM": 170, "HIGH": 465, "XHIGH": 900, "MAX": 1800, "": 900},
    "openai/gpt-image-2.5/flare": {"LOW": 45, "MEDIUM": 170, "HIGH": 465, "XHIGH": 900, "MAX": 1800, "": 900},
    "openai/gpt-image-2": {"LOW": 45, "MEDIUM": 170, "HIGH": 465, "": 465},
}
# Replicate 渠道同名模型的上游成本（分/张），用于确认"对齐之后仍然赚钱"。
REPLICATE_UPSTREAM_FEN = {
    "openai/gpt-image-2.5/sunburst": {"LOW": 9, "MEDIUM": 34, "HIGH": 93, "XHIGH": 180, "MAX": 360, "": 180},
    "openai/gpt-image-2.5/flare": {"LOW": 9, "MEDIUM": 34, "HIGH": 93, "XHIGH": 180, "MAX": 360, "": 180},
    "openai/gpt-image-2": {"LOW": 9, "MEDIUM": 34, "HIGH": 93, "": 93},
}
# 能力合同里开放的质量档，也与 Replicate 渠道逐档一致。
REPLICATE_QUALITY_TIERS = {
    "openai/gpt-image-2.5/sunburst": ["low", "medium", "high", "xhigh", "max"],
    "openai/gpt-image-2.5/flare": ["low", "medium", "high", "xhigh", "max"],
    "openai/gpt-image-2": ["low", "medium", "high"],
}


def manifest_text_to_image_base_models():
    """把插件里"文生图端点就是基名"的名单读出来。

    这份名单是插件真正用来拼路径的数据，脚本里的预览输出只是它的复述；两边对不上时，
    预览会显示一条线上根本走不通的路径，所以这里直接读 manifest 比对，而不是各写一份。
    """
    manifest = json.loads((ROOT / "plugin-packages" / "fal-image" / "manifest.json").read_text(encoding="utf-8"))
    provider = manifest["contributes"]["providers"][0]
    branch = provider["create"]["pathTemplate"]["$concat"][2]["$if"]["else"]
    return branch["$switch"]["cases"][0]["when"]["$in"][1]


def model_by_key(model_key):
    for model in seed.MODELS:
        if model.model_key == model_key:
            return model
    raise AssertionError(f"missing model {model_key}")


class FalImageSeedTest(unittest.TestCase):
    def test_models_are_registered_by_base_name(self):
        # fal 把文生图与图生图拆成两条端点，渠道上只能登记基名，端点由参考图决定。
        self.assertEqual(
            sorted(model.model_key for model in seed.MODELS),
            ["openai/gpt-image-2", "openai/gpt-image-2.5/flare", "openai/gpt-image-2.5/sunburst"],
        )
        for model in seed.MODELS:
            self.assertFalse(model.model_key.endswith("/edit"))

    def test_text_to_image_path_follows_the_registered_model(self):
        # gpt-image-2 的文生图端点就是基名本身，试 /text-to-image 实测回 404；
        # 其余模型仍是 {model}/text-to-image。这两条路径必须与插件里的名单一致。
        self.assertEqual(seed.text_to_image_endpoint(model_by_key("openai/gpt-image-2")), "openai/gpt-image-2")
        self.assertEqual(
            seed.text_to_image_endpoint(model_by_key("openai/gpt-image-2.5/sunburst")),
            "openai/gpt-image-2.5/sunburst/text-to-image",
        )

    def test_plugin_base_path_list_matches_the_models_this_script_registers(self):
        # 插件按名单决定 gpt-image-2 走基名、其余走 /text-to-image；脚本登记了新模型却忘了加名单，
        # 线上就是文生图 404。两边必须同时改，所以这里双向比对。
        base_models = manifest_text_to_image_base_models()
        for model in seed.MODELS:
            needs_base_path = seed.text_to_image_endpoint(model) == model.model_key
            self.assertEqual(
                needs_base_path,
                model.model_key in base_models,
                f"{model.model_key} 的文生图路径与插件名单不一致（插件名单：{base_models}）",
            )
        for model_key in base_models:
            self.assertIsNotNone(model_by_key(model_key), f"插件名单里的 {model_key} 没有在本脚本上架")

    def test_display_names_are_byte_identical_to_the_replicate_channel(self):
        # 前台按显示名跨渠道合并，多一个空格就变成两行模型，用户会看到重复型号。
        self.assertEqual(model_by_key("openai/gpt-image-2.5/sunburst").display_name, "GPT Image 2.5 Sunburst")
        self.assertEqual(model_by_key("openai/gpt-image-2.5/flare").display_name, "GPT Image 2.5 Flare")
        self.assertEqual(model_by_key("openai/gpt-image-2").display_name, "GPT Image 2")

    def test_sell_prices_match_the_replicate_tiers(self):
        for model in seed.MODELS:
            self.assertEqual(
                {tier: seed.sell_fen(model, tier) for tier, _ in model.price_tiers},
                REPLICATE_SELL_FEN[model.model_key],
                model.model_key,
            )

    def test_every_tier_sells_above_upstream_cost(self):
        # 对齐售价的前提是上游不贵过售价；哪天 fal 涨价到这个数，这里先红，别等月末对账。
        for model in seed.MODELS:
            for tier, usd in model.price_tiers:
                self.assertLess(
                    seed.upstream_fen(usd), seed.sell_fen(model, tier), f"{model.model_key} {tier} 档售价低于上游成本"
                )

    def test_blank_tier_is_costed_as_high(self):
        # fal 没收到 quality 时按 high 出图，空档必须按 high 的成本兜底，否则这一档在倒贴。
        # 售价不跟着 HIGH 走：空档价对齐的是 Replicate 那边空档的成本，2.5 落在 900、gpt-image-2 落在 465。
        for model in seed.MODELS:
            costs = dict(model.price_tiers)
            self.assertEqual(costs[""], costs["HIGH"], model.model_key)

    def test_gpt_image_2_costs_more_than_replicate_at_high_quality(self):
        # 记录一个反直觉的事实：这个模型上游比 Replicate 贵，别把"fal 就一定更便宜"当成前提。
        # 哪天 fal 降价到这个数以下，这里会红，那时再决定要不要把主力渠道换回来。
        model = model_by_key("openai/gpt-image-2")
        costs = dict(model.price_tiers)
        self.assertGreater(seed.upstream_fen(costs["HIGH"]), REPLICATE_UPSTREAM_FEN[model.model_key]["HIGH"])
        # LOW 档反过来：fal 比 Replicate 便宜一半。
        self.assertLess(seed.upstream_fen(costs["LOW"]), REPLICATE_UPSTREAM_FEN[model.model_key]["LOW"])

    def test_capability_contract_matches_the_replicate_channel(self):
        for model in seed.MODELS:
            image = seed.capability_config(model)["image"]
            self.assertEqual(image["size"]["values"], ["1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4"])
            self.assertFalse(image["size"]["allowCustom"])
            self.assertEqual(image["quality"]["values"], REPLICATE_QUALITY_TIERS[model.model_key], model.model_key)
            self.assertEqual(image["quality"]["default"], "low")
            self.assertEqual(image["references"]["maxImages"], 4)
            self.assertFalse(image["references"]["maskSupported"])
            self.assertEqual(image["maxOutputs"], 10)

    def test_quality_tiers_never_offer_what_the_upstream_rejects(self):
        # gpt-image-2 实测对 xhigh 回 422 literal_error（只认 auto/low/medium/high），
        # 能力合同里多一档，用户就会拿到一个必然失败的选项。
        self.assertEqual(list(model_by_key("openai/gpt-image-2").quality_tiers), ["low", "medium", "high"])
        for model in seed.MODELS:
            self.assertNotIn("auto", model.quality_tiers)

    def test_model_payload_carries_the_fal_protocol(self):
        for model in seed.MODELS:
            payload = seed.model_payload(model)
            self.assertEqual(payload["modelKey"], model.model_key)
            self.assertEqual(payload["providerModelKey"], model.model_key)
            self.assertEqual(payload["protocol"], "fal-image")
            self.assertEqual(payload["capability"], "image")
            self.assertTrue(payload["enabled"])

    def test_price_rows_are_keyed_by_channel_and_vendor_model(self):
        model = model_by_key("openai/gpt-image-2")
        for tier, usd in model.price_tiers:
            row = seed.price_payload(model, tier, usd, "CHANNEL_000012")
            self.assertEqual(row["modelKey"], "CHANNEL_000012::openai/gpt-image-2")
            self.assertEqual(row["priceTier"], tier)
            self.assertEqual(row["sellUnitPrice"], REPLICATE_SELL_FEN[model.model_key][tier])
            self.assertEqual(row["upstreamUnitPrice"], seed.upstream_fen(usd))
            self.assertEqual(row["vendorCode"], "fal")


if __name__ == "__main__":
    unittest.main()
