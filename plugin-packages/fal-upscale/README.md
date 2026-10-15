# fal.ai Upscale

该目录是 BeefTV 声明式协议插件源码。后端从生成的 `fal-upscale.beeftv-plugin` 包加载，不依赖系统内置 `host:` 适配器。

本插件由 BeefTV Contributors 维护，用于适配 fal.ai 相关服务接口；不表示 BeefTV 与该服务商存在隶属、授权或合作关系。

完整接口见 [docs/interface.md](docs/interface.md)。

它只做一件事：把一张图交给 fal 的 `fal-ai/recraft/upscale/crisp`，拿回放大 4 倍的结果。端点写死在 `create.path` 里，不按模型名拼路径——Crisp 是 fal 的独立 app 端点，拼成 `{model}/edit` 之类的形式会 404。

放大倍率是上游写死的 4×，没有可调参数；输出是无损 WebP，体积涨得比像素快（1K 输入约 4.2MB，2K 输入约 16.7MB），因此调用方拿到结果后需要按目标长边重新编码，见 docs/interface.md 的「为什么调用方要重编码」。
