# fal.ai Image

该目录是 BeefTV 声明式协议插件源码。后端从生成的 `fal-image.beeftv-plugin` 包加载，不依赖系统内置 `host:` 适配器。

本插件由 BeefTV Contributors 维护，用于适配 fal.ai 相关服务接口；不表示 BeefTV 与该服务商存在隶属、授权或合作关系。

完整接口见 [docs/interface.md](docs/interface.md)。

fal 把同一个模型的文生图与图生图拆成两条端点（后者必填参考图）。插件按请求里有没有参考图自动选端点，所以渠道上只需要登记一个模型基名，用户在画布上既可以直接文生图，也可以拖参考图改图。

`/edit` 侧一律是 `{model}/edit`；文生图侧 fal 有两套命名，多数模型是 `{model}/text-to-image`（`openai/gpt-image-2.5/sunburst`），另一些模型的文生图端点就是基名本身（`openai/gpt-image-2`，试 `/text-to-image` 会回 404）。插件里用一份名单区分这两套，见 docs/interface.md。
