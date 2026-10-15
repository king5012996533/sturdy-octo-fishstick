# fal.ai Remove Background

该目录是 BeefTV 声明式协议插件源码。后端从生成的 `fal-remove-background.beeftv-plugin` 包加载，不依赖系统内置 `host:` 适配器。

本插件由 BeefTV Contributors 维护，用于适配 fal.ai 相关服务接口；不表示 BeefTV 与该服务商存在隶属、授权或合作关系。

完整接口见 [docs/interface.md](docs/interface.md)。

它只做一件事：把一张图交给 fal 的 `fal-ai/birefnet/v2`，拿回背景透明（带 alpha 通道）的 PNG。端点写死在 `create.path` 里，不按模型名拼路径——BiRefNet 是 fal 的独立 app 端点，拼成 `{model}/edit` 之类的形式会 404。

它不改尺寸：输出分辨率等于输入分辨率，没有倍率可调。上游固定回 PNG（带 alpha），所以调用方拿到结果后**不要**做有损重编码——透明边缘被有损压过一次就会出现灰边，这与 AI 超分正好相反（那边必须重编码来压体积）。详见 docs/interface.md 的「为什么调用方不做有损重编码」。
