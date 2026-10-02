# Replicate Predictions Audio 接口字段

## 协议身份

- 插件 ID：`replicate-prediction-audio`。
- Provider ID：`replicate-prediction-audio`。
- 能力：`audio`。
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
| `model` | string | 是 | `path /v1/models/{model}/predictions` | 音频模型 ID，owner/name 形式（minimax/speech-2.8-turbo、minimax/music-2.5）。 |
| `prompt` | string | 是 | `text（语音族）/ lyrics（音乐族）` | 语音模型的朗读文本；音乐模型 2.5 的 lyrics 是必填键，同一个输入框填歌词，风格另走 audioInstructions。 |
| `providerOptions` | object | 否 | `provider-specific fields` | 插件命名空间内的厂商扩展字段：voice_id、emotion、pitch、volume、channel、sample_rate、bitrate、subtitle_enable、english_normalization、input。 |

## 模型族与字段收窄

Replicate 的音频模型是一个模型一套输入 schema，把别的模型认得的键发过去会被上游按
未知字段拒绝。插件按模型全名的第二段首词分族（`minimax/speech-2.8-turbo` → `speech`），
每个键只在对应族里求值，不匹配的键在序列化前被裁掉：

| 族 | 命中示例 | 下发的键 | 不下发 |
| --- | --- | --- | --- |
| `speech` | `minimax/speech-2.8-turbo`、`minimax/speech-2.6-hd` | `text`、`voice_id`、`speed`、`language_boost`、`audio_format` | `lyrics`、`prompt` |
| `music` | `minimax/music-2.5` | `lyrics`、`prompt`、`audio_format` | `text`、`voice_id`、`speed`、`language_boost` |

补充约定：

- 创建入口是模型作用域端点 `POST /v1/models/{owner}/{name}/predictions`，查询与取消走
  `GET /v1/predictions/{id}` 与 `POST /v1/predictions/{id}/cancel`。上游没有 `version` 字段，
  这里也不下发。
- 统一字段 `prompt` 落到语音族的 `text`，落到音乐族是 `lyrics`（MiniMax Music 2.5 把
  `lyrics` 标为必填）。音乐的风格描述走 `audioInstructions`，不要写进歌词。
- 语速按语音族的上限 0.5–2.0 收口，越界取值整键丢弃，避免把上一个模型的 4.0 倍速发上去。
- 输出格式按各族枚举收口（语音 mp3/wav/flac/pcm，音乐 mp3/wav/pcm），未列举的取值退回上游默认。
- 音色由协议决定：Replicate 侧是 MiniMax 国际音色（`Wise_Woman`、`English_Wiselady` 等），
  与 BeefAPI 的中文音色 id 不通用；前台未指定时不下发 `voice_id`，用上游默认音色。
- `providerOptions.replicate-prediction-audio.input` 是整块逃生口：给了它就完全接管 `input`，
  适合插件还没列举的上游新字段。

## 上游请求模板逐字段清单

下表由插件请求模板生成，覆盖 body、query、headers 和 multipart 文件声明中的每个字段。

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.path` | `"dynamic"` |
| `create.contentType` | `"application/json"` |
| `create.body.input` | `{"$omitEmpty":{"$coalesce":[{"$ref":"request.providerOptions.replicate-prediction-audio.input"},{"text":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$ref":"request.prompt"}}]}},"voice_id":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$coalesce":[{"$ref":"request.extra.audioVoice"},{"$ref":"request.providerOptions.replicate-prediction-audio.voice_id"}]}}}]}},"speed":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$switch":{"cases":[{"when":{"$and":[{"$gte":[{"$toFloat":{"$coalesce":[{"$ref":"request.extra.audioSpeed"},{"$ref":"request.providerOptions.replicate-prediction-audio.speed"}]}},0.5]},{"$lte":[{"$toFloat":{"$coalesce":[{"$ref":"request.extra.audioSpeed"},{"$ref":"request.providerOptions.replicate-prediction-audio.speed"}]}},2]}]},"then":{"$toFloat":{"$coalesce":[{"$ref":"request.extra.audioSpeed"},{"$ref":"request.providerOptions.replicate-prediction-audio.speed"}]}}}]}}}}]}},"language_boost":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$coalesce":[{"$ref":"request.providerOptions.replicate-prediction-audio.language_boost"},"Automatic"]}}}]}},"emotion":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-audio.emotion"}}}]}},"pitch":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-audio.pitch"}}}]}},"volume":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-audio.volume"}}}]}},"channel":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-audio.channel"}}}]}},"sample_rate":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-audio.sample_rate"}}}]}},"bitrate":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-audio.bitrate"}}}]}},"subtitle_enable":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-audio.subtitle_enable"}}}]}},"english_normalization":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},"then":{"$omitEmpty":{"$ref":"request.providerOptions.replicate-prediction-audio.english_normalization"}}}]}},"lyrics":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"music"]},"then":{"$ref":"request.prompt"}}]}},"prompt":{"$switch":{"cases":[{"when":{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"music"]},"then":{"$omitEmpty":{"$ref":"request.extra.audioInstructions"}}}]}},"audio_format":{"$switch":{"cases":[{"when":{"$and":[{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"speech"]},{"$in":[{"$coalesce":[{"$ref":"request.extra.audioFormat"},{"$ref":"request.providerOptions.replicate-prediction-audio.audio_format"},"mp3"]},["mp3","wav","flac","pcm"]]}]},"then":{"$coalesce":[{"$ref":"request.extra.audioFormat"},{"$ref":"request.providerOptions.replicate-prediction-audio.audio_format"},"mp3"]}},{"when":{"$and":[{"$eq":[{"$lower":{"$at":[{"$split":[{"$at":[{"$split":[{"$ref":"request.model"},"/"]},1]},"-"]},0]}},"music"]},{"$in":[{"$coalesce":[{"$ref":"request.extra.audioFormat"},{"$ref":"request.providerOptions.replicate-prediction-audio.audio_format"},"mp3"]},["mp3","wav","pcm"]]}]},"then":{"$coalesce":[{"$ref":"request.extra.audioFormat"},{"$ref":"request.providerOptions.replicate-prediction-audio.audio_format"},"mp3"]}}]}}}]}}` |
| `poll.method` | `"GET"` |
| `poll.path` | `"/v1/predictions/{{taskId}}"` |
| `poll.contentType` | `"application/json"` |
| `cancel.method` | `"POST"` |
| `cancel.path` | `"/v1/predictions/{{taskId}}/cancel"` |
| `cancel.contentType` | `"application/json"` |

## Provider 扩展键

- `providerOptions.replicate-prediction-audio.audio_format`
- `providerOptions.replicate-prediction-audio.bitrate`
- `providerOptions.replicate-prediction-audio.channel`
- `providerOptions.replicate-prediction-audio.emotion`
- `providerOptions.replicate-prediction-audio.english_normalization`
- `providerOptions.replicate-prediction-audio.input`
- `providerOptions.replicate-prediction-audio.language_boost`
- `providerOptions.replicate-prediction-audio.pitch`
- `providerOptions.replicate-prediction-audio.sample_rate`
- `providerOptions.replicate-prediction-audio.speed`
- `providerOptions.replicate-prediction-audio.subtitle_enable`
- `providerOptions.replicate-prediction-audio.voice_id`
- `providerOptions.replicate-prediction-audio.volume`

动态模型或工作流允许使用文档声明的完整 `parameters/input/extra_body` 对象；该对象是协议本身的开放 schema，不会被宿主裁剪。

## 响应映射逐字段清单

| 映射位置 | 上游路径或转换表达式 |
| --- | --- |
| `response.taskId` | `{"$coalesce":[{"$ref":"response.id"},{"$ref":"response.request_id"},{"$ref":"taskId"}]}` |
| `response.status` | `{"$coalesce":[{"$ref":"response.status"},{"$ref":"response.state"},"starting"]}` |
| `response.message` | `{"$coalesce":[{"$ref":"response.error.message"},{"$ref":"response.error"},{"$ref":"response.detail"},{"$ref":"response.message"}]}` |
| `response.audios` | `{"$ref":"response.output"}` |
| `response.errorPaths[0]` | `"detail"` |
| `response.errorPaths[1]` | `"error.code"` |
| `response.resultEphemeral` | `true` |

## 响应与错误

插件把上游 task/status/text/media/usage 映射为统一结果。临时媒体 URL 标记为 ephemeral，由宿主立即下载持久化。HTTP 错误、业务 code 和 error object 保持失败语义，不包装成成功。

## 兼容边界

Replicate 音频模型逐模型收窄输入：minimax/speech-* 下发 text/voice_id/speed/audio_format/language_boost，minimax/music-* 只下发 lyrics/prompt/audio_format，两族互不相认的键一律不发。语速按上游 0.5–2.0 收口，输出格式按各族枚举收口，未列举的取值退回上游默认。输出是 30 分钟过期的临时 URL，标记 ephemeral 由宿主立即下载持久化。

<!-- BEEFTV_PLUGIN_MANIFEST_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "beeftv.plugin/v2",
  "id": "replicate-prediction-audio",
  "name": "Replicate Predictions Audio",
  "version": "2.0.0",
  "author": "BeefTV Contributors",
  "description": "Replicate Predictions Audio 独立请求协议插件。",
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
        "id": "replicate-prediction-audio",
        "label": "Replicate Predictions Audio",
        "capabilities": [
          "audio"
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
            "mapping": "path /v1/models/{model}/predictions",
            "description": "音频模型 ID，owner/name 形式（minimax/speech-2.8-turbo、minimax/music-2.5）。"
          },
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "text（语音族）/ lyrics（音乐族）",
            "description": "语音模型的朗读文本；音乐模型 2.5 的 lyrics 是必填键，同一个输入框填歌词，风格另走 audioInstructions。"
          },
          {
            "name": "providerOptions",
            "type": "object",
            "required": false,
            "mapping": "provider-specific fields",
            "description": "插件命名空间内的厂商扩展字段：voice_id、emotion、pitch、volume、channel、sample_rate、bitrate、subtitle_enable、english_normalization、input。"
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
                    "$ref": "request.providerOptions.replicate-prediction-audio.input"
                  },
                  {
                    "text": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$ref": "request.prompt"
                            }
                          }
                        ]
                      }
                    },
                    "voice_id": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$coalesce": [
                                  {
                                    "$ref": "request.extra.audioVoice"
                                  },
                                  {
                                    "$ref": "request.providerOptions.replicate-prediction-audio.voice_id"
                                  }
                                ]
                              }
                            }
                          }
                        ]
                      }
                    },
                    "speed": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$switch": {
                                  "cases": [
                                    {
                                      "when": {
                                        "$and": [
                                          {
                                            "$gte": [
                                              {
                                                "$toFloat": {
                                                  "$coalesce": [
                                                    {
                                                      "$ref": "request.extra.audioSpeed"
                                                    },
                                                    {
                                                      "$ref": "request.providerOptions.replicate-prediction-audio.speed"
                                                    }
                                                  ]
                                                }
                                              },
                                              0.5
                                            ]
                                          },
                                          {
                                            "$lte": [
                                              {
                                                "$toFloat": {
                                                  "$coalesce": [
                                                    {
                                                      "$ref": "request.extra.audioSpeed"
                                                    },
                                                    {
                                                      "$ref": "request.providerOptions.replicate-prediction-audio.speed"
                                                    }
                                                  ]
                                                }
                                              },
                                              2
                                            ]
                                          }
                                        ]
                                      },
                                      "then": {
                                        "$toFloat": {
                                          "$coalesce": [
                                            {
                                              "$ref": "request.extra.audioSpeed"
                                            },
                                            {
                                              "$ref": "request.providerOptions.replicate-prediction-audio.speed"
                                            }
                                          ]
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
                    "language_boost": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$coalesce": [
                                  {
                                    "$ref": "request.providerOptions.replicate-prediction-audio.language_boost"
                                  },
                                  "Automatic"
                                ]
                              }
                            }
                          }
                        ]
                      }
                    },
                    "emotion": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.providerOptions.replicate-prediction-audio.emotion"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "pitch": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.providerOptions.replicate-prediction-audio.pitch"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "volume": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.providerOptions.replicate-prediction-audio.volume"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "channel": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.providerOptions.replicate-prediction-audio.channel"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "sample_rate": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.providerOptions.replicate-prediction-audio.sample_rate"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "bitrate": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.providerOptions.replicate-prediction-audio.bitrate"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "subtitle_enable": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.providerOptions.replicate-prediction-audio.subtitle_enable"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "english_normalization": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "speech"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.providerOptions.replicate-prediction-audio.english_normalization"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "lyrics": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "music"
                              ]
                            },
                            "then": {
                              "$ref": "request.prompt"
                            }
                          }
                        ]
                      }
                    },
                    "prompt": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$eq": [
                                {
                                  "$lower": {
                                    "$at": [
                                      {
                                        "$split": [
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
                                          "-"
                                        ]
                                      },
                                      0
                                    ]
                                  }
                                },
                                "music"
                              ]
                            },
                            "then": {
                              "$omitEmpty": {
                                "$ref": "request.extra.audioInstructions"
                              }
                            }
                          }
                        ]
                      }
                    },
                    "audio_format": {
                      "$switch": {
                        "cases": [
                          {
                            "when": {
                              "$and": [
                                {
                                  "$eq": [
                                    {
                                      "$lower": {
                                        "$at": [
                                          {
                                            "$split": [
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
                                              "-"
                                            ]
                                          },
                                          0
                                        ]
                                      }
                                    },
                                    "speech"
                                  ]
                                },
                                {
                                  "$in": [
                                    {
                                      "$coalesce": [
                                        {
                                          "$ref": "request.extra.audioFormat"
                                        },
                                        {
                                          "$ref": "request.providerOptions.replicate-prediction-audio.audio_format"
                                        },
                                        "mp3"
                                      ]
                                    },
                                    [
                                      "mp3",
                                      "wav",
                                      "flac",
                                      "pcm"
                                    ]
                                  ]
                                }
                              ]
                            },
                            "then": {
                              "$coalesce": [
                                {
                                  "$ref": "request.extra.audioFormat"
                                },
                                {
                                  "$ref": "request.providerOptions.replicate-prediction-audio.audio_format"
                                },
                                "mp3"
                              ]
                            }
                          },
                          {
                            "when": {
                              "$and": [
                                {
                                  "$eq": [
                                    {
                                      "$lower": {
                                        "$at": [
                                          {
                                            "$split": [
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
                                              "-"
                                            ]
                                          },
                                          0
                                        ]
                                      }
                                    },
                                    "music"
                                  ]
                                },
                                {
                                  "$in": [
                                    {
                                      "$coalesce": [
                                        {
                                          "$ref": "request.extra.audioFormat"
                                        },
                                        {
                                          "$ref": "request.providerOptions.replicate-prediction-audio.audio_format"
                                        },
                                        "mp3"
                                      ]
                                    },
                                    [
                                      "mp3",
                                      "wav",
                                      "pcm"
                                    ]
                                  ]
                                }
                              ]
                            },
                            "then": {
                              "$coalesce": [
                                {
                                  "$ref": "request.extra.audioFormat"
                                },
                                {
                                  "$ref": "request.providerOptions.replicate-prediction-audio.audio_format"
                                },
                                "mp3"
                              ]
                            }
                          }
                        ]
                      }
                    }
                  }
                ]
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
              "starting"
            ]
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.error"
              },
              {
                "$ref": "response.detail"
              },
              {
                "$ref": "response.message"
              }
            ]
          },
          "audios": {
            "$ref": "response.output"
          },
          "errorPaths": [
            "detail",
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
