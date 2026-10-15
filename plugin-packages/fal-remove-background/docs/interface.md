# fal.ai Remove Background 接口字段

## 协议身份

- 插件 ID：`fal-remove-background`。
- Provider ID：`fal-remove-background`。
- 能力：`image`。
- 默认 Base URL：`https://fal.run`。
- 鉴权驱动：`bearer`（`Authorization: Bearer <apiKey>`）。
- 创建：`POST /fal-ai/birefnet/v2`，同步返回最终图片。

## 为什么路径是写死的

`fal-image` 那个插件的路径由模型名拼出来（`/{model}/edit`），因为 fal 的生图模型都遵循这层命名。BiRefNet 不是生图模型，它是 fal 目录里的一条独立 app 端点：

```
POST https://fal.run/fal-ai/birefnet/v2
```

把它当成模型名去拼（`/fal-ai/birefnet/v2/edit`）只会拿到 404。所以本插件的 `create.path` 是一个常量字符串，请求里的 `model` 只参与渠道模型登记与计费，不进 URL。

登记在渠道上的模型标识就取上游端点名 `fal-ai/birefnet/v2`：价目行按「渠道 + 模型标识」归属，标识与上游对得上，对账时不用再翻一层映射表。

## 请求

根层只有一个字段：

| 字段 | 来源 | 说明 |
| --- | --- | --- |
| `image_url` | `request.images[0]` | 待抠图的原图，只取第一张。 |

`images` 声明为必填，并且有一条校验规则挡住空输入（`$gt [$len request.images] 0`）——抠图没有原图这件事本身就不成立，白跑一次上游请求只会让用户等到一个必然失败的错。

图片值可以是公网 URL，也可以是 `data:` 内联：插件声明了 `requiresPublicMediaUrls: false`，宿主会把本地素材转成 data URL 直接塞进 `image_url`。**这条路径已实测**：9.7MB 的 4096×2731 data URL 能正常出图，所以平台不需要为这个功能额外暴露素材地址。

插件不发送 `prompt`。上游只接受 `image_url`（外加可选的 `operating_resolution` 等分段参数），多发一个提示词字段属于自造协议。

其余厂商字段走 `providerOptions.fal-remove-background.input`，会被浅合并进根层请求体；这个逃生口不对用户开放，只在联调时用。

## 响应

上游同步返回：

```json
{"image": {"url": "https://v3b.fal.media/files/.../out.png", "content_type": "image/png", "width": 4096, "height": 2731}, "mask_image": null}
```

插件的响应映射：

| 目标 | 来源 | 说明 |
| --- | --- | --- |
| `status` | `response.image.url` 非空则 `completed`，否则 `failed` | 同步端点，响应体里有没有图就是成败本身。 |
| `images` | `[{ url }]` | 只有一张。上游同时回了宽高，但渠道模型的产物尺寸由宿主下载真实文件后读取，这里透传不到，就不写进来冒充已映射。 |
| `message` | `response.error.message` / `response.message` / `response.fail_reason` / `detail` | 失败时给用户看的那句话。 |

`resultEphemeral: true`：fal 返回的是自家的临时媒体地址，宿主必须立刻下载到本地资源库，不能把这个 URL 存进任务产物——地址过期之后用户看到的就是一张死图。

上游还有 `mask_image` 字段（前景蒙版），本插件不透传：用户要的是抠好的图，多存一份蒙版只是多占一份磁盘。

## 为什么调用方不做有损重编码

输出分辨率等于输入分辨率，不放大，所以没有超分那种"结果涨到十几兆"的问题：

| 输入 | 输出 | 体积 |
| --- | --- | --- |
| 1024² | 1024² 带 alpha PNG | 约 0.58 MB |
| 4096×2731 | 4096×2731 带 alpha PNG | 约 2.74 MB |

体积可控，且透明边缘对压缩极敏感：有损重编码会让主体轮廓出现灰边和方块噪点，抠图这个功能的交付物正是一圈边缘，所以这里保持 PNG 原样落库。这与 AI 超分相反——那边放大 4 倍后必须重编码，否则单张十几兆。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | API Key，形如 `fal_sk_<key_id>:<secret>`，与 `fal-image`、`fal-upscale` 插件用的是同一个 fal 账号。 |

<!-- BEEFTV_PLUGIN_MANIFEST_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "beeftv.plugin/v2",
  "id": "fal-remove-background",
  "name": "fal.ai Remove Background",
  "version": "1.0.0",
  "author": "BeefTV Contributors",
  "description": "fal.ai 抠图协议插件：固定端点 POST /fal-ai/birefnet/v2，输入一张原图，输出背景透明（带 alpha 通道）的 PNG，分辨率不变。",
  "permissions": [
    "generation.run",
    "media.read"
  ],
  "configuration": {
    "fields": [
      {
        "name": "apiKey",
        "type": "secret",
        "label": "API Key",
        "required": true
      }
    ]
  },
  "contributes": {
    "providers": [
      {
        "id": "fal-remove-background",
        "label": "fal.ai Remove Background",
        "capabilities": [
          "image"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "https://fal.run",
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "images",
            "type": "media[]",
            "required": true,
            "mapping": "image_url",
            "description": "待抠图的原图，只取第一张；BiRefNet 只接受一张输入。"
          },
          {
            "name": "providerOptions",
            "type": "object",
            "required": false,
            "mapping": "provider-specific fields",
            "description": "插件命名空间内的厂商扩展字段。"
          }
        ],
        "validations": [
          {
            "assert": {
              "$gt": [
                {
                  "$len": {
                    "$ref": "request.images"
                  }
                },
                0
              ]
            },
            "message": "移除背景必须带一张原图"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/fal-ai/birefnet/v2",
          "originPath": true,
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "image_url": {
                  "$first": {
                    "$map": {
                      "from": {
                        "$ref": "request.images"
                      },
                      "as": "media",
                      "in": {
                        "$ref": "media.value"
                      }
                    }
                  }
                }
              },
              {
                "$omitEmpty": {
                  "$ref": "request.providerOptions.fal-remove-background.input"
                }
              }
            ]
          }
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.request_id"
              },
              {
                "$ref": "response.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$switch": {
              "cases": [
                {
                  "when": {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.image.url"
                        }
                      },
                      0
                    ]
                  },
                  "then": "completed"
                }
              ],
              "default": "failed"
            }
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.fail_reason"
              },
              {
                "$if": {
                  "condition": {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "response.detail"
                        }
                      },
                      0
                    ]
                  },
                  "then": {
                    "$json": {
                      "$ref": "response.detail"
                    }
                  }
                }
              }
            ]
          },
          "images": [
            {
              "url": {
                "$ref": "response.image.url"
              }
            }
          ],
          "errorPaths": [
            "error.code"
          ],
          "resultEphemeral": true
        }
      }
    ]
  },
  "documentation": "<当前插件的完整 documentation，由 README.md 与 docs/interface.md 拼接而成；为避免 JSON 递归，此处不重复展开正文。>"
}
```
<!-- BEEFTV_PLUGIN_MANIFEST_END -->
