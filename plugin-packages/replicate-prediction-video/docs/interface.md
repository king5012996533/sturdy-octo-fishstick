# Replicate Predictions Video 接口字段

## 协议身份

- 插件 ID：`replicate-prediction-video`。
- Provider ID：`replicate-prediction-video`。
- 能力：`video`。
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
| `prompt` | string | 是 | `prompt/content/input` | 视频提示词。 |
| `images` | media[] | 否 | `image`（bytedance/google）/ `start_image`（kwaivgi）/ `first_frame_image`（minimax）/ `end_image`（kwaivgi）/ `last_frame`（google）/ `last_frame_image`（minimax）/ `reference_images`（bytedance/google） | 按 role 分发；字段名按厂商收窄，上游对多余字段是硬校验，多发一个别名就会被 422 拒绝。 |
| `videos` | media[] | 否 | `reference video` | 参考视频。 |
| `audios` | media[] | 否 | `reference audio/voice` | 参考音频或音色。 |
| `duration` | integer | 否 | `duration` | 时长秒数；上游没有 `seconds` 字段，多发别名会被严格校验的模型拒绝。 |
| `aspectRatio` | string | 否 | `aspect_ratio` / `size` | 比例字符串；wan 族按比例 + 分辨率换算成像素尺寸。 |
| `resolution` | string | 否 | `resolution` / `quality` | 分辨率档位；pixverse 的档位字段名是 `quality`，不下发 `resolution`。 |
| `generateAudio` | boolean | 否 | `generate_audio` | 是否生成音频；目前只有 veo-3 一族接受。 |
| `watermark` | boolean | 否 | `watermark` | 水印开关。 |
| `providerOptions` | object | 否 | `provider-specific fields` | 插件命名空间内的厂商扩展字段。 |

## 上游请求模板逐字段清单

下表由插件请求模板生成，覆盖 body、query、headers 和 multipart 文件声明中的每个字段。

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.pathTemplate` | `{"$concat":["/v1/models/",{"$ref":"request.model"},"/predictions"]}` |
| `create.contentType` | `"application/json"` |
| `create.body.input` | `{"$omitEmpty":{"$coalesce":[{"$ref":"request.providerOptions.replicate-prediction-video.input"},{"prompt":{"$ref":"request.prompt"},"duration":{"$if":{"condition":{"$gt":[{"$ref":"request.duration"},0]},"then":{"$ref":"request.duration"}}},"seconds":{"$if":{"condition":{"$gt":[{"$ref":"request.duration"},0]},"then":{"$ref":"request.duration"}}},"aspect_ratio":{"$omitEmpty":{"$ref":"request.aspectRatio"}},"ratio":{"$omitEmpty":{"$ref":"request.aspectRatio"}},"resolution":{"$omitEmpty":{"$ref":"request.resolution"}},"size":{"$switch":{"cases":[{"when":{"$and":[{"$eq":[{"$ref":"request.aspectRatio"},"16:9"]},{"$eq":[{"$ref":"request.resolution"},"480p"]}]},"then":"832*480"},{"when":{"$and":[{"$eq":[{"$ref":"request.aspectRatio"},"16:9"]},{"$eq":[{"$ref":"request.resolution"},"720p"]}]},"then":"1280*720"},{"when":{"$and":[{"$eq":[{"$ref":"request.aspectRatio"},"16:9"]},{"$eq":[{"$ref":"request.resolution"},"1080p"]}]},"then":"1920*1080"},{"when":{"$and":[{"$eq":[{"$ref":"request.aspectRatio"},"9:16"]},{"$eq":[{"$ref":"request.resolution"},"480p"]}]},"then":"480*832"},{"when":{"$and":[{"$eq":[{"$ref":"request.aspectRatio"},"9:16"]},{"$eq":[{"$ref":"request.resolution"},"720p"]}]},"then":"720*1280"},{"when":{"$and":[{"$eq":[{"$ref":"request.aspectRatio"},"9:16"]},{"$eq":[{"$ref":"request.resolution"},"1080p"]}]},"then":"1080*1920"}]}},"quality":{"$switch":{"cases":[{"when":{"$eq":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},0]},"pixverse"]},"then":{"$ref":"request.resolution"}}]}},"generate_audio":{"$switch":{"cases":[{"when":{"$eq":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},0]},"google"]},"then":{"$ref":"request.generateAudio"}}]}},"images":{"$omitEmpty":{"$map":{"from":{"$ref":"request.images"},"as":"media","in":{"$ref":"media.value"}}}},"videos":{"$omitEmpty":{"$map":{"from":{"$ref":"request.videos"},"as":"media","in":{"$ref":"media.value"}}}},"audios":{"$omitEmpty":{"$map":{"from":{"$ref":"request.audios"},"as":"media","in":{"$ref":"media.value"}}}},"image":{"$omitEmpty":{"$if":{"condition":{"$ne":[{"$ref":"request.operation"},"reference_to_video"]},"then":{"$coalesce":[{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"first_frame"]}}},"as":"media","in":{"$ref":"media.value"}}}},{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},""]}}},"as":"media","in":{"$ref":"media.value"}}}},{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"reference_image"]}}},"as":"media","in":{"$ref":"media.value"}}}}]}}}},"start_image":{"$omitEmpty":{"$if":{"condition":{"$ne":[{"$ref":"request.operation"},"reference_to_video"]},"then":{"$coalesce":[{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"first_frame"]}}},"as":"media","in":{"$ref":"media.value"}}}},{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},""]}}},"as":"media","in":{"$ref":"media.value"}}}},{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"reference_image"]}}},"as":"media","in":{"$ref":"media.value"}}}}]}}}},"first_frame_image":{"$omitEmpty":{"$if":{"condition":{"$ne":[{"$ref":"request.operation"},"reference_to_video"]},"then":{"$coalesce":[{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"first_frame"]}}},"as":"media","in":{"$ref":"media.value"}}}},{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},""]}}},"as":"media","in":{"$ref":"media.value"}}}},{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"reference_image"]}}},"as":"media","in":{"$ref":"media.value"}}}}]}}}},"end_image":{"$omitEmpty":{"$if":{"condition":{"$ne":[{"$ref":"request.operation"},"reference_to_video"]},"then":{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"last_frame"]}}},"as":"media","in":{"$ref":"media.value"}}}}}}},"last_frame_image":{"$omitEmpty":{"$if":{"condition":{"$ne":[{"$ref":"request.operation"},"reference_to_video"]},"then":{"$first":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"last_frame"]}}},"as":"media","in":{"$ref":"media.value"}}}}}}},"reference_images":{"$omitEmpty":{"$map":{"from":{"$filter":{"from":{"$sortByOrder":{"$ref":"request.images"}},"as":"media","where":{"$eq":[{"$ref":"media.role"},"reference_image"]}}},"as":"media","in":{"$ref":"media.value"}}}}}]}}` |
| `create.body.webhook` | `{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-video.webhook"}}` |
| `create.body.webhook_events_filter` | `{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-video.webhook_events_filter"}}` |
| `poll.method` | `"GET"` |
| `poll.path` | `"/v1/predictions/{{taskId}}"` |
| `poll.contentType` | `"application/json"` |
| `cancel.method` | `"POST"` |
| `cancel.path` | `"/v1/predictions/{{taskId}}/cancel"` |
| `cancel.contentType` | `"application/json"` |

## Provider 扩展键

- `providerOptions.replicate-prediction-video.input`
- `providerOptions.replicate-prediction-video.webhook`
- `providerOptions.replicate-prediction-video.webhook_events_filter`

动态模型或工作流允许使用文档声明的完整 `parameters/input/extra_body` 对象；该对象是协议本身的开放 schema，不会被宿主裁剪。

## 响应映射逐字段清单

| 映射位置 | 上游路径或转换表达式 |
| --- | --- |
| `response.taskId` | `{"$coalesce":[{"$ref":"response.id"},{"$ref":"response.request_id"},{"$ref":"response.prompt_id"},{"$ref":"taskId"}]}` |
| `response.status` | `{"$coalesce":[{"$ref":"response.status"},{"$ref":"response.state"},{"$ref":"response.data.status"},"pending"]}` |
| `response.message` | `{"$coalesce":[{"$ref":"response.error.message"},{"$ref":"response.message"},{"$ref":"response.fail_reason"}]}` |
| `response.videos` | `{"$ref":"response.output"}` |
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
  "id": "replicate-prediction-video",
  "name": "Replicate Predictions Video",
  "version": "2.1.0",
  "author": "BeefTV Contributors",
  "description": "Replicate Predictions Video 独立请求协议插件。",
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
        "id": "replicate-prediction-video",
        "label": "Replicate Predictions Video",
        "capabilities": [
          "video"
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
            "description": "视频模型 ID。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt/content/input",
            "description": "视频提示词。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "first_frame / last_frame / reference_image",
            "description": "显式 role 图片输入。"
          },
          {
            "name": "videos",
            "type": "media[]",
            "required": false,
            "mapping": "reference video",
            "description": "参考视频。"
          },
          {
            "name": "audios",
            "type": "media[]",
            "required": false,
            "mapping": "reference audio/voice",
            "description": "参考音频或音色。"
          },
          {
            "name": "duration",
            "type": "integer",
            "required": false,
            "mapping": "duration",
            "description": "时长秒数。上游没有 seconds 字段，别名会被严格校验的模型拒绝。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "required": false,
            "mapping": "aspect_ratio（wan 族换算成 size 像素尺寸）",
            "description": "画幅比例或尺寸。"
          },
          {
            "name": "resolution",
            "type": "string",
            "required": false,
            "mapping": "resolution / quality",
            "description": "分辨率档位。"
          },
          {
            "name": "generateAudio",
            "type": "boolean",
            "required": false,
            "mapping": "generate_audio",
            "description": "是否生成音频。"
          },
          {
            "name": "watermark",
            "type": "boolean",
            "required": false,
            "mapping": "watermark",
            "description": "水印开关。"
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
                    "$ref": "request.providerOptions.replicate-prediction-video.input"
                  },
                  {
                    "prompt": {
                      "$ref": "request.prompt"
                    },
                    "duration": {
                      "$if": {
                        "condition": {
                          "$gt": [
                            {
                              "$ref": "request.duration"
                            },
                            0
                          ]
                        },
                        "then": {
                          "$ref": "request.duration"
                        }
                      }
                    },
                    "aspect_ratio": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$ne": [
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
                                  "wan-video"
                                ]
                              },
                              "then": {
                                "$ref": "request.aspectRatio"
                              }
                            }
                          ]
                        }
                      }
                    },
                    "resolution": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$ne": [
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
                                  "pixverse"
                                ]
                              },
                              "then": {
                                "$ref": "request.resolution"
                              }
                            }
                          ]
                        }
                      }
                    },
                    "size": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$in": [
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
                                    1
                                  ]
                                },
                                [
                                  "wan-2.5-t2v",
                                  "wan-2.5-i2v"
                                ]
                              ]
                            },
                            "then": {
                              "$switch": {
                                "cases": [
                                  {
                                    "when": {
                                      "$and": [
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.aspectRatio"
                                            },
                                            "16:9"
                                          ]
                                        },
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.resolution"
                                            },
                                            "480p"
                                          ]
                                        }
                                      ]
                                    },
                                    "then": "832*480"
                                  },
                                  {
                                    "when": {
                                      "$and": [
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.aspectRatio"
                                            },
                                            "16:9"
                                          ]
                                        },
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.resolution"
                                            },
                                            "720p"
                                          ]
                                        }
                                      ]
                                    },
                                    "then": "1280*720"
                                  },
                                  {
                                    "when": {
                                      "$and": [
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.aspectRatio"
                                            },
                                            "16:9"
                                          ]
                                        },
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.resolution"
                                            },
                                            "1080p"
                                          ]
                                        }
                                      ]
                                    },
                                    "then": "1920*1080"
                                  },
                                  {
                                    "when": {
                                      "$and": [
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.aspectRatio"
                                            },
                                            "9:16"
                                          ]
                                        },
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.resolution"
                                            },
                                            "480p"
                                          ]
                                        }
                                      ]
                                    },
                                    "then": "480*832"
                                  },
                                  {
                                    "when": {
                                      "$and": [
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.aspectRatio"
                                            },
                                            "9:16"
                                          ]
                                        },
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.resolution"
                                            },
                                            "720p"
                                          ]
                                        }
                                      ]
                                    },
                                    "then": "720*1280"
                                  },
                                  {
                                    "when": {
                                      "$and": [
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.aspectRatio"
                                            },
                                            "9:16"
                                          ]
                                        },
                                        {
                                          "$eq": [
                                            {
                                              "$ref": "request.resolution"
                                            },
                                            "1080p"
                                          ]
                                        }
                                      ]
                                    },
                                    "then": "1080*1920"
                                  }
                                ]
                              }
                            }
                          }
                        ]
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
                                "pixverse"
                              ]
                            },
                            "then": {
                              "$ref": "request.resolution"
                            }
                          }
                        ]
                      }
                    },
                    "generate_audio": {
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
                                "google"
                              ]
                            },
                            "then": {
                              "$ref": "request.generateAudio"
                            }
                          }
                        ]
                      }
                    },
                    "image": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$and": [
                                  {
                                    "$in": [
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
                                      [
                                        "bytedance",
                                        "google"
                                      ]
                                    ]
                                  },
                                  {
                                    "$ne": [
                                      {
                                        "$ref": "request.operation"
                                      },
                                      "reference_to_video"
                                    ]
                                  }
                                ]
                              },
                              "then": {
                                "$coalesce": [
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                "first_frame"
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  },
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                ""
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  },
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                "reference_image"
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  }
                                ]
                              }
                            }
                          ]
                        }
                      }
                    },
                    "start_image": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$and": [
                                  {
                                    "$in": [
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
                                      [
                                        "kwaivgi"
                                      ]
                                    ]
                                  },
                                  {
                                    "$ne": [
                                      {
                                        "$ref": "request.operation"
                                      },
                                      "reference_to_video"
                                    ]
                                  }
                                ]
                              },
                              "then": {
                                "$coalesce": [
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                "first_frame"
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  },
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                ""
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  },
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                "reference_image"
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  }
                                ]
                              }
                            }
                          ]
                        }
                      }
                    },
                    "first_frame_image": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$and": [
                                  {
                                    "$in": [
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
                                      [
                                        "minimax"
                                      ]
                                    ]
                                  },
                                  {
                                    "$ne": [
                                      {
                                        "$ref": "request.operation"
                                      },
                                      "reference_to_video"
                                    ]
                                  }
                                ]
                              },
                              "then": {
                                "$coalesce": [
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                "first_frame"
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  },
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                ""
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  },
                                  {
                                    "$first": {
                                      "$map": {
                                        "from": {
                                          "$filter": {
                                            "from": {
                                              "$sortByOrder": {
                                                "$ref": "request.images"
                                              }
                                            },
                                            "as": "media",
                                            "where": {
                                              "$eq": [
                                                {
                                                  "$ref": "media.role"
                                                },
                                                "reference_image"
                                              ]
                                            }
                                          }
                                        },
                                        "as": "media",
                                        "in": {
                                          "$ref": "media.value"
                                        }
                                      }
                                    }
                                  }
                                ]
                              }
                            }
                          ]
                        }
                      }
                    },
                    "end_image": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$and": [
                                  {
                                    "$in": [
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
                                      [
                                        "kwaivgi"
                                      ]
                                    ]
                                  },
                                  {
                                    "$ne": [
                                      {
                                        "$ref": "request.operation"
                                      },
                                      "reference_to_video"
                                    ]
                                  }
                                ]
                              },
                              "then": {
                                "$first": {
                                  "$map": {
                                    "from": {
                                      "$filter": {
                                        "from": {
                                          "$sortByOrder": {
                                            "$ref": "request.images"
                                          }
                                        },
                                        "as": "media",
                                        "where": {
                                          "$eq": [
                                            {
                                              "$ref": "media.role"
                                            },
                                            "last_frame"
                                          ]
                                        }
                                      }
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
                    },
                    "last_frame": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$and": [
                                  {
                                    "$in": [
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
                                      [
                                        "google"
                                      ]
                                    ]
                                  },
                                  {
                                    "$ne": [
                                      {
                                        "$ref": "request.operation"
                                      },
                                      "reference_to_video"
                                    ]
                                  }
                                ]
                              },
                              "then": {
                                "$first": {
                                  "$map": {
                                    "from": {
                                      "$filter": {
                                        "from": {
                                          "$sortByOrder": {
                                            "$ref": "request.images"
                                          }
                                        },
                                        "as": "media",
                                        "where": {
                                          "$eq": [
                                            {
                                              "$ref": "media.role"
                                            },
                                            "last_frame"
                                          ]
                                        }
                                      }
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
                    },
                    "last_frame_image": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$and": [
                                  {
                                    "$in": [
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
                                      [
                                        "minimax"
                                      ]
                                    ]
                                  },
                                  {
                                    "$ne": [
                                      {
                                        "$ref": "request.operation"
                                      },
                                      "reference_to_video"
                                    ]
                                  }
                                ]
                              },
                              "then": {
                                "$first": {
                                  "$map": {
                                    "from": {
                                      "$filter": {
                                        "from": {
                                          "$sortByOrder": {
                                            "$ref": "request.images"
                                          }
                                        },
                                        "as": "media",
                                        "where": {
                                          "$eq": [
                                            {
                                              "$ref": "media.role"
                                            },
                                            "last_frame"
                                          ]
                                        }
                                      }
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
                    },
                    "reference_images": {
                      "$omitEmpty": {
                        "$switch": {
                          "cases": [
                            {
                              "when": {
                                "$in": [
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
                                  [
                                    "bytedance",
                                    "google"
                                  ]
                                ]
                              },
                              "then": {
                                "$map": {
                                  "from": {
                                    "$filter": {
                                      "from": {
                                        "$sortByOrder": {
                                          "$ref": "request.images"
                                        }
                                      },
                                      "as": "media",
                                      "where": {
                                        "$eq": [
                                          {
                                            "$ref": "media.role"
                                          },
                                          "reference_image"
                                        ]
                                      }
                                    }
                                  },
                                  "as": "media",
                                  "in": {
                                    "$ref": "media.value"
                                  }
                                }
                              }
                            }
                          ]
                        }
                      }
                    }
                  }
                ]
              }
            },
            "webhook": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.replicate-prediction-video.webhook"
              }
            },
            "webhook_events_filter": {
              "$omitEmpty": {
                "$ref": "request.providerOptions.replicate-prediction-video.webhook_events_filter"
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
          "videos": {
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
