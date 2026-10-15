# Volcengine Ark Agent Plan Seedream Images 接口字段

## 协议身份

- 插件 ID：`volcengine-ark-agent-plan-seedream`。
- Provider ID：`volcengine-ark-agent-plan-image`。
- 能力：`image`。
- 默认 Base URL：`https://ark.cn-beijing.volces.com`。
- 鉴权驱动：`bearer`。
- 创建：`POST /api/plan/v3/images/generations`。
- 生命周期：同步响应。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | API Key |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `model` | 图片模型 ID。 |
| `prompt` | string | 是 | `prompt` | 图片提示词。 |
| `images` | media[] | 否 | `provider image/reference fields` | 参考图或编辑源图，role 由业务层确定。 |
| `imageCount` | integer | 否 | `n/sample_count` | 输出数量。 |
| `aspectRatio` | string | 否 | `size/aspect_ratio` | 比例或尺寸，语义按协议说明。 |
| `resolution` | string | 否 | `resolution/imageSize` | 分辨率档位。 |
| `quality` | string | 否 | `quality` | 质量档位。 |
| `providerOptions` | object | 否 | `provider-specific fields` | 插件命名空间内的厂商扩展字段。 |

## 上游请求模板逐字段清单

下表由插件请求模板生成，覆盖 body、query、headers 和 multipart 文件声明中的每个字段。

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.path` | `"/api/plan/v3/images/generations"` |
| `create.contentType` | `"application/json"` |
| `create.body.model` | `{"$ref":"request.model"}` |
| `create.body.prompt` | `{"$ref":"request.prompt"}` |
| `create.body.size` | `{"$omitEmpty":{"$switch":{"cases":[{"when":{"$eq":[{"$upper":{"$trim":{"$ref":"request.aspectRatio"}}},"AUTO"]},"then":"2K"},{"when":{"$in":[{"$upper":{"$trim":{"$ref":"request.aspectRatio"}}},["1K","1.5K","2K","3K","4K"]]},"then":{"$upper":{"$trim":{"$ref":"request.aspectRatio"}}}},{"when":{"$eq":[{"$trim":{"$ref":"request.aspectRatio"}},"1:1"]},"then":"2048x2048"},{"when":{"$eq":[{"$trim":{"$ref":"request.aspectRatio"}},"4:3"]},"then":"2368x1776"},{"when":{"$eq":[{"$trim":{"$ref":"request.aspectRatio"}},"3:4"]},"then":"1776x2368"},{"when":{"$eq":[{"$trim":{"$ref":"request.aspectRatio"}},"16:9"]},"then":"2816x1584"},{"when":{"$eq":[{"$trim":{"$ref":"request.aspectRatio"}},"9:16"]},"then":"1584x2816"},{"when":{"$eq":[{"$trim":{"$ref":"request.aspectRatio"}},"3:2"]},"then":"2496x1664"},{"when":{"$eq":[{"$trim":{"$ref":"request.aspectRatio"}},"2:3"]},"then":"1664x2496"},{"when":{"$eq":[{"$trim":{"$ref":"request.aspectRatio"}},"21:9"]},"then":"3136x1344"}],"default":{"$trim":{"$ref":"request.aspectRatio"}}}}}` |
| `create.body.image` | `{"$omitEmpty":{"$if":{"condition":{"$eq":[{"$len":{"$ref":"request.images"}},1]},"then":{"$first":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}},"else":{"$if":{"condition":{"$gt":[{"$len":{"$ref":"request.images"}},1]},"then":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}},"else":null}}}}}` |
| `create.body.layer_decomposition` | `{"$omitEmpty":{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.layer_decomposition"}}` |
| `create.body.optimize_prompt_options` | `{"$omitEmpty":{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.optimize_prompt_options"}}` |
| `create.body.output_format` | `{"$coalesce":[{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.output_format"},"jpeg"]}` |
| `create.body.background` | `{"$omitEmpty":{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.background"}}` |
| `create.body.tools` | `{"$omitEmpty":{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.tools"}}` |
| `create.body.sequential_image_generation` | `{"$omitEmpty":{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.sequential_image_generation"}}` |
| `create.body.sequential_image_generation_options` | `{"$omitEmpty":{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.sequential_image_generation_options"}}` |
| `create.body.watermark` | `{"$coalesce":[{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.watermark"},{"$ref":"request.watermark"},false]}` |
| `create.body.response_format` | `{"$coalesce":[{"$ref":"request.providerOptions.volcengine-ark-agent-plan-image.response_format"},"b64_json"]}` |

## Provider 扩展键

- `providerOptions.volcengine-ark-agent-plan-image.background`
- `providerOptions.volcengine-ark-agent-plan-image.layer_decomposition`
- `providerOptions.volcengine-ark-agent-plan-image.optimize_prompt_options`
- `providerOptions.volcengine-ark-agent-plan-image.output_format`
- `providerOptions.volcengine-ark-agent-plan-image.response_format`
- `providerOptions.volcengine-ark-agent-plan-image.sequential_image_generation`
- `providerOptions.volcengine-ark-agent-plan-image.sequential_image_generation_options`
- `providerOptions.volcengine-ark-agent-plan-image.tools`
- `providerOptions.volcengine-ark-agent-plan-image.watermark`

动态模型或工作流允许使用文档声明的完整 `parameters/input/extra_body` 对象；该对象是协议本身的开放 schema，不会被宿主裁剪。

## 响应映射逐字段清单

| 映射位置 | 上游路径或转换表达式 |
| --- | --- |
| `response.status` | `"succeeded"` |
| `response.images` | `{"$map":{"from":{"$ref":"response.data"},"as":"item","in":{"url":{"$omitEmpty":{"$ref":"item.url"}},"dataUrl":{"$if":{"condition":{"$ref":"item.b64_json"},"then":{"$concat":["data:",{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$coalesce":[{"$ref":"item.output_format"},"jpeg"]}},"png"]},"then":"image/png"}],"default":"image/jpeg"}},";base64,",{"$ref":"item.b64_json"}]},"else":null}}}}}` |
| `response.usage` | `{"$ref":"response.usage"}` |
| `response.errorPaths[0]` | `"error.code"` |
| `response.messagePaths[0]` | `"error.message"` |
| `response.resultEphemeral` | `true` |

## 响应与错误

插件把上游 task/status/text/media/usage 映射为统一结果。临时媒体 URL 标记为 ephemeral，由宿主立即下载持久化。HTTP 错误、业务 code 和 error object 保持失败语义，不包装成成功。

## 兼容边界

Agent Plan 专属接入：请求路径为 /api/plan/v3/images/generations，必须使用 Agent Plan 控制台专属 API Key；请求体与官方 Seedream 协议一致，但凭证与额度按 AFP 套餐结算，不能与 /api/v3 官方 Key 混用。官方 Ark 推理接入：Base URL 使用 /api/v3，API Key 来自方舟推理接入控制台。请求体对齐官方图片生成 API：size 接受分辨率档位（1K/1.5K/2K/3K/4K）或 宽x高，二者不可混用，比例串按 2K 档换算，auto 落 2K；layer_decomposition、optimize_prompt_options、background、tools 从 providerOptions 按需透传，缺省时交给官方默认值；官方 stream 需要 SSE 解包，本插件按同步 JSON 解析响应，因此不下发。两处与官方默认值的有意差异：watermark 默认 false（平台不对用户图片加 AI 生成水印）、response_format 默认 b64_json（避免把会过期的下载链接当产物）。output_format 也走 providerOptions，但插件会显式补上官方默认的 jpeg：官方响应不回传这个字段，产物 MIME 只能按插件下发的值标注，不显式下发就会把 JPEG 标成 PNG。改这个值（例如透明背景需要 png）时，产物标注不会自动跟着变，需要一并改这里。5.0 pro / flash 不支持组图与联网搜索，sequential_image_generation 与 tools 只对 5.0 lite 及更早型号有意义。

<!-- BEEFTV_PLUGIN_MANIFEST_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "beeftv.plugin/v2",
  "id": "volcengine-ark-agent-plan-seedream",
  "name": "Volcengine Ark Agent Plan Seedream Images",
  "version": "2.0.0",
  "author": "BeefTV Contributors",
  "description": "Volcengine Ark Agent Plan Seedream Images 独立请求协议插件。",
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
        "id": "volcengine-ark-agent-plan-image",
        "label": "Volcengine Ark Agent Plan Seedream Images",
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
        "baseUrl": "https://ark.cn-beijing.volces.com",
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
            "description": "图片模型 ID。"
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
            "mapping": "provider image/reference fields",
            "description": "参考图或编辑源图，role 由业务层确定。"
          },
          {
            "name": "imageCount",
            "type": "integer",
            "required": false,
            "mapping": "n/sample_count",
            "description": "输出数量。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "required": false,
            "mapping": "size/aspect_ratio",
            "description": "比例或尺寸，语义按协议说明。"
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
            "description": "质量档位。"
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
          "path": "/api/plan/v3/images/generations",
          "contentType": "application/json",
          "body": {
            "model": {
              "$ref": "request.model"
            },
            "prompt": {
              "$ref": "request.prompt"
            },
            "size": {
              "$omitEmpty": {
                "$switch": {
                  "cases": [
                    {
                      "when": {
                        "$eq": [
                          {
                            "$upper": {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            }
                          },
                          "AUTO"
                        ]
                      },
                      "then": "2K"
                    },
                    {
                      "when": {
                        "$in": [
                          {
                            "$upper": {
                              "$trim": {
                                "$ref": "request.aspectRatio"
                              }
                            }
                          },
                          [
                            "1K",
                            "1.5K",
                            "2K",
                            "3K",
                            "4K"
                          ]
                        ]
                      },
                      "then": {
                        "$upper": {
                          "$trim": {
                            "$ref": "request.aspectRatio"
                          }
                        }
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
                          "1:1"
                        ]
                      },
                      "then": "2048x2048"
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
                      "then": "2368x1776"
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
                      "then": "1776x2368"
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
                      "then": "2816x1584"
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
                      "then": "1584x2816"
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
                      "then": "2496x1664"
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
                      "then": "1664x2496"
                    },
                    {
                      "when": {
                        "$eq": [
                          {
                            "$trim": {
                              "$ref": "request.aspectRatio"
                            }
                          },
                          "21:9"
                        ]
                      },
                      "then": "3136x1344"
                    }
                  ],
                  "default": {
                    "$trim": {
                      "$ref": "request.aspectRatio"
                    }
                  }
                }
              }
            },
            "image": {
              "$omitEmpty": {
                "$if": {
                  "condition": {
                    "$eq": [
                      {
                        "$len": {
                          "$ref": "request.images"
                        }
                      },
                      1
                    ]
                  },
                  "then": {
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
                  },
                  "else": {
                    "$if": {
                      "condition": {
                        "$gt": [
                          {
                            "$len": {
                              "$ref": "request.images"
                            }
                          },
                          1
                        ]
                      },
                      "then": {
                        "$map": {
                          "from": {
                            "$ref": "request.images"
                          },
                          "as": "media",
                          "in": {
                            "$ref": "media.value"
                          }
                        }
                      },
                      "else": null
                    }
                  }
                }
              }
            },
            "layer_decomposition": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.layer_decomposition"
              }
            },
            "optimize_prompt_options": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.optimize_prompt_options"
              }
            },
            "output_format": {
              "$coalesce": [
                {
                  "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.output_format"
                },
                "jpeg"
              ]
            },
            "background": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.background"
              }
            },
            "tools": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.tools"
              }
            },
            "sequential_image_generation": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.sequential_image_generation"
              }
            },
            "sequential_image_generation_options": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.sequential_image_generation_options"
              }
            },
            "watermark": {
              "$coalesce": [
                {
                  "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.watermark"
                },
                {
                  "$ref": "request.watermark"
                },
                false
              ]
            },
            "response_format": {
              "$coalesce": [
                {
                  "$ref": "request.providerOptions.volcengine-ark-agent-plan-image.response_format"
                },
                "b64_json"
              ]
            }
          }
        },
        "response": {
          "status": "succeeded",
          "images": {
            "$map": {
              "from": {
                "$ref": "response.data"
              },
              "as": "item",
              "in": {
                "url": {
                  "$omitEmpty": {
                    "$ref": "item.url"
                  }
                },
                "dataUrl": {
                  "$if": {
                    "condition": {
                      "$ref": "item.b64_json"
                    },
                    "then": {
                      "$concat": [
                        "data:",
                        {
                          "$switch": {
                            "cases": [
                              {
                                "when": {
                                  "$eq": [
                                    {
                                      "$lower": {
                                        "$coalesce": [
                                          {
                                            "$ref": "item.output_format"
                                          },
                                          "jpeg"
                                        ]
                                      }
                                    },
                                    "png"
                                  ]
                                },
                                "then": "image/png"
                              }
                            ],
                            "default": "image/jpeg"
                          }
                        },
                        ";base64,",
                        {
                          "$ref": "item.b64_json"
                        }
                      ]
                    },
                    "else": null
                  }
                }
              }
            }
          },
          "usage": {
            "$ref": "response.usage"
          },
          "errorPaths": [
            "error.code"
          ],
          "messagePaths": [
            "error.message"
          ],
          "resultEphemeral": true
        }
      }
    ]
  }
}
```
<!-- BEEFTV_PLUGIN_MANIFEST_END -->
