# Replicate Predictions Image 接口字段

## 协议身份

- 插件 ID：`replicate-prediction-image`。
- Provider ID：`replicate-prediction-image`。
- 能力：`image`。
- 默认 Base URL：`https://api.replicate.com`。
- 鉴权驱动：`bearer`。
- 创建：`POST /v1/models/{owner}/{name}/predictions`（模型作用域端点）。
- 查询：`GET /v1/predictions/{{taskId}}`。
- 取消：`POST /v1/predictions/{{taskId}}/cancel`。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | API Key |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `path /v1/models/{model}/predictions` | 上游模型标识，owner/name 形式，进请求路径。 |
| `prompt` | string | 是 | `prompt` | 图片提示词。 |
| `images` | media[] | 否 | `input.images` / `input.input_images` | 参考图或编辑源图；支持数组参考的模型（flux-2 klein、seedream）内联转发，OpenAI 图片族（`openai/gpt-image-*`）改发 `input_images`。 |
| `imageCount` | integer | 否 | `input.num_outputs` / `input.max_images` / `input.number_of_images` | 输出数量；按模型支持的同义字段并列下发，未知字段由上游忽略。 |
| `aspectRatio` | string | 否 | `input.aspect_ratio` | 宽高比；能力合同已把尺寸归一成比例，插件直通上游。 |
| `resolution` | string | 否 | `input.image_size` / `input.size` | 分辨率档位；仅 Google（1K/2K）与 ByteDance（1K/2K/4K）图片模型使用。 |
| `quality` | string | 否 | `input.quality`（OpenAI 图片族）/ `input.image_size` / `input.size` | OpenAI 图片族直通 `input.quality`（low/medium/high/xhigh/max/auto，实际可选档以能力合同为准）；Google、ByteDance 仍由档位字符串决定分辨率字段。 |
| `providerOptions` | object | 否 | `provider-specific fields` | 插件命名空间内的厂商扩展字段。 |

## 上游请求模板逐字段清单

下表由插件请求模板生成，覆盖 body、query、headers 和 multipart 文件声明中的每个字段。

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.pathTemplate` | `{"$concat":["/v1/models/",{"$ref":"request.model"},"/predictions"]}` |
| `create.contentType` | `"application/json"` |
| `create.body.input` | `{"$omitEmpty":{"$coalesce":[{"$ref":"request.providerOptions.replicate-prediction-image.input"},{"prompt":{"$ref":"request.prompt"},"images":{"$omitEmpty":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}},"videos":{"$omitEmpty":{"$map":{"from":{"$ref":"request.videos"},"as":"media","in":{"$ref":"media.value"}}}},"audios":{"$omitEmpty":{"$map":{"from":{"$ref":"request.audios"},"as":"media","in":{"$ref":"media.value"}}}},"aspect_ratio":{"$omitEmpty":{"$ref":"request.aspectRatio"}},"num_outputs":{"$if":{"condition":{"$gt":[{"$ref":"request.imageCount"},0]},"then":{"$ref":"request.imageCount"}}},"max_images":{"$if":{"condition":{"$gt":[{"$ref":"request.imageCount"},0]},"then":{"$ref":"request.imageCount"}}},"number_of_images":{"$if":{"condition":{"$gt":[{"$ref":"request.imageCount"},0]},"then":{"$ref":"request.imageCount"}}},"image_size":{"$switch":{"cases":[{"when":{"$and":[{"$eq":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},0]},"google"]},{"$in":[{"$lower":{"$trim":{"$ref":"request.quality"}}},["1k","2k"]]}]},"then":{"$upper":{"$trim":{"$ref":"request.quality"}}}}]}},"size":{"$switch":{"cases":[{"when":{"$and":[{"$eq":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},0]},"bytedance"]},{"$in":[{"$lower":{"$trim":{"$ref":"request.quality"}}},["1k","2k","4k"]]}]},"then":{"$upper":{"$trim":{"$ref":"request.quality"}}}}]}},"image_input":{"$omitEmpty":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}},"image":{"$omitEmpty":{"$first":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}}},"input_image":{"$omitEmpty":{"$first":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}}},"image_reference_url":{"$omitEmpty":{"$first":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}}},"image_prompt":{"$omitEmpty":{"$first":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}}},"subject_reference":{"$omitEmpty":{"$first":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}}},"quality":{"$switch":{"cases":[{"when":{"$eq":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},0]},"openai"]},"then":{"$lower":{"$trim":{"$ref":"request.quality"}}}}]}},"moderation":{"$switch":{"cases":[{"when":{"$eq":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},0]},"openai"]},"then":{"$coalesce":[{"$lower":{"$trim":{"$ref":"request.providerOptions.replicate-prediction-image.moderation"}}},"low"]}}]}},"input_images":{"$switch":{"cases":[{"when":{"$eq":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},0]},"openai"]},"then":{"$omitEmpty":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}}}]}}}]}}` |
| `create.body.webhook` | `{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-image.webhook"}}` |
| `create.body.webhook_events_filter` | `{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-image.webhook_events_filter"}}` |
| `poll.method` | `"GET"` |
| `poll.path` | `"/v1/predictions/{{taskId}}"` |
| `poll.contentType` | `"application/json"` |
| `cancel.method` | `"POST"` |
| `cancel.path` | `"/v1/predictions/{{taskId}}/cancel"` |
| `cancel.contentType` | `"application/json"` |

## Provider 扩展键

- `providerOptions.replicate-prediction-image.input`
- `providerOptions.replicate-prediction-image.moderation`
- `providerOptions.replicate-prediction-image.webhook`
- `providerOptions.replicate-prediction-image.webhook_events_filter`

动态模型或工作流允许使用文档声明的完整 `parameters/input/extra_body` 对象；该对象是协议本身的开放 schema，不会被宿主裁剪。

## OpenAI 图片族补充映射

`openai/gpt-image-*` 的输入键与通用 Replicate 图片模型不同，插件按 `request.model` 的 owner 收口，只有 owner 为 `openai` 时才下发以下三个键，其余模型不受影响（`input_images` 与 `output_format`、`background` 同名键在上游是可选字段，未声明时不出现）。

| 统一字段 | 上游键 | 说明 |
| --- | --- | --- |
| `quality` | `input.quality` | 直通上游质量档位 `low` / `medium` / `high` / `xhigh` / `max` / `auto`；创作端 1K/2K/4K 档位映射为 low/medium/high。 |
| `images` | `input.input_images` | 参考图或编辑源图数组；上游键名为 `input_images`，与通用模型的 `images` 不同。 |
| `providerOptions.replicate-prediction-image.moderation` | `input.moderation` | 审核档位 `auto` / `low`；未显式配置时缺省 `low`，用于压低试跑成本。 |

## 响应映射逐字段清单

| 映射位置 | 上游路径或转换表达式 |
| --- | --- |
| `response.taskId` | `{"$coalesce":[{"$ref":"response.id"},{"$ref":"response.request_id"},{"$ref":"response.prompt_id"},{"$ref":"taskId"}]}` |
| `response.status` | `{"$coalesce":[{"$ref":"response.status"},{"$ref":"response.state"},{"$ref":"response.data.status"},"pending"]}` |
| `response.message` | `{"$coalesce":[{"$ref":"response.error.message"},{"$ref":"response.message"},{"$ref":"response.fail_reason"}]}` |
| `response.images` | `{"$ref":"response.output"}` |
| `response.errorPaths[0]` | `"error.code"` |
| `response.resultEphemeral` | `true` |

## 响应与错误

插件把上游 task/status/text/media/usage 映射为统一结果。临时媒体 URL 标记为 ephemeral，由宿主立即下载持久化。HTTP 错误、业务 code 和 error object 保持失败语义，不包装成成功。

## 兼容边界

这是运行时/工作流协议，模型字段由 endpoint、version 或 workflow schema 决定。插件不伪造固定模型字段；providerOptions.input/workflow/prompt 是完整请求对象，并由 conformance fixture 锁定实际接入版本。

<!-- BEEFTV_PLUGIN_MANIFEST_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "beeftv.plugin/v2",
  "id": "replicate-prediction-image",
  "name": "Replicate Predictions Image",
  "version": "2.3.0",
  "author": "BeefTV Contributors",
  "description": "Replicate Predictions Image 独立请求协议插件。",
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
        "id": "replicate-prediction-image",
        "label": "Replicate Predictions Image",
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
        "baseUrl": "https://api.replicate.com",
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
          "pathTemplate": {
            "$concat": [
              "/v1/models/",
              {
                "$ref": "request.model"
              },
              "/predictions"
            ]
          },
          "contentType": "application/json",
          "body": {
            "input": {
              "$omitEmpty": {
                "$coalesce": [
                  {
                    "$ref": "request.providerOptions.replicate-prediction-image.input"
                  },
                  {
                    "prompt": {
                      "$ref": "request.prompt"
                    },
                    "images": {
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
                    },
                    "videos": {
                      "$omitEmpty": {
                        "$map": {
                          "from": {
                            "$ref": "request.videos"
                          },
                          "as": "media",
                          "in": {
                            "$ref": "media.value"
                          }
                        }
                      }
                    },
                    "audios": {
                      "$omitEmpty": {
                        "$map": {
                          "from": {
                            "$ref": "request.audios"
                          },
                          "as": "media",
                          "in": {
                            "$ref": "media.value"
                          }
                        }
                      }
                    },
                    "aspect_ratio": {
                      "$omitEmpty": {
                        "$ref": "request.aspectRatio"
                      }
                    },
                    "num_outputs": {
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
                    },
                    "max_images": {
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
                    },
                    "number_of_images": {
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
                    },
                    "image_size": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$and": [
                                {
                                  "$eq": [
                                    {
                                      "$at": [
                                        {
                                          "$split": [
                                            {
                                              "$ref": "request.model"
                                            },
                                            "/"
                                          ]
                                        },
                                        0
                                      ]
                                    },
                                    "google"
                                  ]
                                },
                                {
                                  "$in": [
                                    {
                                      "$lower": {
                                        "$trim": {
                                          "$ref": "request.quality"
                                        }
                                      }
                                    },
                                    [
                                      "1k",
                                      "2k"
                                    ]
                                  ]
                                }
                              ]
                            },
                            "then": {
                              "$upper": {
                                "$trim": {
                                  "$ref": "request.quality"
                                }
                              }
                            }
                          }
                        ]
                      }
                    },
                    "size": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$and": [
                                {
                                  "$eq": [
                                    {
                                      "$at": [
                                        {
                                          "$split": [
                                            {
                                              "$ref": "request.model"
                                            },
                                            "/"
                                          ]
                                        },
                                        0
                                      ]
                                    },
                                    "bytedance"
                                  ]
                                },
                                {
                                  "$in": [
                                    {
                                      "$lower": {
                                        "$trim": {
                                          "$ref": "request.quality"
                                        }
                                      }
                                    },
                                    [
                                      "1k",
                                      "2k",
                                      "4k"
                                    ]
                                  ]
                                }
                              ]
                            },
                            "then": {
                              "$upper": {
                                "$trim": {
                                  "$ref": "request.quality"
                                }
                              }
                            }
                          }
                        ]
                      }
                    },
                    "image_input": {
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
                    },
                    "image": {
                      "$omitEmpty": {
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
                    "input_image": {
                      "$omitEmpty": {
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
                    "image_reference_url": {
                      "$omitEmpty": {
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
                    "image_prompt": {
                      "$omitEmpty": {
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
                    "subject_reference": {
                      "$omitEmpty": {
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
                    "quality": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$at": [
                                    {
                                      "$split": [
                                        {
                                          "$ref": "request.model"
                                        },
                                        "/"
                                      ]
                                    },
                                    0
                                  ]
                                },
                                "openai"
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
                    },
                    "moderation": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$at": [
                                    {
                                      "$split": [
                                        {
                                          "$ref": "request.model"
                                        },
                                        "/"
                                      ]
                                    },
                                    0
                                  ]
                                },
                                "openai"
                              ]
                            },
                            "then": {
                              "$coalesce": [
                                {
                                  "$lower": {
                                    "$trim": {
                                      "$ref": "request.providerOptions.replicate-prediction-image.moderation"
                                    }
                                  }
                                },
                                "low"
                              ]
                            }
                          }
                        ]
                      }
                    },
                    "input_images": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$at": [
                                    {
                                      "$split": [
                                        {
                                          "$ref": "request.model"
                                        },
                                        "/"
                                      ]
                                    },
                                    0
                                  ]
                                },
                                "openai"
                              ]
                            },
                            "then": {
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
                          }
                        ]
                      }
                    }
                  }
                ]
              }
            },
            "webhook": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.replicate-prediction-image.webhook"
              }
            },
            "webhook_events_filter": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.replicate-prediction-image.webhook_events_filter"
              }
            }
          }
        },
        "poll": {
          "method": "GET",
          "path": "/v1/predictions/{{taskId}}"
        },
        "cancel": {
          "method": "POST",
          "path": "/v1/predictions/{{taskId}}/cancel"
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.id"
              },
              {
                "$ref": "response.request_id"
              },
              {
                "$ref": "response.prompt_id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$coalesce": [
              {
                "$ref": "response.status"
              },
              {
                "$ref": "response.state"
              },
              {
                "$ref": "response.data.status"
              },
              "pending"
            ]
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
              }
            ]
          },
          "images": {
            "$ref": "response.output"
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
