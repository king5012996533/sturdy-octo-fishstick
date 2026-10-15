# fal.ai Image 接口字段

## 协议身份

- 插件 ID：`fal-image`。
- Provider ID：`fal-image`。
- 能力：`image`。
- 默认 Base URL：`https://fal.run`。
- 鉴权驱动：`bearer`（`Authorization: Bearer <apiKey>`）。
- 创建：`POST /{model}/edit`（带参考图）或 `POST /{model}`／`POST /{model}/text-to-image`（不带参考图），同步返回最终图片。
  路径由 `pathTemplate` 拼接，不能用 `{{model}}` 占位：模型 ID 里的斜杠会被转义成 `%2F`。

## 文生图与图生图是两条端点

fal 把同一个模型的能力拆成两条端点，`openai/gpt-image-2.5/sunburst` 就是典型：

| 端点 | fal 的分类 | 入参要求 |
| --- | --- | --- |
| `{model}/text-to-image` | text-to-image | 只吃 `prompt`，收了 `image_urls` 反而报错 |
| `{model}/edit` | image-to-image | 必填 `image_urls`，实测缺字段与空数组都是 422 |

对用户来说这是一个模型的两条路，不该在模型列表里拆成两个。插件按请求里有没有参考图自动选端点：

- 有参考图 → `POST /{model}/edit`
- 没有参考图 → `POST /{model}/text-to-image`，若模型属于"文生图端点就是基名"的那一类，则退回 `POST /{model}`

所以渠道模型登记的是**基名**（`openai/gpt-image-2.5/sunburst`），不带 `/edit` 后缀；
这与 `openai-images` 按参考图在 `/v1/images/edits` 与 `/v1/images/generations` 之间切换是同一套写法。
两条端点在上游同一套计费口径下（都按 `quality` 与像素计价），因此共用一份价目。

### 文生图的两种命名，图生图只有一种

fal 目录里文生图路径有两套写法，`/edit` 侧则一律是 `{model}/edit`：

| 文生图端点 | 例子 | 插件行为 |
| --- | --- | --- |
| `{model}/text-to-image` | `openai/gpt-image-2.5/sunburst` | 默认路径 |
| `{model}`（基名本身） | `openai/gpt-image-2` | 列在插件的 `BASE_PATH_TEXT_TO_IMAGE` 名单里 |

名单写在 `create.pathTemplate` 的 `$switch` 里（`openai/gpt-image-2` 一项）。
判断依据是实测：`POST https://fal.run/openai/gpt-image-2/text-to-image` 回
`404 {"detail":"Path /text-to-image not found"}`，而 `POST https://fal.run/openai/gpt-image-2`
用同一份根层 body 正常出图；`/edit` 侧两套命名都是 `{model}/edit`，无需分支。

加新模型时先确认它属于哪一套：拿基名分别试 `{model}` 与 `{model}/text-to-image`，
前者 200 说明要把它加进名单，否则渠道模型一上架就会在文生图路径上 404。

## 为什么用同步端点而不是队列端点

fal 的队列端点（`https://queue.fal.run`）需要三步：提交拿 `request_id`、轮询状态、再取 `response_url` 拿结果。
实测 `GET /{owner}/{app}/requests/{id}/status` 只回状态、不回图片，而带结果的 `GET /{owner}/{app}/requests/{id}` 在任务未完成时返回 HTTP 400。
本项目的声明式插件只能声明"创建 + 轮询 + 结果下载"，没有"轮询成功后按 JSON 里的 URL 再取一次"的编排能力，
而且轮询遇到非 2xx 会直接判定任务失败，因此队列端点在本插件的执行模型里走不通。

`POST https://fal.run/{model}/...` 是 fal 的同步端点：随请求阻塞到出图，响应就是最终结果。生图（含 4K）实测在 20 秒到 2 分钟量级，
宿主的上游超时是 5 分钟，够用。这也是本项目其它生图插件（`openai-images`、`tencent-hunyuan-image-35`、`agnes-image`）的既有做法。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | API Key，形如 `fal_sk_<key_id>:<secret>` |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | 请求路径 | fal 的模型基名，直接拼进 URL，如 `openai/gpt-image-2.5/sunburst`；端点由参考图决定。 |
| `prompt` | string | 是 | `prompt` | 图片提示词。 |
| `images` | media[] | 否 | `image_urls` | 参考图。有值走 `/edit`（图生图），无值走 `/text-to-image`（文生图），fal 上限 16 张。 |
| `imageCount` | integer | 否 | `num_images` | 输出数量。 |
| `aspectRatio` | string | 否 | `image_size` | 见下方比例映射表。 |
| `quality` | string | 否 | `quality` | `auto`/`low`/`medium`/`high`/`xhigh`/`max`，不在白名单内时不下发。 |
| `providerOptions` | object | 否 | 请求体根层字段 | 见下方扩展键。 |

`resolution` 是宿主保留字段，本插件不下发：fal 用 `image_size` 同时表达比例与档位。

## 比例映射

fal 的 `image_size` 既接受枚举，也接受 `{width, height}` 对象。插件按下面的表换算 `aspectRatio`：

| 统一比例 | 上游 `image_size` |
| --- | --- |
| `1:1` | `square_hd` |
| `16:9` | `landscape_16_9` |
| `9:16` | `portrait_16_9` |
| `4:3` | `landscape_4_3` |
| `3:4` | `portrait_4_3` |
| `3:2` | `{"width": 1536, "height": 1024}` |
| `2:3` | `{"width": 1024, "height": 1536}` |
| `宽x高`（如 `2752x1536`） | `{"width": 2752, "height": 1536}` |
| 其它（含 `auto`、空值） | 不下发，由 fal 用自己的默认值 |

## 上游请求模板逐字段清单

下表由插件请求模板生成，覆盖 body、query、headers 和 multipart 文件声明中的每个字段。

真正下发的请求体是"多个片段按顺序合并"的结果：后面的片段覆盖前面的同名字段。
函数式插件把 `providerOptions["fal-image"]["input"]` 放在最后一段，因此它可以覆盖上面任何一个字段，
也可以补 fal 的其它入参（例如 `output_format`、`enable_safety_checker`、`mask_url`、`background`）。

## Provider 扩展键

- `providerOptions.fal-image.input`：直接并入请求体根层，优先级最高。

fal 各模型的入参名并不统一（`image_urls` / `images`、`num_images` / `num_inference_steps` 等），
换模型时用这个键补齐差异，不用改插件。

## 响应映射逐字段清单

响应映射把 `{"images": [{"url": ...}]}` 直接交给宿主下载，不做二次取数。

## 响应与错误

插件按响应体判定状态：`images` 非空即视为完成，否则视为失败，并把 `detail`（fal 的校验错误是数组）序列化进错误文案。
非 2xx 响应由宿主判定失败，正文会原样带进错误信息。

<!-- BEEFTV_PLUGIN_MANIFEST_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "beeftv.plugin/v2",
  "id": "fal-image",
  "name": "fal.ai Image",
  "version": "1.0.0",
  "author": "BeefTV Contributors",
  "description": "fal.ai 生图协议插件，按 fal.run 同步端点逐字段构造请求。",
  "documentation": "<当前插件的完整 documentation，由 README.md 与 docs/interface.md 拼接而成；为避免 JSON 递归，此处不重复展开正文。>",
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
        "id": "fal-image",
        "label": "fal.ai Image",
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
            "name": "model",
            "type": "string",
            "required": true,
            "mapping": "model",
            "description": "图片模型基名，如 openai/gpt-image-2.5/sunburst；端点由参考图决定，模型名里不带 /edit。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "图片提示词。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "image_urls",
            "description": "参考图，最多 16 张。有参考图走 /edit（图生图），没有走 /text-to-image（文生图）。"
          },
          {
            "name": "imageCount",
            "type": "integer",
            "required": false,
            "mapping": "num_images",
            "description": "输出数量。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "required": false,
            "mapping": "image_size",
            "description": "输出比例，按插件映射规则转成 fal 的 image_size。"
          },
          {
            "name": "resolution",
            "type": "string",
            "required": false,
            "mapping": "resolution/imageSize",
            "description": "分辨率档位。"
          },
          {
            "name": "quality",
            "type": "string",
            "required": false,
            "mapping": "quality",
            "description": "质量档位：auto/low/medium/high/xhigh/max。"
          },
          {
            "name": "providerOptions",
            "type": "object",
            "required": false,
            "mapping": "provider-specific fields",
            "description": "插件命名空间内的厂商扩展字段。"
          }
        ],
        "create": {
          "method": "POST",
          "originPath": true,
          "pathTemplate": {
            "$concat": [
              "/",
              {
                "$ref": "request.model"
              },
              {
                "$if": {
                  "condition": {
                    "$gt": [
                      {
                        "$len": {
                          "$ref": "request.images"
                        }
                      },
                      0
                    ]
                  },
                  "then": "/edit",
                  "else": {
                    "$switch": {
                      "cases": [
                        {
                          "when": {
                            "$in": [
                              {
                                "$ref": "request.model"
                              },
                              [
                                "openai/gpt-image-2"
                              ]
                            ]
                          },
                          "then": ""
                        }
                      ],
                      "default": "/text-to-image"
                    }
                  }
                }
              }
            ]
          },
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "prompt": {
                  "$ref": "request.prompt"
                }
              },
              {
                "image_urls": {
                  "$omitEmpty": {
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
                "num_images": {
                  "$if": {
                    "condition": {
                      "$gt": [
                        {
                          "$ref": "request.imageCount"
                        },
                        0
                      ]
                    },
                    "then": {
                      "$ref": "request.imageCount"
                    }
                  }
                }
              },
              {
                "quality": {
                  "$omitEmpty": {
                    "$switch": {
                      "cases": [
                        {
                          "when": {
                            "$in": [
                              {
                                "$lower": {
                                  "$trim": {
                                    "$ref": "request.quality"
                                  }
                                }
                              },
                              [
                                "auto",
                                "low",
                                "medium",
                                "high",
                                "xhigh",
                                "max"
                              ]
                            ]
                          },
                          "then": {
                            "$lower": {
                              "$trim": {
                                "$ref": "request.quality"
                              }
                            }
                          }
                        }
                      ]
                    }
                  }
                }
              },
              {
                "image_size": {
                  "$switch": {
                    "cases": [
                      {
                        "when": {
                          "$eq": [
                            {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            },
                            "1:1"
                          ]
                        },
                        "then": "square_hd"
                      },
                      {
                        "when": {
                          "$eq": [
                            {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            },
                            "16:9"
                          ]
                        },
                        "then": "landscape_16_9"
                      },
                      {
                        "when": {
                          "$eq": [
                            {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            },
                            "9:16"
                          ]
                        },
                        "then": "portrait_16_9"
                      },
                      {
                        "when": {
                          "$eq": [
                            {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            },
                            "4:3"
                          ]
                        },
                        "then": "landscape_4_3"
                      },
                      {
                        "when": {
                          "$eq": [
                            {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            },
                            "3:4"
                          ]
                        },
                        "then": "portrait_4_3"
                      },
                      {
                        "when": {
                          "$eq": [
                            {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            },
                            "3:2"
                          ]
                        },
                        "then": {
                          "width": 1536,
                          "height": 1024
                        }
                      },
                      {
                        "when": {
                          "$eq": [
                            {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            },
                            "2:3"
                          ]
                        },
                        "then": {
                          "width": 1024,
                          "height": 1536
                        }
                      },
                      {
                        "when": {
                          "$gt": [
                            {
                              "$len": {
                                "$split": [
                                  {
                                    "$trim": {
                                      "$ref": "request.aspectRatio"
                                    }
                                  },
                                  "x"
                                ]
                              }
                            },
                            1
                          ]
                        },
                        "then": {
                          "width": {
                            "$toInt": {
                              "$at": [
                                {
                                  "$split": [
                                    {
                                      "$trim": {
                                        "$ref": "request.aspectRatio"
                                      }
                                    },
                                    "x"
                                  ]
                                },
                                0
                              ]
                            }
                          },
                          "height": {
                            "$toInt": {
                              "$at": [
                                {
                                  "$split": [
                                    {
                                      "$trim": {
                                        "$ref": "request.aspectRatio"
                                      }
                                    },
                                    "x"
                                  ]
                                },
                                1
                              ]
                            }
                          }
                        }
                      }
                    ]
                  }
                }
              },
              {
                "$omitEmpty": {
                  "$ref": "request.providerOptions.fal-image.input"
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
                          "$ref": "response.images"
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
          "images": {
            "$ref": "response.images"
          },
          "errorPaths": [
            "error.code"
          ],
          "resultEphemeral": true
        }
      }
    ]
  }
}
```
<!-- BEEFTV_PLUGIN_MANIFEST_END -->
